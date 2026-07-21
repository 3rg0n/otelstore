# otelstore

[![CI](https://github.com/3rg0n/otelstore/actions/workflows/ci.yml/badge.svg)](https://github.com/3rg0n/otelstore/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/3rg0n/otelstore?sort=semver)](https://github.com/3rg0n/otelstore/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8.svg)](https://go.dev)

One binary. The whole local OpenTelemetry stack.

Instead of standing up an OTel Collector **and** Loki **and** Tempo **and**
Prometheus every time you want somewhere to send telemetry, you grab one
dependency-free binary, run it, and point OTLP at it. It ingests **traces, logs,
and metrics** over OTLP (gRPC + HTTP) into a single local file, and lets you
query them back over REST or MCP.

Pure Go, no CGO — a single static executable on macOS, Linux, and Windows. No
external services, no database to provision.

## The whole idea

```
                        ┌──────────────────────────────┐
  many clients ──OTLP──▶│  otelstore                   │──▶ query API
  (any SDK, any OTLP     │  logs · traces · metrics ·   │    REST + MCP
   tool, Claude Code)    │  events  →  one SQLite file  │
                        └──────────────────────────────┘
```

Point anything that emits **OTLP** at it — apps with an OpenTelemetry SDK,
auto-instrumentation agents, Claude Code, or even an upstream OTel Collector.
otelstore receives every signal, stores it in one file, and answers queries.
Think VictoriaLogs + VictoriaTraces + VictoriaMetrics, but **one binary instead
of three**.

"Events" aren't a separate thing to configure: in OpenTelemetry an event is a
log record (with an `event.name`), so events are stored and queried through the
logs path — nothing extra to set up.

The one requirement: clients speak **OTLP**, the vendor-neutral standard almost
everything emits. A source that only speaks Prometheus-scrape or Zipkin needs a
translator in front — that's the source's quirk, not otelstore's job.

## Status

Early MVP — usable locally today; not a hardened multi-tenant service.

**Works now**
- OTLP ingest: traces, logs, metrics — over gRPC (`:4317`) and HTTP/protobuf (`:4318`)
- Query: REST (`:4319`) and MCP tools (`:4320`) for agent self-remediation
- Single-file storage (pure-Go SQLite) with optional time-based retention
- Optional bearer-token auth on every endpoint

**Not included (by design)**
- No UI — bring your own Grafana and point it at the query API
- No built-in TLS — front it with a reverse proxy (see [Cloud / TLS](#cloud--tls))
- Single node, local — not clustered or HA

## Quickstart

```sh
# build (CGO not required)
go build -o otelstore ./cmd/otelstore

# run, persisting to a local file
./otelstore -db-path ./telemetry.db
```

Point any OTLP exporter at `localhost:4317` (gRPC) or `localhost:4318` (HTTP),
then query `localhost:4319`.

## Use with Claude Code

otelstore speaks Claude Code's default OTLP output (gRPC, metrics + logs). Run
otelstore, then:

```sh
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_METRICS_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
# if you started otelstore with -auth-token:
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer <token>"
```

Then query what Claude Code reported, e.g. session cost:

```sh
curl "localhost:4319/v1/metrics?name=claude_code.cost.usage"
```

## Ports

| Port  | Purpose                    | Protocol            |
|-------|----------------------------|---------------------|
| 4317  | OTLP ingest                | gRPC                |
| 4318  | OTLP ingest                | HTTP/protobuf       |
| 4319  | Query API                  | HTTP/REST (JSON)    |
| 4320  | Query for agents           | MCP (streamable HTTP)|

## Configuration

| Flag           | Default     | Description                                            |
|----------------|-------------|--------------------------------------------------------|
| `-version`     | —           | Print version and exit                                 |
| `-db-path`     | `:memory:`  | SQLite file path; `:memory:` is ephemeral              |
| `-grpc-port`   | `:4317`     | OTLP gRPC ingest address                               |
| `-ingest-port` | `:4318`     | OTLP HTTP ingest address                               |
| `-query-port`  | `:4319`     | REST query API address                                 |
| `-mcp-addr`    | `:4320`     | MCP query server address                               |
| `-auth-token`  | _(empty)_   | Bearer token; if set, required on all endpoints except `/healthz` and `/readyz`. Also `OTELSTORE_AUTH_TOKEN` |
| `-retention`   | `0`         | Age FIFO: delete data older than this (e.g. `4320h` = 180 days); `0` disables |
| `-max-size`    | `0`         | Size FIFO: evict oldest rows until the DB is under this many bytes; `0` disables |

When `-auth-token` is empty, otelstore is open (intended for localhost). When
set, every ingest and query request must send `Authorization: Bearer <token>`.

**Binding:** by default every port binds `127.0.0.1` (loopback only) — local-only,
and no host-firewall prompt. To accept traffic from other machines, pass an
explicit host, e.g. `-grpc-port 0.0.0.0:4317`, and set `-auth-token` (and front
with a proxy for TLS).

## Querying

REST:

```sh
# spans + correlated logs for a task, run, or trace (exactly one filter)
curl "localhost:4319/v1/query?job_id=JOB123"
curl "localhost:4319/v1/query?run_id=RUN1"
curl "localhost:4319/v1/query?trace_id=<hex>"

# all spans in a trace
curl "localhost:4319/v1/traces/<hex-trace-id>"

# metrics by name
curl "localhost:4319/v1/metrics?name=claude_code.token.usage"

# events/logs by event.name and/or minimum severity
curl "localhost:4319/v1/logs?event_name=claude_code.api_error"
curl "localhost:4319/v1/logs?min_severity=17"          # error and above

# health / readiness (never require a token)
curl "localhost:4319/healthz"
curl "localhost:4319/readyz"
```

Retention has two independent FIFO strategies, usable together: `-retention`
drops data past an age window (e.g. keep 180 days), and `-max-size` evicts the
oldest rows once the database exceeds a byte cap.

MCP (`:4320`) exposes tools `query_job`, `query_run`, and `get_trace` so an agent
can read its own telemetry and self-remediate. See the OpenAPI schema in
`api/openapi.yaml` and deeper notes in [docs/usage.md](docs/usage.md).

## Cloud / TLS

otelstore serves plaintext and is meant to sit behind a reverse proxy that
terminates TLS:

```
OTLP client --TLS--> Traefik --plaintext--> otelstore (:4317/:4318/:4319)
```

Terminate TLS at Traefik (or any proxy), route to otelstore, and set
`-auth-token` so the proxy-forwarded requests still carry a bearer token.

## Build from source

```sh
go build -o otelstore ./cmd/otelstore      # CGO_ENABLED=0 works — static binary
```

The repo is a Go workspace (`go.work`) that also contains `emit/` — small
Go/Rust helper libraries for producing conformant OTLP spans. Cross-platform
release binaries:

```sh
sh scripts/build-release.sh                # writes dist/ for mac/linux/windows
```

## License

MIT. See [LICENSE](LICENSE).
