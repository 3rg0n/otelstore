# Overlay Schema — emit-side telemetry contract

Status: draft · Date: 2026-07-14 · Owner: observability component (this repo)

## What this is

The **emit-side contract** every agent/emitter follows so that the generic
telemetry store can be queried meaningfully by the self-healing loop. The store
is deliberately dumb: it ingests OTLP spans + attribute maps generically and
**never reads this file**. All structure lives here, on emit. If emitters don't
follow this, the store fills with mush and the healer's queries return partial
trees — so conforming *is* the point.

Design rule: **align, don't fuse.** The OpenTelemetry GenAI semantic conventions
are at *Development* stability (breaking changes expected), so they are treated
as **decoration**, never as the schema or the correlation key. Our own stable
keys are the spine; `gen_ai.*` rides along for ecosystem interop.

## Conformance kit (this contract has three parts)

A markdown doc alone drifts. The contract is enforced by all three:

1. **Describe** — this document (the reference: spans, attributes, rules).
2. **Encode** — per-language emitter helpers (`emit/go`, `emit/rust`). The
   *only* sanctioned way to start these spans; they set required attributes so
   an emitter cannot forget or misname them. Emitters call helpers, never set
   span names / `gen_ai.*` attributes by hand.
3. **Enforce** — a CI conformance test that emits from each helper and asserts
   the shape (required attrs present, span names in the allowed set, error →
   span status). Catches drift even if someone bypasses a helper.

## Correlation spine (owned keys — never subject to semconv churn)

These are **ours**, stable, and are what the healer queries on. They match the
already-shipped `job_id` convention (Piece 1) — flat snake_case, not dotted.

| Key           | Grain                                    | Required on                          |
|---------------|------------------------------------------|--------------------------------------|
| `run_id`      | one DAG execution (a workflow run)       | **every** span, log, event           |
| `job_id`      | one single-verb task (one Valkey job)    | every **task-level** span (see below)|
| `workflow_name` | the workflow definition (workflow.yaml) | the workflow-run span                |
| `agent_id`    | the executing agent                      | agent/task spans                     |

- `run_id` is the **universal spine** — present on everything, including the
  workflow-root span (which is not itself a job).
- `job_id` is the **primary lookup key** for remediation — present on every
  task-level span. Absent only on the workflow-root span.
- The healer keys on `job_id` (task granularity) and walks up to `run_id` for
  the full run tree. Neither key is a `gen_ai.*` attribute, by design.

## Grain mapping (the deliberate decision)

`gen_ai.conversation.id` = the **workflow run** (`run_id`). Rationale: a
"conversation" is the interaction thread; one DAG execution *is* that thread;
individual jobs are turns within it. Do **not** alias `conversation.id` to
`job_id`. Pinned here so anvil/blacksmith map it identically.

| Concept            | Owned key (stable) | GenAI decoration (Development)          |
|--------------------|--------------------|-----------------------------------------|
| Workflow definition| `workflow_name`    | `gen_ai.workflow.name`                  |
| Workflow run       | `run_id`           | `gen_ai.conversation.id` = `run_id`     |
| Task (single verb) | `job_id`           | — (identified by span kind + `agent_id`)|
| Agent              | `agent_id`         | `gen_ai.agent.id`, `gen_ai.agent.name`  |

## Span catalog

Maps our orchestrator concepts onto GenAI span operations. `operation.name` and
`gen_ai.*` are decoration; the owned columns are the contract.

**Span name == `gen_ai.operation.name`, exactly, for every span.** This is a hard
rule: the OTel span name and the `gen_ai.operation.name` attribute MUST be the
same string, and MUST be byte-identical across every language helper (Go, Rust,
…). This is the single most drift-prone point — two helpers naming the "same"
span differently silently splits the healer's queries — so it is pinned here and
asserted by the conformance test in every language.

| Overlay span      | span name / `gen_ai.operation.name` | Span kind      | Required owned attrs           | GenAI decoration (should-emit)                          |
|-------------------|-------------------------------------|----------------|--------------------------------|---------------------------------------------------------|
| workflow run      | `invoke_workflow`                   | INTERNAL       | `run_id`, `workflow_name`      | `gen_ai.workflow.name`, `gen_ai.conversation.id`        |
| agent invocation  | `invoke_agent`                      | INTERNAL / CLIENT¹ | `run_id`, `job_id`, `agent_id` | `gen_ai.agent.id`, `gen_ai.agent.name`, `.version`  |
| planning          | `plan`                              | INTERNAL       | `run_id`, `job_id`             | —                                                       |
| tool / verb exec  | `execute_tool`                      | INTERNAL       | `run_id`, `job_id`, `tool_name`| `gen_ai.tool.name`, `gen_ai.tool.call.id`               |
| model call        | `chat`³                             | CLIENT         | `run_id`, `job_id`             | `gen_ai.provider.name`, `gen_ai.request.model`, tokens² |

¹ CLIENT when the agent is invoked over a remote service, else INTERNAL.
² token-usage metrics per GenAI metrics conventions (`gen_ai.usage.*`).
³ Model spans: use `chat` as the operation/span name (the GenAI conventions'
  default inference operation). NOT `model` or `model_call` — those are not GenAI
  operation values and diverged between the first Go/Rust drafts. If a helper
  needs a non-chat model operation later (e.g. `embeddings`, `generate_content`),
  add it here first, then to every language helper — never ad hoc in one language.

## Required vs optional

- **Required (healer depends on these — conformance test asserts):**
  - `run_id` on every span.
  - `job_id` on every task-level span (`invoke_agent`, `plan`, `execute_tool`,
    model call).
  - `gen_ai.operation.name` on every span, from the allowed set above.
  - On failure: span status = ERROR + recorded exception (so "why did it fail"
    is answerable from the span alone).
- **Should (interop / richer queries, non-fatal if absent):**
  - `agent_id` / `gen_ai.agent.*`, `tool_name` / `gen_ai.tool.*`,
    `gen_ai.provider.name`, `gen_ai.request.model`, token-usage metrics.

## Semconv version pin

Target: **OpenTelemetry GenAI semantic conventions — *Development* stability**,
pinned to a fixed snapshot so "align to it" has a fixed referent.

- Repo: `open-telemetry/semantic-conventions-genai`
- Pinned commit: `<PIN EXACT SHA>` (as of 2026-07-14)
- Covers: agent spans (`create_agent`, `invoke_agent`, `invoke_workflow`,
  `plan`), tool/model spans, MCP conventions, token-usage metrics, and
  provider conventions (Anthropic, Bedrock, OpenAI, Azure AI).

## Churn policy (why the seam holds)

When the GenAI conventions change (they will — Development stability):

1. Bump the pinned commit above.
2. Update the `gen_ai.*` decoration mapping in **one place** — the emitter
   helper — and nowhere else.
3. Re-run the conformance test.

The store, the query API, and the healer's correlation keys (`job_id`,
`run_id`) are **unaffected**, because none of them depend on `gen_ai.*` names.
That isolation is the entire reason the shape lives in the helper, not the store.

## Open items

- Pin the exact GenAI semconv commit SHA (placeholder above).
- Confirm `conversation.id` grain (run-level) with orchestrator owner if it
  disagrees.
- Audit-log durability is **backlogged** (telemetry rides best-effort OTLP);
  the `run_id`/`job_id`-on-everything rule keeps a future durable audit store
  join-able.
