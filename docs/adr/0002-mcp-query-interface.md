# 0002. MCP query interface for agent self-remediation

- Status: accepted
- Date: 2026-07-17

## Context

The MVP (ADR 0001) exposes a REST query API. The product goal, however, is that
a broken agent in an orchestrated DAG reads its own telemetry and self-remediates.
Those agents speak the Model Context Protocol (MCP), not ad-hoc REST. A raw REST
surface makes the model compose HTTP calls and parse generic JSON; an MCP tool
surface gives it named, schema-typed tools that map directly to the question it
asks: "why did job X fail?"

The official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`) is at a stable
v1.6.1 (v1.7.0 is pre-release only). The query logic already lives in the store
(`QueryByKey`, `GetTrace`), so an MCP server is a thin adapter, not new query
logic.

## Decision

- **Add an MCP server as a thin layer over the existing store queries** — no new
  query logic, no duplication of the REST validation. Use the official SDK at
  v1.6.1.
- **Transport: streamable HTTP, hosted in the `otelstore` daemon** (the process
  that owns the SQLite store and receives OTLP), on its own port. A separate
  stdio binary would contend for the single-writer SQLite file; co-hosting avoids
  that. stdio transport can be added later for local-only clients.
- **Tools (agent-facing, task-shaped):**
  - `query_job(job_id)` → spans + logs for the task, PLUS an explicit `errors`
    array (error-status spans with messages) — the remediation context a broken
    agent needs, surfaced rather than left for the model to derive.
  - `query_run(run_id)` → spans + logs for the whole workflow run.
  - `get_trace(trace_id)` → the span tree for a trace.
- Tools return **structured output** (typed Out), so the model gets typed JSON,
  not a text blob to re-parse.

## Consequences

- Easier: closes the self-remediation loop with the consumer's native protocol;
  reuses store queries verbatim; the daemon stays single-process over one DB.
- Harder / deferred: the MCP tool schema is another emit-adjacent contract to
  keep aligned with the store's field names; stdio transport and auth on the MCP
  endpoint are not yet addressed. The SDK is v1.x but young — pin the version and
  isolate its API behind our handler package so an SDK bump is a one-file change.
- The REST API (ADR 0001) remains for non-agent consumers (Grafana connector,
  scripts); MCP and REST are two front-ends over the same store.
