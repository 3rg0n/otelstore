# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

otelstore is a single-binary, pure-Go local OpenTelemetry backend: OTLP ingest
(gRPC + HTTP/protobuf) for traces/logs/metrics, a pure-Go SQLite store, a REST
query API, and an MCP server so an agent can query its own telemetry. No CGO,
no external services — the whole point is "one dependency-free binary."

Module: `github.com/3rg0n/otelstore`, single Go module (no `go.work` — the
earlier `emit/go` submodule was folded into `emit/` at the repo root).

## Commands

```sh
# build (CGO must stay disabled — this is the whole point of the project)
CGO_ENABLED=0 go build -o otelstore ./cmd/otelstore

# run
./otelstore -db-path ./telemetry.db

# full gate before a PR
gofmt -l .           # must list nothing (CI fails on any output); fix: gofmt -w .
CGO_ENABLED=0 go build ./...
go vet ./...
go test ./...

# single package / single test
go test ./internal/store/...
go test ./internal/store/ -run TestStoreSpanRoundTrip -v

# end-to-end tests — spawn the compiled binary, drive it over real sockets
# (gated behind the "e2e" tag; NOT part of `go test ./...`)
go test -tags e2e ./test/e2e/ -v

# security/quality tools this repo gates on (CI installs pinned versions)
gosec ./...          # must be 0 issues
staticcheck ./...
govulncheck ./...

# cross-platform release binaries -> dist/ (version stamped from git tag)
sh scripts/build-release.sh
```

CI (`.github/workflows/ci.yml`) runs three jobs on every push/PR:
gofmt+build+vet+test, the tagged e2e suite, and the security scanners above —
all with actions pinned by commit SHA. A `v*` tag triggers `.github/workflows/release.yml`, which builds
release binaries + SBOM (CycloneDX) and publishes a GitHub Release.

## Architecture

Request flow: **gRPC (`:4317`) / HTTP (`:4318`) ingest → `internal/store`
(SQLite) → REST query (`:4319`) or MCP tools (`:4320`)**. All four servers
default to binding `127.0.0.1` (loopback-only) so a plain run isn't exposed to
the LAN; `cmd/otelstore/main.go` warns at startup if a listener binds
non-loopback with no auth token set. `main.go` wires all servers plus auth
middleware, the retention sweeper goroutine, and graceful shutdown.

- **`internal/store`** — the only component that touches SQLite. Owns schema
  (`spans`, `logs`, `metrics` tables), attribute merging (resource → scope →
  span attrs, later wins), and JSON marshaling of everything except a small
  set of promoted columns. **The store is deliberately generic**: only
  `run_id`/`job_id` (and, for logs, `event.name`/severity for the events
  query path) are ever pulled out of attributes into indexed columns. It
  never interprets `gen_ai.*` or any other attribute name — see "Overlay
  schema" below for why. Query methods (`QueryByKey`, `GetTrace`,
  `QueryMetrics`, `QueryLogs`) use a `switch` over a fixed key set to build
  static SQL (avoids gosec G201 / SQL injection by construction — never build
  queries by string concatenation). `GetTrace` is bounded (`LIMIT`), and
  inserts batch in a single transaction (WAL mode) for ingest throughput.
  Retention is two independent mechanisms: `DeleteBefore` (age-based, driven
  by `-retention`) and `EnforceMaxSize`/`DBSize` (size-based FIFO eviction,
  driven by `-max-size`) — both run from the same sweeper goroutine in `main.go`.
- **`internal/receiver`** — HTTP/protobuf OTLP ingest (`/v1/traces`,
  `/v1/logs`, `/v1/metrics`), unmarshals the collector proto types and hands
  spans off to `store`. Request bodies are capped via `http.MaxBytesReader`
  (64 MiB) as a DoS bound.
- **`internal/grpcreceiver`** — gRPC OTLP ingest, same three signals,
  implements the standard `TraceServiceServer` / `LogsServiceServer` /
  `MetricsServiceServer` interfaces, with `MaxRecvMsgSize` capped (64 MiB).
  Has its own auth interceptor (reads the bearer token from gRPC metadata
  instead of HTTP headers), but the token check itself calls the same shared
  `auth.TokenValid()` as the HTTP middleware — the transports don't duplicate
  validation logic, only the boilerplate around extracting the header.
