# Usage

Deeper reference for pointing OTLP sources at otelstore and querying it back.
For the overview, see the top-level [README](../README.md).

## OTLP endpoint matrix

otelstore accepts OTLP over two transports simultaneously. Which one a client
uses depends on `OTEL_EXPORTER_OTLP_PROTOCOL`.

| Protocol           | Address                  | Notes                                        |
|--------------------|--------------------------|----------------------------------------------|
| `grpc`             | `http://localhost:4317`  | Bare host:port, no path. OTel default.       |
| `http/protobuf`    | `http://localhost:4318`  | Paths appended automatically (see below).    |

HTTP ingest paths (protocol `http/protobuf`):

| Signal  | Path          |
|---------|---------------|
| Traces  | `/v1/traces`  |
| Logs    | `/v1/logs`    |
| Metrics | `/v1/metrics` |

## Pointing a generic OTLP source at otelstore

Standard OpenTelemetry SDK environment variables apply:

```sh
# gRPC (default)
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317

# or HTTP/protobuf
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318

# auth, if otelstore was started with -auth-token
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer <token>"
```

Per-signal overrides (`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`, `..._LOGS_...`,
`..._METRICS_...`) work as usual and can point different signals here.

## Correlation keys

otelstore promotes two flat attributes to indexed columns for fast lookup:
`run_id` and `job_id`. Anything else — including OpenTelemetry GenAI
(`gen_ai.*`) attributes — is stored generically as JSON and returned in the
`attributes` object. Emit `run_id`/`job_id` on your spans/logs/metrics to make
the correlated query (`/v1/query?job_id=...`) most useful. See
`docs/overlay-schema.md` for the emit-side contract and the `emit/` helper
libraries.

## Query API

### `GET /v1/query`

Correlated spans + logs for exactly one key. Supplying zero or more than one of
the three filters returns `400`.

| Param      | Meaning                          |
|------------|----------------------------------|
| `job_id`   | single task                      |
| `run_id`   | whole run                        |
| `trace_id` | one trace                        |
| `limit`    | max rows (default 1000, max 10000) |

Response: `{ "spans": [...], "logs": [...] }`.

### `GET /v1/traces/{trace_id}`

All spans for a trace, ordered by start time:
`{ "trace_id": "...", "spans": [...] }`.

### `GET /v1/metrics?name=<name>`

Metric data points by name, ordered by time:
`{ "metrics": [ { "name", "value_double", "time_ns", "run_id", "job_id", "attributes" } ] }`.

## MCP tools (`:4320`)

otelstore exposes the query surface as Model Context Protocol tools over
streamable HTTP, so an agent can inspect its own telemetry:

| Tool         | Input       | Returns                                  |
|--------------|-------------|------------------------------------------|
| `query_job`  | `job_id`    | spans, logs, and an `errors` array       |
| `query_run`  | `run_id`    | spans, logs                              |
| `get_trace`  | `trace_id`  | spans                                    |

`query_job` surfaces error-status spans in a dedicated `errors` array so a
failing agent gets its failure context in one call.

## Retention

By default otelstore keeps everything. Set `-retention` to a duration (e.g.
`-retention 168h`) and a background sweeper periodically deletes spans, logs,
and metrics older than that window. Suitable for a long-running local daemon so
the SQLite file stays bounded.

## Metrics support notes

Gauge and Sum metric types are ingested (each data point stored with its
double/int value). Histogram data points are currently skipped rather than
errored — a client sending histograms will not fail, but those points are not
stored yet.
