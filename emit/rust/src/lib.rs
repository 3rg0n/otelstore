//! OpenTelemetry emitter helper crate encoding the emit-side telemetry contract.
//!
//! This crate provides type-safe helpers for emitting spans conforming to the
//! overlay schema defined in `docs/overlay-schema.md`. Each helper sets required
//! owned attributes automatically and ensures the correct span name / gen_ai.operation.name,
//! making it the *only* easy way to emit properly-shaped spans.

use opentelemetry::trace::{Span, Status, Tracer};
use opentelemetry_sdk::trace::Tracer as SdkTracer;

// ============================================================================
// Attribute Key Constants — the contract keys (owned, stable)
// ============================================================================

/// Workflow run identifier (universal spine — present on every span).
pub const ATTR_RUN_ID: &str = "run_id";

/// Single-verb task identifier (present on task-level spans only).
pub const ATTR_JOB_ID: &str = "job_id";

/// Workflow definition name (present on workflow-run span only).
pub const ATTR_WORKFLOW_NAME: &str = "workflow_name";

/// Executing agent identifier (present on agent/task spans).
pub const ATTR_AGENT_ID: &str = "agent_id";

/// Tool name (present on tool/verb execution spans).
pub const ATTR_TOOL_NAME: &str = "tool_name";

// ============================================================================
// GenAI Semantic Convention Keys — centralized for semconv churn
// ============================================================================

/// GenAI operation name (decoration; values from overlay spec).
const ATTR_GEN_AI_OPERATION_NAME: &str = "gen_ai.operation.name";

/// GenAI workflow name (decoration).
const ATTR_GEN_AI_WORKFLOW_NAME: &str = "gen_ai.workflow.name";

/// GenAI conversation ID (decoration; grain = run_id).
const ATTR_GEN_AI_CONVERSATION_ID: &str = "gen_ai.conversation.id";

/// GenAI agent ID (decoration).
const ATTR_GEN_AI_AGENT_ID: &str = "gen_ai.agent.id";

/// GenAI agent name (decoration).
#[allow(dead_code)]
const ATTR_GEN_AI_AGENT_NAME: &str = "gen_ai.agent.name";

/// GenAI tool name (decoration).
const ATTR_GEN_AI_TOOL_NAME: &str = "gen_ai.tool.name";

/// GenAI provider name (decoration).
const ATTR_GEN_AI_PROVIDER_NAME: &str = "gen_ai.provider.name";

/// GenAI request model (decoration).
const ATTR_GEN_AI_REQUEST_MODEL: &str = "gen_ai.request.model";

/// Tracer name constant.
pub const TRACER_NAME: &str = "emit";

// ============================================================================
// Operation name constants (align with overlay spec and Go helper byte-for-byte)
// ============================================================================

const OP_INVOKE_WORKFLOW: &str = "invoke_workflow";
const OP_INVOKE_AGENT: &str = "invoke_agent";
const OP_PLAN: &str = "plan";
const OP_EXECUTE_TOOL: &str = "execute_tool";
const OP_CHAT: &str = "chat";

// ============================================================================
// Span Starters — one helper per overlay span type
// ============================================================================

/// Start a workflow-run span (operation.name = invoke_workflow).
///
/// Sets required attributes: `run_id`, `workflow_name`.
/// Decorates with `gen_ai.workflow.name`, `gen_ai.conversation.id`.
///
/// # Arguments
/// * `tracer` - The OpenTelemetry tracer
/// * `run_id` - Workflow run identifier (universal spine)
/// * `workflow_name` - Workflow definition name
///
/// # Returns
/// An active span with required attributes set.
pub fn start_workflow_span(
    tracer: &SdkTracer,
    run_id: &str,
    workflow_name: &str,
) -> opentelemetry_sdk::trace::Span {
    let mut span = tracer.start(OP_INVOKE_WORKFLOW);

    span.set_attribute(opentelemetry::KeyValue::new(ATTR_RUN_ID, run_id.to_string()));
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_WORKFLOW_NAME,
        workflow_name.to_string(),
    ));

    // Decoration: gen_ai.* attributes
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_OPERATION_NAME,
        OP_INVOKE_WORKFLOW,
    ));
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_WORKFLOW_NAME,
        workflow_name.to_string(),
    ));
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_CONVERSATION_ID,
        run_id.to_string(),
    ));

    span
}

