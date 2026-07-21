# MAESTRO Threat Model

**Project**: otelstore (github.com/3rg0n/otelstore)
**Date**: 2026-07-21
**Framework**: MAESTRO (OWASP MAS + CSA) with ASI Threat Taxonomy
**Taxonomy**: T1-T15 core, T16-T47 extended, BV-1–BV-12 blindspot vectors

## Executive Summary

otelstore is a single-binary local OpenTelemetry store (OTLP traces/logs/metrics
in over gRPC :4317 and HTTP :4318; REST query :4319; MCP query :4320; pure-Go
SQLite). Seven layer agents plus a dependency CVE scanner analyzed it.

**Finding counts (post-dedup):** 1 critical, 3 high, 8 medium, 9 low.

**Most critical:** the **MCP query server on :4320 is not wrapped by the auth
middleware** (`cmd/otelstore/main.go:104`). Even when `-auth-token` is set — which
the README says protects "every ingest and query request" — anyone who can reach
:4320 can call `query_job`/`query_run`/`get_trace` and read **all** stored
telemetry. This is a trust-chain break and the release blocker. It was
independently confirmed by four separate layer agents (L3, L6, L7) and verified
by hand against the source.

**Agentic risk factors present:** Agent-to-Agent communication (the MCP server
serves telemetry to external agent clients) and Agent Identity (no per-client
identity — any client with network reach, and no token, gets everything).

**Validation manifest:** `{agents_run: 8, raw_findings: 44, fabricated_dropped:
12 (L2 run-1, zero tool calls, invented SQL injection against nonexistent
tables/columns), legitimate: 32, deduped_total: 21}`. One agent's output was
discarded as fabricated and re-run; the replacement read the real files and
confirmed the queries are correctly parameterized.

## Scope

- **Languages:** Go 1.26.5 (pure-Go, no CGO) + Rust emit helper (`emit/rust`)
- **AI components:** No foundation model is invoked. otelstore *hosts* an MCP
  server exposing read-only query tools to external agents. L1 (foundation model)
  not applicable; L3 assessed for the MCP tool surface.
- **Entry point:** `cmd/otelstore/main.go` — daemon, 4 listeners
- **Agentic risk factors:** A2A communication, Agent identity (both via MCP)

## Risk Summary

