# Changelog

All notable changes to this project are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com); this project adheres to
semantic versioning once released.

## [Unreleased]

## [v0.2.0] — 2026-08-08

First release since `v0.1.4` (2026-07-21), covering nine commits. Headlines:
**all 16 MAESTRO threat-model findings are now remediated** (the last three —
attribute redaction, gRPC per-connection bounds, `-db-path` validation — land
here), HTTP ingest accepts **OTLP/JSON** so clients that only speak it no longer
get a 400 on every export, and ingest rejections are no longer silent. Minor
rather than patch: `-redact-attrs` and OTLP/JSON are new user-visible surface.

No breaking changes to flags, the query API, or the store schema; upgrading is a
binary swap. Redaction is opt-in and off by default, so behaviour is unchanged
unless you set `-redact-attrs`.

### Added — 2026-08-07 (attribute redaction; threat-model backlog closed)

- **`-redact-attrs` / `OTELSTORE_REDACT_ATTRS`: opt-in attribute redaction at
  ingest** (threat-model #10, CWE-312). Telemetry legitimately carries secrets and
  PII in attribute values — `Authorization` headers, prompts, user identifiers —
  and otelstore persists attributes verbatim, so the database file outlives the
  incident it was opened for. A comma-separated list of exact keys or prefix globs
  (`authorization,gen_ai.prompt*`, case-insensitive) replaces matching values with
  `[REDACTED]` before anything is written, so a redacted value never reaches disk.
  The key is kept so a reader can tell "withheld" from "never emitted".
  - **The store stays generic.** Rule names live entirely in operator config; the
    new `internal/redact` package is a value `main.go` constructs and hands to the
    store, which applies it without interpreting any key. No default deny-list, and
    no `gen_ai.*` special-casing in `internal/store`.
  - Every record-level insert path goes through one `Store.mergedAttrs` choke
    point, so a new signal cannot accidentally skip redaction. Span *events* carry
    a second, nested attribute map that is not merged with resource/scope, so
    `InsertSpans` redacts those separately — otherwise an exporter putting a prompt
    on a span event rather than the span would bypass redaction. Redaction runs
    *before* `run_id`/`job_id` are promoted to indexed columns — extracting first
    would copy an unredacted value into a column and defeat the redaction.
  - Default behaviour is unchanged: with no rules configured, nothing is altered.
- **gRPC per-connection bounds** (threat-model #15, CWE-400). `MaxRecvMsgSize`
  caps one message, but grpc-go defaults `MaxConcurrentStreams` to unlimited and
  `MaxConnectionIdle` to infinity, so a client could still pin memory with many
  streams or idle connections. Now `MaxConcurrentStreams: 256`,
  `MaxConnectionIdle: 5m`, and keepalive enforcement (`MinTime: 30s`,
  `PermitWithoutStream: true` so an idle-but-healthy OTLP exporter isn't torn
  down). Generous enough not to constrain a real exporter.
- **`-db-path` validation** (threat-model #14, CWE-427). `modernc.org/sqlite`
  accepts URI-form paths, so `file:x?_pragma=...` in `-db-path` could reconfigure
  the database engine through what is documented as a plain filename. `:memory:`
  and plain paths are accepted (and `filepath.Clean`ed); a `file:` prefix or an
  embedded `?` is rejected. Defense-in-depth for a launch config that is less
  trusted than the operator — `-db-path` is not attacker-facing input.

With these, **all 16 MAESTRO findings are remediated**; `THREAT_MODEL.md` carries
a remediation-status table.

### Fixed — 2026-08-07 (concurrent ingest on the default in-memory store)

- **Concurrent ingest against `-db-path :memory:` failed with `no such table:
  spans`.** Each connection to a plain `:memory:` DSN gets its own private
  database, so the second connection the pool opened saw no schema. Sequential use
  never noticed — the pool hands back the one connection — but concurrent ingest
  did, and `:memory:` is the default. The in-memory pool is now pinned to one
  connection, so concurrent writers serialize instead of failing; SQLite already
  serializes writes, and file-backed stores are unaffected. Found by the new gRPC
  concurrency test, not by the threat model.

### Added — 2026-08-07 (formatting gate)

- **CI fails on unformatted code.** The `build · vet · test` job now runs
  `gofmt -l .` first (renamed `gofmt · build · vet · test`). Nothing in the gate
  previously looked at formatting — `go build`, `go vet`, `go test`, gosec,
  staticcheck and govulncheck all pass on unformatted source — so drift
  accumulated unnoticed in nine files. `gofmt -l` exits 0 even when it lists
  files, so the step tests its output explicitly and prints `gofmt -d .` before
  failing. Documented in `CONTRIBUTING.md` and `CLAUDE.md`.

### Fixed — 2026-08-07 (line endings, formatting drift)

- **`.gitattributes` pins `eol=lf`** for all text files. Without it,
  `core.autocrlf=true` on Windows produced a CRLF working copy against LF
  committed content, so `gofmt -l .` flagged all 20 Go files locally while CI
  (Linux) was clean — enough noise to hide a real regression. `*.sh` also needs
  LF because `scripts/build-release.sh` runs under `sh` and a CRLF shebang fails
  with `bad interpreter`. `git add --renormalize .` staged no changes, confirming
  committed content was already LF.
- **`gofmt -w` across nine files** — mis-ordered imports within a group and
  struct-tag/trailing-comment misalignment. Whitespace and ordering only; no
  logic changed. Only visible once the working copy checked out as LF.

### Added — 2026-08-05 (OTLP/JSON ingest)

- **HTTP ingest accepts OTLP/JSON** (#7). `:4318` now decodes both encodings on
  the same port, per the OTLP spec: `Content-Type: application/json` is decoded
  with `protojson` (`DiscardUnknown: true`, so exporters emitting fields newer
  than our pinned proto aren't turned into 400s); anything else, including an
  absent header, stays binary protobuf. Responses are returned in the request's
  encoding rather than always protobuf. Real clients that only speak OTLP/JSON —
  e.g. the VS Code GitHub Copilot Chat extension — previously got a 400 on every
  export and stored nothing. `README.md` and `docs/usage.md` updated: they
  advertised `HTTP/protobuf` only.
- **Spec-conformant ingest error responses.** OTLP/HTTP requires that `4xx`/`5xx`
  bodies be a `google.rpc.Status` and that the response reuse the request's
  `Content-Type`. Ingest previously answered every failure with a plain-text
  `http.Error`, which was non-conformant for protobuf *and* JSON clients; all
  failure paths now emit a `Status` in the request's encoding. The body carries a
  fixed reason while the underlying error text goes only to the log, so a client
  can't use ingest failures to probe internals (e.g. SQL constraint names).

### Fixed — 2026-08-05 (silent ingest rejections)

- **HTTP ingest rejections are no longer silent** (#8). Every non-2xx ingest
  outcome — bad method, oversized/unreadable body, unmarshal failure, unknown
  route, store-insert error, response marshal/write failure — now logs one line
  with method, path, status, stage, `Content-Type`, body length, and error. A
  receiver that was up but refusing 100% of traffic used to log nothing, making
  "healthy process, empty database" undiagnosable. Log fields are sanitized
  against log injection (CWE-117), matching the auth middleware. The success
  path stays quiet — no line per export.
- Ingest log lines report `read=<n>` (body bytes actually consumed) on every
  rejection, including store failures, which previously logged a hardcoded `0`
  and so misreported a large request as empty.
- `sanitizeForLog` in the receiver additionally strips Unicode line/paragraph
  separators (U+2028/U+2029) and format characters such as bidi overrides. Go's
  logger treats these as ordinary runes, but log viewers and aggregators may
  render them as a line break or use them to disguise a forged line.

## [v0.1.0] – [v0.1.4] — 2026-07-20 / 2026-07-21

Everything below shipped across the five `v0.1.x` tags. Those releases were cut
without stamping this file, so the sections sat under `[Unreleased]`; they are
grouped here rather than split per tag, since attributing each one to a specific
`v0.1.x` after the fact would be guesswork.

### Added — 2026-07-21 (threat-model hardening backlog)

- **Audit logging:** auth failures are now logged (HTTP middleware + gRPC
  interceptor) with source, path/method, and reason — never the token. Logged
  fields are sanitized against log injection (CWE-117).
- **Non-loopback warning:** startup logs a prominent WARNING if any listener
  binds a non-loopback address while auth is disabled.
- **`-auth-token-file` / `OTELSTORE_AUTH_TOKEN_FILE`:** read the bearer token
  from a file to avoid exposing it in process args/env.
- **Release workflow:** `contents: write` scoped to the release job (top-level
  read-only); SBOM (CycloneDX via pinned cyclonedx-gomod) generated and attached
  to releases.
- **Governance:** `.github/dependabot.yml` (gomod, cargo, github-actions);
  `.gitignore` adds `*.jks`/`*.jceks`.

### Security — 2026-07-21 (MAESTRO threat model + fixes)

- **Critical: authenticated the MCP query server.** The MCP endpoint (:4320)
  was not behind the auth middleware, so anyone reaching it could read all
  stored telemetry via `query_job`/`query_run`/`get_trace` even when
  `-auth-token` was set. It is now auth-wrapped like ingest/query; an e2e
  regression test asserts an unauthenticated MCP request gets 401.
- **DoS bounds:** `GetTrace` now applies a `LIMIT` (10000); HTTP ingest caps
  request bodies via `http.MaxBytesReader` (64 MiB); gRPC sets
  `MaxRecvMsgSize` (64 MiB).
- **Supply chain:** CI installs gosec/staticcheck/govulncheck at pinned
  versions instead of `@latest`.
- MCP startup log now reports auth status.
- Full threat model written to `THREAT_MODEL.md`. Remaining lower-severity
  findings (audit logging, non-loopback bind warning, token-file, release.yml
  scoping, optional attribute redaction, SBOM/Dependabot) tracked as backlog.

### Added — 2026-07-21 (event queries, health probes, size retention)

- **Query logs/events** — `GET /v1/logs?event_name=&min_severity=`. OTLP events
  are log records with an `event.name`; it's now promoted to an indexed column
  and directly queryable, optionally filtered by minimum severity. New
  `store.QueryLogs`.
- **Health probes** — `GET /healthz` (liveness) and `GET /readyz` (store ping;
  503 if down). Both always bypass bearer auth so external health-checkers
  (Traefik, k8s, containers) can probe without a token.
- **Size-based retention** — `-max-size <bytes>` evicts oldest rows (FIFO) once
  the DB exceeds the cap, complementing the existing age-based `-retention`
  (e.g. `4320h` = 180 days). New `store.EnforceMaxSize` / `store.DBSize`.

### Added — 2026-07-21 (polish)

- `-version` flag and a startup version log line. The version is stamped at
  release time via `-ldflags "-X main.version=<tag>"` (defaults to `dev` for
  local builds); `scripts/build-release.sh` derives it from the git tag.
- README status badges (CI, latest release, license, Go version).

### Added — 2026-07-21 (e2e harness, CI, landing page)

- **End-to-end test harness** (`test/e2e`, `-tags e2e`) — spawns the compiled
  binary as a subprocess on ephemeral ports and drives it over real sockets:
  OTLP traces + metrics over gRPC, logs over HTTP, all with bearer auth, then
  queries each back over REST. Asserts the error span surfaces (status_code=2),
  the metric value round-trips, auth rejects unauthenticated requests, and data
  survives a process restart (real on-disk persistence). Verified on Windows and
  Linux (WSL Ubuntu 24.04). An untagged `doc.go` keeps `go test ./...` green.
- **CI** (`.github/workflows/ci.yml`) — build (CGO-free) · vet · test, a
  dedicated e2e job (`-tags e2e`), and a security job (gosec/staticcheck/
  govulncheck) on every push and PR. Actions pinned by commit SHA.
- **Release** (`.github/workflows/release.yml`) — on a `v*` tag, cross-compiles
  via `scripts/build-release.sh` and publishes binaries + checksums as a GitHub
  Release.
- **Landing page** (`docs/index.html` + `.nojekyll`) — self-contained static
  page for GitHub Pages.

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
  (OTLP endpoint matrix, query reference), MIT `LICENSE`, and
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
  - `emit/` — Go emitter helper (`StartWorkflowSpan`/`StartAgentSpan`/
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
  - `test/e2e_test.go` — end-to-end proof: the `emit/` helper exports via a
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