/// Start an agent-invocation span (operation.name = invoke_agent).
///
/// Sets required attributes: `run_id`, `job_id`, `agent_id`.
/// Decorates with `gen_ai.agent.id`, `gen_ai.agent.name`.
///
/// # Arguments
/// * `tracer` - The OpenTelemetry tracer
/// * `run_id` - Workflow run identifier (universal spine)
/// * `job_id` - Task identifier for this agent invocation
/// * `agent_id` - Agent identifier
///
/// # Returns
/// An active span with required attributes set.
pub fn start_agent_span(
    tracer: &SdkTracer,
    run_id: &str,
    job_id: &str,
    agent_id: &str,
) -> opentelemetry_sdk::trace::Span {
    let mut span = tracer.start(OP_INVOKE_AGENT);

    span.set_attribute(opentelemetry::KeyValue::new(ATTR_RUN_ID, run_id.to_string()));
    span.set_attribute(opentelemetry::KeyValue::new(ATTR_JOB_ID, job_id.to_string()));
    span.set_attribute(opentelemetry::KeyValue::new(ATTR_AGENT_ID, agent_id.to_string()));

    // Decoration: gen_ai.* attributes
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_OPERATION_NAME,
        OP_INVOKE_AGENT,
    ));
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_AGENT_ID,
        agent_id.to_string(),
    ));

    span
}

/// Start a planning span (operation.name = plan).
///
/// Sets required attributes: `run_id`, `job_id`.
/// Decorates with `gen_ai.operation.name = plan`.
///
/// # Arguments
/// * `tracer` - The OpenTelemetry tracer
/// * `run_id` - Workflow run identifier (universal spine)
/// * `job_id` - Task identifier for this planning task
///
/// # Returns
/// An active span with required attributes set.
pub fn start_plan_span(
    tracer: &SdkTracer,
    run_id: &str,
    job_id: &str,
) -> opentelemetry_sdk::trace::Span {
    let mut span = tracer.start(OP_PLAN);

    span.set_attribute(opentelemetry::KeyValue::new(ATTR_RUN_ID, run_id.to_string()));
    span.set_attribute(opentelemetry::KeyValue::new(ATTR_JOB_ID, job_id.to_string()));

    // Decoration: gen_ai.* attributes
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_OPERATION_NAME,
        OP_PLAN,
    ));

    span
}

/// Start a tool/verb execution span (operation.name = execute_tool).
///
/// Sets required attributes: `run_id`, `job_id`, `tool_name`.
/// Decorates with `gen_ai.tool.name`.
///
/// # Arguments
/// * `tracer` - The OpenTelemetry tracer
/// * `run_id` - Workflow run identifier (universal spine)
/// * `job_id` - Task identifier for this tool execution
/// * `tool_name` - Name of the tool being executed
///
/// # Returns
/// An active span with required attributes set.
pub fn start_tool_span(
    tracer: &SdkTracer,
    run_id: &str,
    job_id: &str,
    tool_name: &str,
) -> opentelemetry_sdk::trace::Span {
    let mut span = tracer.start(OP_EXECUTE_TOOL);

    span.set_attribute(opentelemetry::KeyValue::new(ATTR_RUN_ID, run_id.to_string()));
    span.set_attribute(opentelemetry::KeyValue::new(ATTR_JOB_ID, job_id.to_string()));
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_TOOL_NAME,
        tool_name.to_string(),
    ));

    // Decoration: gen_ai.* attributes
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_OPERATION_NAME,
        OP_EXECUTE_TOOL,
    ));
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_TOOL_NAME,
        tool_name.to_string(),
    ));

    span
}