| # | ASI Threat | Layer | Title | Severity | L | I | Risk | Framework |
|---|-----------|-------|-------|----------|---|---|------|-----------|
| 1 | T45,T3,T9 | L3/L6/L7 | MCP server :4320 bypasses auth — all telemetry readable unauthenticated | **Critical** | 3 | 3 | 9 | STRIDE:E / A01 / CWE-306,862 |
| 2 | T4 | L3 | `GetTrace` has no `LIMIT` — unbounded query via unauth MCP | High | 3 | 2 | 6 | STRIDE:D / CWE-400 |
| 3 | T4 | L2/L4 | HTTP ingest `io.ReadAll` with no body-size cap — memory DoS | High | 3 | 2 | 6 | STRIDE:D / CWE-400,770 |
| 4 | T8,T44 | L5 | Auth failures + data access unlogged — no audit trail | High | 3 | 2 | 6 | STRIDE:R / A09 / CWE-778 |
| 5 | T4 | L2/L4 | gRPC server sets no explicit `MaxRecvMsgSize` | Medium | 2 | 2 | 4 | STRIDE:D / CWE-400 |
| 6 | T25,BV-3 | L7 | CI installs gosec/staticcheck/govulncheck via `@latest` (unpinned) | Medium | 2 | 2 | 4 | STRIDE:T / A06 / CWE-829 |
| 7 | T22 | L4 | Auth token via flag/env — visible in process args / child env | Medium | 2 | 2 | 4 | STRIDE:I / CWE-522 |
| 8 | T3,T22 | L4/L6 | No warning when binding non-loopback without auth | Medium | 2 | 2 | 4 | STRIDE:E / A01 |
| 9 | BV-3 | L4 | `release.yml` grants workflow-global `contents:write` | Medium | 2 | 2 | 4 | STRIDE:T / A08 |
| 10 | T1 | L2 | Stored attributes not PII/secret-redacted | Medium | 2 | 2 | 4 | STRIDE:I / CWE-312 |
| 11 | T9 | L7 | MCP startup log omits auth status (obscures #1) | Low | 1 | 2 | 2 | STRIDE:I |
| 12 | T13 | L7 | No SBOM generation in release pipeline | Low | 1 | 2 | 2 | governance |
| 13 | T25 | L7 | No Dependabot/Renovate; CVE detection reactive | Low | 1 | 1 | 1 | governance |
| 14 | T3 | L4 | `-db-path` passed to sql.Open without path validation | Low | 2 | 1 | 2 | CWE-427 |
| 15 | T4 | L4 | No per-connection gRPC rate limit / max concurrent streams | Low | 2 | 1 | 2 | CWE-400 |
| 16 | T22 | L6 | `.gitignore` lacks `*.jks`/`*.jceks` (Java keystores) | Low | 1 | 1 | 1 | CWE-538 |

Deduped CVE scan: **no dependency vulnerabilities** (govulncheck clean).

## Layer Analysis

### Layer 1: Foundation Model
Not applicable — otelstore invokes no LLM/foundation model. It is a telemetry
store that *hosts* an MCP server; there is no model inference, prompt, or
fine-tuning surface.

### Layer 2: Data Operations
The SQL layer is **safe from injection** — `QueryByKey`, `QueryLogs`,
`QueryMetrics`, and `EnforceMaxSize` all use static per-key/per-table query
strings with bound `?` parameters (verified: `store.go` switch statements and the
`WHERE 1=1` + fixed-fragment assembly in `QueryLogs`). A first-pass agent
fabricated `fmt.Sprintf` injection findings against tables/columns that do not
exist; those were discarded. Genuine data-layer risks: no HTTP body-size cap on
ingest (`io.ReadAll`, receiver.go), no explicit gRPC message-size cap, and no
redaction of stored attributes (telemetry can legitimately carry secrets/PII in
attribute values).

### Layer 3: Agent Frameworks (MCP)
The MCP server exposes three **read-only** query tools (no mutate/delete — good).
Two real issues: (1) **the MCP endpoint is unauthenticated** even when a token is
configured (finding #1, critical); (2) `get_trace` → `store.GetTrace` runs an
unbounded `SELECT ... WHERE trace_id = ?` with no `LIMIT`, so a single call can
pull an arbitrarily large result set into memory — reachable precisely because
the endpoint has no auth. Tools share one store with no per-client scoping (any
client can query any `job_id`/`run_id`), acceptable for a local single-tenant
tool but a boundary to document.

### Layer 4: Deployment Infrastructure
No container/k8s manifests in-repo. Loopback-default binding is good. Gaps:
unbounded ingest reads (DoS), token exposed via process args/env, no operator
warning when binding to a non-loopback address without a token, `release.yml`
uses workflow-global `contents:write`, and the retention sweeper lacks a floor/
backoff that could busy-loop under a tiny `-max-size`.

### Layer 5: Evaluation & Observability
Weakest layer. Auth failures (HTTP 401 and gRPC `Unauthenticated`) are returned
**silently** — no log line — so credential-guessing or unauthorized access leaves
no trace. There is no per-request logging (no source, filter, result count) and
no audit trail of data access or retention deletes. Tokens are not currently
logged (safe), but the pattern is fragile.

### Layer 6: Security & Compliance
Auth primitive itself is sound: exact `Bearer ` prefix + `crypto/subtle`
constant-time compare, shared by HTTP middleware and gRPC interceptor, with
`/healthz`/`/readyz` bypass via exact-path allowlist. The failure is **coverage**
— the MCP transport is not behind it. **VCS hygiene: good.** `.gitignore` covers
`.env*`, `*.pem/key/p12/pfx`, `credentials.json`, `*_secret*`, `*.token`, DBs,
build artifacts; no secret-shaped or artifact path appears in `git ls-files`.
Minor: add `*.jks`/`*.jceks` for defense-in-depth.

### Layer 7: Agent Ecosystem
No outbound calls from the server, no A2A beyond serving MCP clients; deps are
well-known official orgs pinned via `go.sum`/`Cargo.lock`. Governance gaps: CI
fetches its **security scanners** via `@latest` (unpinned — a supply-chain hole
in the very tools meant to catch supply-chain issues), no SBOM, no Dependabot.

## Agent/Skill Integrity
No agent/skill definitions found in the repository — integrity audit not
applicable.

## Dependency CVEs
None. `govulncheck ./...` reported no vulnerabilities across the Go module; the
Rust `emit` crate's `Cargo.lock` pins are clean. *Scanned with: govulncheck.*

## Recommended Mitigations (priority order)

1. **[Critical] Authenticate the MCP server.** Wrap `mcpHandler` with
   `auth.Middleware(*authToken)` exactly like ingest/query in `main.go`. Until
   fixed, `-auth-token` gives a false sense of protection.
2. **[High] Bound `GetTrace`.** Add a `LIMIT` (cap 10000, matching `QueryByKey`).
3. **[High] Cap ingest size.** Wrap request bodies in `http.MaxBytesReader` and
   set `grpc.MaxRecvMsgSize`.
4. **[High] Log auth failures + data access** with source, path/method, and
   reason (never the token).
5. **[Medium] Pin CI scanner versions** (gosec/staticcheck/govulncheck) instead
   of `@latest`.
6. **[Medium] Warn on non-loopback bind without a token**; document token-in-env
   exposure and offer a token-file option.
7. **[Medium] Scope `release.yml` `contents:write`** to the release job only.
8. **[Low] Governance:** SBOM in release, Dependabot config, `*.jks` in
   `.gitignore`, MCP startup auth-status log, `-db-path` validation.

## Trust Boundaries

```
                    ┌─────────────────────── otelstore process ───────────────────────┐
  OTLP clients ─────┤ :4317 gRPC ingest   [auth: interceptor]                          │
  (apps, agents,    │ :4318 HTTP ingest   [auth: middleware when -auth-token]          │
   Claude Code)     │ :4319 REST query    [auth: middleware when -auth-token]          │
                    │ :4320 MCP query     [NO AUTH ★ finding #1] ◄── trust break       │
  query clients ────┤                                                                   │
  (Grafana, agent)  │   all four ── SQLite file (attributes = generic JSON, unredacted) │
                    └───────────────────────────────────────────────────────────────────┘
  TLS: terminated by an external reverse proxy (Traefik); otelstore speaks plaintext.
```

The intended boundary is "network → authenticated ingest/query → store." The MCP
port punches a hole straight to the store, bypassing the boundary.

## Data Flow Diagram (text)

```
emitter --OTLP(gRPC/HTTP)--> [auth?] --> receiver --> store(SQLite)
                                                          │
agent/Grafana --REST/MCP query--> [auth? MCP=NO] --------┘
                                                          │
retention sweeper (age + size FIFO) --DELETE oldest------┘
```
