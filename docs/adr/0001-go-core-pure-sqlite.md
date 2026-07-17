# 0001. Go core with pure-Go SQLite storage

- Status: accepted
- Date: 2026-07-16

## Context

We are building a headless, single-binary local telemetry component: OTLP
receiver + log/trace store + query API, no UI. It must run natively on macOS,
Linux, and Windows (a hard requirement), be lightweight, and serve a
self-remediation loop for an agentic orchestrator (see docs/overlay-schema.md).

Two language choices were credible: Go (matches the reference implementations —
Jaeger, the OTel Collector, VictoriaLogs) and Rust (matches OpenObserve). The
emitter helpers already exist in both languages; the *core* is new work.

The store must accept continuous low-volume OTLP ingest and serve point queries
by trace_id / run_id / job_id. At local single-machine volume, an embedded SQL
engine is sufficient; a columnar analytics engine (DuckDB) is unnecessary and its
single-writer + continuous-ingest weaknesses are a poor fit.

## Decision

- **Language: Go.** OTLP protobuf definitions are Go-native and mature
  (`go.opentelemetry.io/proto/otlp` v1.10.0), and it matches the ecosystem's
  reference collectors.
- **Storage: `modernc.org/sqlite` (v1.54.0), the pure-Go SQLite.** No CGO, so the
  binary cross-compiles to a single static native executable on all three
  platforms — the reason OpenObserve ships a clean Windows `.exe` is that it has
  no C dependency, and pure-Go SQLite gives us the same property without leaving
  Go. Owned correlation keys (`run_id`, `job_id`) are promoted to indexed
  columns; all attributes (incl. `gen_ai.*`) are stored generically as JSON. The
  store never interprets the overlay schema.
- **Query interface: REST (2 fixed endpoints), OpenAPI-first.** Evaluated against
  GraphQL; the two consumers (a self-healing agent asking a fixed "why did job X
  fail" question, and future Grafana-style connectors) need fixed queries, not
  flexible field selection over a graph. GraphQL's schema+resolver layer adds
  surface for zero MVP benefit. An OpenAPI schema keeps a later MCP wrapper or
  GraphQL front-end cheap to add.

## Consequences

- Easier: one static binary per OS with no C toolchain; reuse of mature Go OTLP
  libraries; trivial JSON/HTTP consumption by Grafana connectors and an MCP
  wrapper; the store stays dumb and stable while the overlay churns on emit.
- Harder / deferred: pure-Go SQLite is slightly slower than CGO SQLite (fine at
  local volume); no columnar analytics (not needed for point queries); retention/
  TTL, gRPC OTLP, auth, and metrics are explicitly out of the MVP and will need
  their own decisions later.
- Reversible cost: if volume ever outgrows SQLite, the generic
  spans/logs-with-JSON-attributes schema ports to another SQL/columnar engine
  without touching emitters or the query API contract.