/// Start a model-call span (operation.name = chat).
///
/// Sets required attributes: `run_id`, `job_id`.
/// Decorates with `gen_ai.provider.name`, `gen_ai.request.model` when Some.
///
/// # Arguments
/// * `tracer` - The OpenTelemetry tracer
/// * `run_id` - Workflow run identifier (universal spine)
/// * `job_id` - Task identifier for this model call
/// * `provider` - Optional provider name (e.g., "anthropic", "bedrock")
/// * `model` - Optional model identifier (e.g., "claude-sonnet-5")
///
/// # Returns
/// An active span with required attributes set.
pub fn start_model_span(
    tracer: &SdkTracer,
    run_id: &str,
    job_id: &str,
    provider: Option<&str>,
    model: Option<&str>,
) -> opentelemetry_sdk::trace::Span {
    let mut span = tracer.start(OP_CHAT);

    span.set_attribute(opentelemetry::KeyValue::new(ATTR_RUN_ID, run_id.to_string()));
    span.set_attribute(opentelemetry::KeyValue::new(ATTR_JOB_ID, job_id.to_string()));

    // Decoration: gen_ai.* attributes
    span.set_attribute(opentelemetry::KeyValue::new(
        ATTR_GEN_AI_OPERATION_NAME,
        OP_CHAT,
    ));

    if let Some(prov) = provider {
        span.set_attribute(opentelemetry::KeyValue::new(
            ATTR_GEN_AI_PROVIDER_NAME,
            prov.to_string(),
        ));
    }

    if let Some(mdl) = model {
        span.set_attribute(opentelemetry::KeyValue::new(
            ATTR_GEN_AI_REQUEST_MODEL,
            mdl.to_string(),
        ));
    }

    span
}

/// Record an error on a span.
///
/// Sets span status to ERROR and records the exception event.
/// Required by overlay spec: failed spans must carry ERROR status + recorded exception.
///
/// # Arguments
/// * `span` - The span to record the error on
/// * `error` - Error message/description
pub fn record_error(span: &mut opentelemetry_sdk::trace::Span, error: &str) {
    let error_msg = error.to_string();
    span.set_status(Status::Error {
        description: error_msg.clone().into(),
    });
    span.add_event("exception", vec![opentelemetry::KeyValue::new("exception.message", error_msg)]);
}

#[cfg(test)]
mod tests {
    use super::*;
    use opentelemetry::trace::TracerProvider;
    use opentelemetry_sdk::trace::{
        InMemorySpanExporter, SdkTracerProvider, SimpleSpanProcessor, SpanData,
    };

    // ========================================================================
    // Conformance harness — real in-memory exporter so finished spans can be
    // inspected. SimpleSpanProcessor exports synchronously on span end.
    // ========================================================================

    /// Build a provider wired to an in-memory exporter, plus the tracer.
    fn setup() -> (SdkTracerProvider, SdkTracer, InMemorySpanExporter) {
        let exporter = InMemorySpanExporter::default();
        let provider = SdkTracerProvider::builder()
            .with_span_processor(SimpleSpanProcessor::new(exporter.clone()))
            .build();
        let tracer = provider.tracer(TRACER_NAME);
        (provider, tracer, exporter)
    }

    /// The string value of `key` on `span`, or None if the attribute is absent.
    fn attr_string(span: &SpanData, key: &str) -> Option<String> {
        span.attributes
            .iter()
            .find(|kv| kv.key.as_str() == key)
            .map(|kv| kv.value.as_str().to_string())
    }

    /// Whether `span` carries an attribute with the given key at all.
    fn has_attr(span: &SpanData, key: &str) -> bool {
        span.attributes.iter().any(|kv| kv.key.as_str() == key)
    }