- **`internal/query`** — REST query API: `/v1/traces/{id}`, `/v1/query`
  (exactly one of `job_id`/`run_id`/`trace_id`), `/v1/metrics`, `/v1/logs`
  (events query — optional `event_name`/`min_severity` filters), plus
  `/healthz` (liveness) and `/readyz` (checks the store via `store.Ping`).
  The two health endpoints always bypass bearer auth so external
  health-checkers can probe without a token. Mirrors `api/openapi.yaml`.
- **`internal/mcpserver`** — wraps the same store queries as MCP tools
  (`query_job`, `query_run`, `get_trace`) for agent self-remediation. Tool
  logic lives in named functions (`queryJobHandler`, etc.), *not* anonymous
  closures passed to `mcp.AddTool` — tests call the named functions directly
  so a test can't pass while the registered tool is broken. The MCP HTTP
  handler is auth-wrapped in `main.go` exactly like ingest/query — it was not
  originally, which let anyone reaching `:4320` read all stored telemetry
  even with `-auth-token` set; don't reintroduce an unwrapped MCP endpoint.
- **`internal/auth`** — constant-time bearer token check (`TokenValid`),
  shared by the HTTP middleware and the gRPC interceptor so all transports
  enforce auth identically. Auth failures are audit-logged (source,
  path/method, reason — never the token; fields sanitized against log
  injection). Empty token = auth disabled. The token itself can come from
  `-auth-token`, `OTELSTORE_AUTH_TOKEN`, or (least exposed — not visible in
  argv/env) `-auth-token-file` / `OTELSTORE_AUTH_TOKEN_FILE`.
- **`emit/`** (Go) and **`emit/rust`** — the *only* sanctioned way for
  instrumented code to start spans against this store's contract. See below.
  (This used to be a separate `emit/go` submodule under a `go.work`; it was
  folded into a single module so `go install .../cmd/otelstore@latest` works.)

### Overlay schema (emit-side contract) — read `docs/overlay-schema.md` before touching `emit/`

The store is generic by design and never reads this contract; all structure
lives on the emit side. Key rules, enforced by the conformance test
(`emit/emit_test.go`, `emit/rust/src/lib.rs` tests) and required for the
healer's queries to work:

- **Span name MUST equal `gen_ai.operation.name`, byte-for-byte, across every
  language helper.** This is the most drift-prone rule in the repo — two
  helpers naming the "same" span differently silently splits queries. If you
  add or change an operation, update the table in
  `docs/overlay-schema.md` first, then every language helper.
- `run_id` is required on every span; `job_id` is required on every
  task-level span (everything except the workflow-root span). These are the
  **owned correlation keys** — stable, never subject to OTel semantic
  convention churn. `gen_ai.*` attributes are decoration only, riding along
  for ecosystem interop; never promote a `gen_ai.*` key to a store column.
- Callers use the `Start*Span` helpers in `emit/emit.go` (or the Rust
  equivalent) — never set span names or `gen_ai.*` attributes by hand.

### Ground rules from CONTRIBUTING.md

- Stay single-binary and pure-Go: `CGO_ENABLED=0 go build ./...` must always
  succeed. Don't add a dependency that pulls in a C toolchain.
- Keep the store generic: only `run_id`/`job_id` get promoted to columns;
  don't special-case attribute names (including `gen_ai.*`) in `internal/store`.
- Small surface: no UI, no dashboards, no extra query languages. Richer views
  belong in tools that point at the query API (e.g. Grafana), not in this repo.
- Update `CHANGELOG.md` under `[Unreleased]` for user-visible changes; add an
  ADR under `docs/adr/` for decisions that are costly to reverse (storage
  engine, public API shape, security posture).

### Security notes worth knowing before changing auth/ingest paths

See `THREAT_MODEL.md` for the full MAESTRO write-up. The backlog items already
fixed (audit logging, non-loopback bind warning, token-file, MCP auth gap,
DoS body/message size bounds, pinned CI scanner versions, SBOM/Dependabot) are
the ones most likely to look like "missing" if you're only skimming the code
— check `CHANGELOG.md`'s `[Unreleased]` section before assuming a gap is real.
