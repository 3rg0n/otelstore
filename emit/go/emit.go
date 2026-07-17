// Package emit provides OpenTelemetry span helpers that encode the emit-side
// telemetry contract. Each helper sets required owned attributes automatically
// and ensures correct span names and gen_ai.operation.name values.
package emit

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Tracer name constant — used as the instrumentation scope name.
const tracerName = "github.com/otel/emit"

// Correlation spine — owned keys (stable, never subject to semconv churn).
const (
	AttrRunID        = "run_id"
	AttrJobID        = "job_id"
	AttrWorkflowName = "workflow_name"
	AttrAgentID      = "agent_id"
	AttrToolName     = "tool_name"
)

// Operation names — allowed values for gen_ai.operation.name.
// The OTel span name MUST equal these exactly (see overlay-schema.md), and must
// match the Rust helper byte-for-byte.
const (
	OpInvokeWorkflow = "invoke_workflow"
	OpInvokeAgent    = "invoke_agent"
	OpPlan           = "plan"
	OpExecuteTool    = "execute_tool"
	OpModelCall      = "chat" // model spans use the GenAI "chat" operation
)

// GenAI decoration keys — centralized so semconv version bumps edit one spot.
const (
	AttrGenAIOperationName    = "gen_ai.operation.name"
	AttrGenAIWorkflowName     = "gen_ai.workflow.name"
	AttrGenAIConversationID   = "gen_ai.conversation.id"
	AttrGenAIAgentID          = "gen_ai.agent.id"
	AttrGenAIAgentName        = "gen_ai.agent.name"
	AttrGenAIAgentVersion     = "gen_ai.agent.version"
	AttrGenAIToolName         = "gen_ai.tool.name"
	AttrGenAIToolCallID       = "gen_ai.tool.call.id"
	AttrGenAIProviderName     = "gen_ai.provider.name"
	AttrGenAIRequestModel     = "gen_ai.request.model"
)

// getTracer returns the package-level tracer.
func getTracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// StartWorkflowSpan starts a workflow-run span with required owned attributes.
// Sets operation.name=invoke_workflow, run_id, workflow_name.
// Decoration: gen_ai.workflow.name, gen_ai.conversation.id.
// Returns the updated context and the span.
func StartWorkflowSpan(ctx context.Context, runID, workflowName string) (context.Context, trace.Span) {
	tracer := getTracer()
	ctx, span := tracer.Start(ctx, OpInvokeWorkflow, trace.WithSpanKind(trace.SpanKindInternal))

	span.SetAttributes(
		attribute.String(AttrRunID, runID),
		attribute.String(AttrWorkflowName, workflowName),
		attribute.String(AttrGenAIOperationName, OpInvokeWorkflow),
		attribute.String(AttrGenAIWorkflowName, workflowName),
		attribute.String(AttrGenAIConversationID, runID), // grain: workflow run
	)

	return ctx, span
}

// StartAgentSpan starts an agent-invocation span with required owned attributes.
// Sets operation.name=invoke_agent, run_id, job_id, agent_id.
// Decoration: gen_ai.agent.id.
func StartAgentSpan(ctx context.Context, runID, jobID, agentID string) (context.Context, trace.Span) {
	tracer := getTracer()
	ctx, span := tracer.Start(ctx, OpInvokeAgent, trace.WithSpanKind(trace.SpanKindInternal))

	span.SetAttributes(
		attribute.String(AttrRunID, runID),
		attribute.String(AttrJobID, jobID),
		attribute.String(AttrAgentID, agentID),
		attribute.String(AttrGenAIOperationName, OpInvokeAgent),
		attribute.String(AttrGenAIAgentID, agentID),
	)

	return ctx, span
}

// StartPlanSpan starts a planning span with required owned attributes.
// Sets operation.name=plan, run_id, job_id.
// Span name: "plan".
func StartPlanSpan(ctx context.Context, runID, jobID string) (context.Context, trace.Span) {
	tracer := getTracer()
	ctx, span := tracer.Start(ctx, OpPlan, trace.WithSpanKind(trace.SpanKindInternal))

	span.SetAttributes(
		attribute.String(AttrRunID, runID),
		attribute.String(AttrJobID, jobID),
		attribute.String(AttrGenAIOperationName, OpPlan),
	)

	return ctx, span
}

// StartToolSpan starts a tool/verb execution span with required owned attributes.
// Sets operation.name=execute_tool, run_id, job_id, tool_name.
// Decoration: gen_ai.tool.name.
func StartToolSpan(ctx context.Context, runID, jobID, toolName string) (context.Context, trace.Span) {
	tracer := getTracer()
	ctx, span := tracer.Start(ctx, OpExecuteTool, trace.WithSpanKind(trace.SpanKindInternal))

	span.SetAttributes(
		attribute.String(AttrRunID, runID),
		attribute.String(AttrJobID, jobID),
		attribute.String(AttrToolName, toolName),
		attribute.String(AttrGenAIOperationName, OpExecuteTool),
		attribute.String(AttrGenAIToolName, toolName),
	)

	return ctx, span
}

// ModelOption is an option function for configuring model spans.
type ModelOption func(attrs *[]attribute.KeyValue)

// WithProviderName sets the provider name in the span attributes.
func WithProviderName(name string) ModelOption {
	return func(attrs *[]attribute.KeyValue) {
		*attrs = append(*attrs, attribute.String(AttrGenAIProviderName, name))
	}
}

// WithRequestModel sets the request model in the span attributes.
func WithRequestModel(model string) ModelOption {
	return func(attrs *[]attribute.KeyValue) {
		*attrs = append(*attrs, attribute.String(AttrGenAIRequestModel, model))
	}
}

// StartModelSpan starts a model-call span with required owned attributes.
// Sets operation.name derived from span kind (CLIENT for model calls), run_id, job_id.
// Decoration: gen_ai.provider.name, gen_ai.request.model (via opts).
func StartModelSpan(ctx context.Context, runID, jobID string, opts ...ModelOption) (context.Context, trace.Span) {
	tracer := getTracer()
	ctx, span := tracer.Start(ctx, OpModelCall, trace.WithSpanKind(trace.SpanKindClient))

	attrs := []attribute.KeyValue{
		attribute.String(AttrRunID, runID),
		attribute.String(AttrJobID, jobID),
		attribute.String(AttrGenAIOperationName, OpModelCall),
	}

	// Apply options to add optional attributes (provider, model).
	for _, opt := range opts {
		opt(&attrs)
	}

	span.SetAttributes(attrs...)

	return ctx, span
}

// RecordError sets the span status to ERROR and records the exception.
// The overlay requires failed spans carry ERROR status + recorded exception.
func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}

	span.SetStatus(codes.Error, err.Error())
	span.RecordError(err)
}