    /// End the span, flush, and return the single exported SpanData.
    fn export_one(
        provider: &SdkTracerProvider,
        exporter: &InMemorySpanExporter,
        mut span: opentelemetry_sdk::trace::Span,
    ) -> SpanData {
        span.end();
        let _ = provider.force_flush();
        let spans = exporter.get_finished_spans().expect("get finished spans");
        assert_eq!(spans.len(), 1, "expected exactly one exported span");
        spans.into_iter().next().unwrap()
    }

    /// Every span must have span name == gen_ai.operation.name, byte-for-byte.
    fn assert_name_matches_operation(span: &SpanData) {
        let op = attr_string(span, ATTR_GEN_AI_OPERATION_NAME)
            .expect("gen_ai.operation.name must be set");
        assert_eq!(
            span.name.as_ref(),
            op.as_str(),
            "span name must equal gen_ai.operation.name"
        );
    }

    #[test]
    fn test_workflow_span_conformance() {
        let (provider, tracer, exporter) = setup();
        let span = start_workflow_span(&tracer, "run-123", "my-workflow");
        let s = export_one(&provider, &exporter, span);

        assert_eq!(s.name.as_ref(), OP_INVOKE_WORKFLOW);
        assert_eq!(
            attr_string(&s, ATTR_GEN_AI_OPERATION_NAME).as_deref(),
            Some(OP_INVOKE_WORKFLOW)
        );
        assert_name_matches_operation(&s);
        assert_eq!(attr_string(&s, ATTR_RUN_ID).as_deref(), Some("run-123"));
        assert_eq!(
            attr_string(&s, ATTR_WORKFLOW_NAME).as_deref(),
            Some("my-workflow")
        );
        // Grain: conversation.id == run_id.
        assert_eq!(
            attr_string(&s, ATTR_GEN_AI_CONVERSATION_ID).as_deref(),
            Some("run-123")
        );
        // Mandatory negative assertion: job_id absent on the workflow span.
        assert!(
            !has_attr(&s, ATTR_JOB_ID),
            "job_id must be ABSENT on the workflow span"
        );
    }

    #[test]
    fn test_agent_span_conformance() {
        let (provider, tracer, exporter) = setup();
        let span = start_agent_span(&tracer, "run-456", "job-789", "agent-001");
        let s = export_one(&provider, &exporter, span);

        assert_eq!(s.name.as_ref(), OP_INVOKE_AGENT);
        assert_name_matches_operation(&s);
        assert_eq!(attr_string(&s, ATTR_RUN_ID).as_deref(), Some("run-456"));
        assert_eq!(attr_string(&s, ATTR_JOB_ID).as_deref(), Some("job-789"));
        assert_eq!(attr_string(&s, ATTR_AGENT_ID).as_deref(), Some("agent-001"));
        assert_eq!(
            attr_string(&s, ATTR_GEN_AI_AGENT_ID).as_deref(),
            Some("agent-001")
        );
    }

    #[test]
    fn test_plan_span_conformance() {
        let (provider, tracer, exporter) = setup();
        let span = start_plan_span(&tracer, "run-111", "job-222");
        let s = export_one(&provider, &exporter, span);

        assert_eq!(s.name.as_ref(), OP_PLAN);
        assert_name_matches_operation(&s);
        assert_eq!(attr_string(&s, ATTR_RUN_ID).as_deref(), Some("run-111"));
        assert_eq!(attr_string(&s, ATTR_JOB_ID).as_deref(), Some("job-222"));
    }

    #[test]
    fn test_tool_span_conformance() {
        let (provider, tracer, exporter) = setup();
        let span = start_tool_span(&tracer, "run-333", "job-444", "exec-bash");
        let s = export_one(&provider, &exporter, span);

        assert_eq!(s.name.as_ref(), OP_EXECUTE_TOOL);
        assert_name_matches_operation(&s);
        assert_eq!(attr_string(&s, ATTR_RUN_ID).as_deref(), Some("run-333"));
        assert_eq!(attr_string(&s, ATTR_JOB_ID).as_deref(), Some("job-444"));
        assert_eq!(attr_string(&s, ATTR_TOOL_NAME).as_deref(), Some("exec-bash"));
        assert_eq!(
            attr_string(&s, ATTR_GEN_AI_TOOL_NAME).as_deref(),
            Some("exec-bash")
        );
    }

