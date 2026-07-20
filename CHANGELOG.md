# Changelog

All notable changes to this project are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com); this project adheres to
semantic versioning once released.

## [Unreleased]

### Added — 2026-07-17 (unified all-signal ingest, auth, retention, docs)

- **Metrics signal** — otelstore now handles all three OTLP signals. New
  `metrics` table (generic attributes JSON, `run_id`/`job_id` promoted),
  `store.InsertMetrics` (Gauge + Sum data points), `store.QueryMetrics`,
  HTTP `POST /v1/metrics` ingest, and `GET /v1/metrics?name=` query.
- **gRPC OTLP ingest on `:4317`** (`internal/grpcreceiver`) — Trace + Logs +
  Metrics services, the OTel-default transport. Makes "point Claude Code's OTel
  at it" work out of the box (Claude Code defaults to gRPC).
- **Optional bearer-token auth** (`internal/auth`) — `-auth-token` /
  `OTELSTORE_AUTH_TOKEN`; when set, every HTTP and gRPC endpoint requires
  `Authorization: Bearer <token>`. Exact `Bearer ` prefix check +
  constant-time compare (`crypto/subtle`), shared by the HTTP middleware and
  gRPC interceptors.
- **Retention** — `store.DeleteBefore` and a `-retention` duration flag; a
  background sweeper prunes spans/logs/metrics older than the window so a
  long-running local daemon stays bounded.
- **Docs & release** — README (incl. "Use with Claude Code"), `docs/usage.md`
  (OTLP endpoint matrix, query reference), Apache-2.0 `LICENSE`, and
  `scripts/build-release.sh` (CGO-free binaries for macOS/Linux/Windows).

### Security

- Auth requires the exact `Bearer ` scheme (rejects same-length non-Bearer
  headers) and compares tokens in constant time. Gate clean across the new
  packages: gosec 0 issues, staticcheck/govulncheck/go vet, `CGO_ENABLED=0`
  build. Verified end-to-end against the compiled binary with a real gRPC
  client (metric + trace ingested and queried back; wrong/!Bearer token
  rejected).

### Added — 2026-07-17 (MCP query server)

- **MCP query server** (`internal/mcpserver`) — exposes the store as Model
  Context Protocol tools so an agent can self-remediate over its native
  protocol (official `go-sdk` v1.6.1, streamable HTTP, structured output):
  - `query_job(job_id)` → spans + logs + an explicit `errors` array
    (error-status spans surfaced for the healer).
  - `query_run(run_id)` → spans + logs for a workflow run.
  - `get_trace(trace_id)` → span tree.
  - Thin layer over `store.QueryByKey`/`GetTrace` (no duplicated query logic);
    handlers are named funcs both the server registers and tests invoke.
  - `store.ErrorSpans` helper filters error-status spans (status_code==2).
  - Hosted in `cmd/otelstore` on `-mcp-addr` (default `:4320`) with HTTP
    timeout hardening and graceful shutdown, alongside the ingest/query servers.
- **ADR 0002** — MCP query interface for agent self-remediation.

### Added — 2026-07-17

- **Emit-side conformance kit** — the emit contract for agentic-orchestrator
  telemetry, so emitters produce query-able span shapes:
  - `docs/overlay-schema.md` — the contract: owned correlation keys (`run_id`,
    `job_id`) as the stable spine; OpenTelemetry GenAI semantic conventions
    (`gen_ai.*`) as decoration only; rule that span name == `gen_ai.operation.name`
    byte-for-byte across languages; model op pinned to `chat`.
  - `emit/go` — Go emitter helper (`StartWorkflowSpan`/`StartAgentSpan`/
    `StartPlanSpan`/`StartToolSpan`/`StartModelSpan`/`RecordError`) with a
    real conformance test suite (in-memory exporter; asserts the negative
    case that `job_id` is absent on workflow spans).
  - `emit/rust` — Rust emitter crate mirroring the Go contract byte-for-byte,
    with an equivalent conformance test suite.
- **Headless single-binary MVP** — OTLP in, local store, query out; no UI:
  - `internal/receiver` — OTLP/HTTP receiver (`POST /v1/traces`, `/v1/logs`,
    protobuf); flattens resource/scope/span attributes and promotes owned keys.
  - `internal/store` — pure-Go SQLite store (`modernc.org/sqlite`, no CGO):
    `spans` + `logs` tables, owned keys promoted to indexed columns, all
    attributes stored generically as JSON (never special-cases `gen_ai.*`).
  - `internal/query` — REST query API per `api/openapi.yaml`:
    `GET /v1/traces/{trace_id}` and `GET /v1/query?job_id|run_id|trace_id`
    (exactly-one-filter, correlated spans + logs).
  - `cmd/otelstore` — single binary wiring receiver + query servers with
    hardened HTTP timeouts and graceful shutdown.
  - `test/e2e_test.go` — end-to-end proof: the `emit/go` helper exports via a
    real OTLP HTTP exporter → receiver → store → query, asserting emit span
    names, error status, correlated logs, and trace ordering survive the round trip.
- **`api/openapi.yaml`** — OpenAPI 3.1 schema for the query API (API-first).
- **ADR 0001** — Go core with pure-Go SQLite storage; REST over GraphQL for the
  fixed-query, model-facing consumer.

### Security

- Static per-key SQL (no identifier interpolation); `ReadHeaderTimeout` and
  related timeouts on both HTTP servers. Gate clean: `gosec` 0 issues,
  `staticcheck`, `govulncheck`, `go vet`, and `CGO_ENABLED=0` build all pass.

### Notes / deferred

- Audit-log durability deferred (telemetry rides best-effort OTLP); `run_id`/
  `job_id` on every record keep a future durable audit store join-able.
- Open: pin exact GenAI semconv commit SHA in the overlay; confirm the
  `gen_ai.conversation.id` grain (currently workflow-run) with the orchestrator.
