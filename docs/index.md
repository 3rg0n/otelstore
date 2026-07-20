---
title: otelstore
description: One binary. The whole local OpenTelemetry stack.
---

# otelstore

**One binary. The whole local OpenTelemetry stack.**

Stop wiring up an OTel Collector *and* Loki *and* Tempo *and* Prometheus every
time you need somewhere to send telemetry. Grab one dependency-free binary, run
it, and point OTLP at it. It ingests **traces, logs, and metrics** into a single
local file and lets you query them back over REST or MCP.

Pure Go, no CGO — a single static executable for macOS, Linux, and Windows.

## Why

- **Zero dependencies.** No external services, no database to provision — just
  `./otelstore`.
- **All three signals.** Traces, logs, and metrics over OTLP gRPC (`:4317`) and
  HTTP (`:4318`).
- **Query built in.** REST (`:4319`) and MCP (`:4320`) — the latter lets an
  agent read its own telemetry and self-remediate.
- **Cross-platform.** One static binary per OS; native Windows included.

## Quickstart

```sh
go build -o otelstore ./cmd/otelstore
./otelstore -db-path ./telemetry.db
```

Point any OTLP exporter at `localhost:4317` (gRPC) or `localhost:4318` (HTTP),
then query `localhost:4319`.

## Works with Claude Code

```sh
export CLAUDE_CODE_ENABLE_TELEMETRY=1
export OTEL_METRICS_EXPORTER=otlp
export OTEL_LOGS_EXPORTER=otlp
export OTEL_EXPORTER_OTLP_PROTOCOL=grpc
export OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
```

```sh
curl "localhost:4319/v1/metrics?name=claude_code.cost.usage"
```

## Learn more

- [README](https://github.com/3rg0n/otelstore#readme) — full docs
- [Usage reference](usage.md) — OTLP endpoint matrix, query API, MCP tools
- [Contributing](https://github.com/3rg0n/otelstore/blob/main/CONTRIBUTING.md)

MIT licensed.