    #[test]
    fn test_model_span_with_provider_and_model() {
        let (provider, tracer, exporter) = setup();
        let span = start_model_span(
            &tracer,
            "run-555",
            "job-666",
            Some("anthropic"),
            Some("claude-sonnet-5"),
        );
        let s = export_one(&provider, &exporter, span);

        // Model op is pinned to "chat" and must match the span name.
        assert_eq!(s.name.as_ref(), OP_CHAT);
        assert_eq!(
            attr_string(&s, ATTR_GEN_AI_OPERATION_NAME).as_deref(),
            Some(OP_CHAT)
        );
        assert_name_matches_operation(&s);
        assert_eq!(attr_string(&s, ATTR_RUN_ID).as_deref(), Some("run-555"));
        assert_eq!(attr_string(&s, ATTR_JOB_ID).as_deref(), Some("job-666"));
        assert_eq!(
            attr_string(&s, ATTR_GEN_AI_PROVIDER_NAME).as_deref(),
            Some("anthropic")
        );
        assert_eq!(
            attr_string(&s, ATTR_GEN_AI_REQUEST_MODEL).as_deref(),
            Some("claude-sonnet-5")
        );
    }

    #[test]
    fn test_model_span_without_optional_attributes() {
        let (provider, tracer, exporter) = setup();
        let span = start_model_span(&tracer, "run-777", "job-888", None, None);
        let s = export_one(&provider, &exporter, span);

        assert_eq!(s.name.as_ref(), OP_CHAT);
        assert_eq!(attr_string(&s, ATTR_RUN_ID).as_deref(), Some("run-777"));
        assert_eq!(attr_string(&s, ATTR_JOB_ID).as_deref(), Some("job-888"));
        // Optional decoration must be ABSENT when not supplied.
        assert!(
            !has_attr(&s, ATTR_GEN_AI_PROVIDER_NAME),
            "provider must be absent when None"
        );
        assert!(
            !has_attr(&s, ATTR_GEN_AI_REQUEST_MODEL),
            "model must be absent when None"
        );
    }

    #[test]
    fn test_record_error() {
        let (provider, tracer, exporter) = setup();
        let mut span = start_agent_span(&tracer, "run-999", "job-000", "agent-err");
        record_error(&mut span, "connection timeout");
        let s = export_one(&provider, &exporter, span);

        match &s.status {
            Status::Error { description } => {
                assert_eq!(description.as_ref(), "connection timeout")
            }
            other => panic!("expected Status::Error, got {other:?}"),
        }
        let has_exception = s.events.iter().any(|e| e.name.as_ref() == "exception");
        assert!(has_exception, "expected an 'exception' event to be recorded");
    }

    #[test]
    fn test_run_id_present_on_every_span() {
        // run_id is the universal spine — assert on all five span types.
        let (provider, tracer, exporter) = setup();
        let spans = [
            start_workflow_span(&tracer, "spine", "wf"),
            start_agent_span(&tracer, "spine", "j", "a"),
            start_plan_span(&tracer, "spine", "j"),
            start_tool_span(&tracer, "spine", "j", "t"),
            start_model_span(&tracer, "spine", "j", None, None),
        ];
        for mut span in spans {
            span.end();
        }
        let _ = provider.force_flush();
        let exported = exporter.get_finished_spans().expect("finished spans");
        assert_eq!(exported.len(), 5);
        for s in &exported {
            assert_eq!(
                attr_string(s, ATTR_RUN_ID).as_deref(),
                Some("spine"),
                "run_id must be present on span {}",
                s.name
            );
            assert_name_matches_operation(s);
        }
    }
}
