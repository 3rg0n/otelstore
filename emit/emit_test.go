package emit

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// setup creates an in-memory span recorder and sets the global tracer provider.
func setup(t *testing.T) (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Logf("provider shutdown error: %v", err)
		}
	})
	return exporter, provider
}

// getAttributes returns a map of attributes for easier assertion.
func getAttributes(span tracetest.SpanStub) map[string]interface{} {
	result := make(map[string]interface{})
	for _, kv := range span.Attributes {
		result[string(kv.Key)] = kv.Value.AsInterface()
	}
	return result
}

// TestStartWorkflowSpan asserts the workflow-run span has correct shape.
func TestStartWorkflowSpan(t *testing.T) {
	exporter, provider := setup(t)
	ctx := context.Background()

	runID := "run-123"
	workflowName := "my-workflow"

	_, span := StartWorkflowSpan(ctx, runID, workflowName)
	span.End()

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Logf("force flush error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]
	attrs := getAttributes(s)

	// Assertions per spec.
	if s.Name != OpInvokeWorkflow {
		t.Errorf("expected span name %q, got %q", OpInvokeWorkflow, s.Name)
	}

	// Required owned attributes.
	if attrs[AttrRunID] != runID {
		t.Errorf("expected %s=%q, got %v", AttrRunID, runID, attrs[AttrRunID])
	}
	if attrs[AttrWorkflowName] != workflowName {
		t.Errorf("expected %s=%q, got %v", AttrWorkflowName, workflowName, attrs[AttrWorkflowName])
	}

	// gen_ai.operation.name must be invoke_workflow.
	if attrs[AttrGenAIOperationName] != OpInvokeWorkflow {
		t.Errorf("expected %s=%q, got %v", AttrGenAIOperationName, OpInvokeWorkflow, attrs[AttrGenAIOperationName])
	}

	// Decoration attributes.
	if attrs[AttrGenAIWorkflowName] != workflowName {
		t.Errorf("expected %s=%q, got %v", AttrGenAIWorkflowName, workflowName, attrs[AttrGenAIWorkflowName])
	}
	if attrs[AttrGenAIConversationID] != runID {
		t.Errorf("expected %s=%q (grain: run_id), got %v", AttrGenAIConversationID, runID, attrs[AttrGenAIConversationID])
	}

	// job_id should NOT be present on workflow span.
	if _, ok := attrs[AttrJobID]; ok {
		t.Errorf("expected %s to be absent on workflow span", AttrJobID)
	}

	if s.SpanKind != trace.SpanKindInternal {
		t.Errorf("expected SpanKindInternal, got %v", s.SpanKind)
	}
}

// TestStartAgentSpan asserts the agent-invocation span has correct shape.
func TestStartAgentSpan(t *testing.T) {
	exporter, provider := setup(t)
	ctx := context.Background()

	runID := "run-456"
	jobID := "job-789"
	agentID := "agent-001"

	_, span := StartAgentSpan(ctx, runID, jobID, agentID)
	span.End()

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Logf("force flush error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]
	attrs := getAttributes(s)

	if s.Name != OpInvokeAgent {
		t.Errorf("expected span name %q, got %q", OpInvokeAgent, s.Name)
	}

	// Required owned attributes.
	if attrs[AttrRunID] != runID {
		t.Errorf("expected %s=%q, got %v", AttrRunID, runID, attrs[AttrRunID])
	}
	if attrs[AttrJobID] != jobID {
		t.Errorf("expected %s=%q, got %v", AttrJobID, jobID, attrs[AttrJobID])
	}
	if attrs[AttrAgentID] != agentID {
		t.Errorf("expected %s=%q, got %v", AttrAgentID, agentID, attrs[AttrAgentID])
	}

	// gen_ai.operation.name must be invoke_agent.
	if attrs[AttrGenAIOperationName] != OpInvokeAgent {
		t.Errorf("expected %s=%q, got %v", AttrGenAIOperationName, OpInvokeAgent, attrs[AttrGenAIOperationName])
	}

	// Decoration.
	if attrs[AttrGenAIAgentID] != agentID {
		t.Errorf("expected %s=%q, got %v", AttrGenAIAgentID, agentID, attrs[AttrGenAIAgentID])
	}

	if s.SpanKind != trace.SpanKindInternal {
		t.Errorf("expected SpanKindInternal, got %v", s.SpanKind)
	}
}

// TestStartPlanSpan asserts the planning span has correct shape.
func TestStartPlanSpan(t *testing.T) {
	exporter, provider := setup(t)
	ctx := context.Background()

	runID := "run-111"
	jobID := "job-222"

	_, span := StartPlanSpan(ctx, runID, jobID)
	span.End()

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Logf("force flush error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]
	attrs := getAttributes(s)

	if s.Name != OpPlan {
		t.Errorf("expected span name %q, got %q", OpPlan, s.Name)
	}

	// Required owned attributes.
	if attrs[AttrRunID] != runID {
		t.Errorf("expected %s=%q, got %v", AttrRunID, runID, attrs[AttrRunID])
	}
	if attrs[AttrJobID] != jobID {
		t.Errorf("expected %s=%q, got %v", AttrJobID, jobID, attrs[AttrJobID])
	}

	// gen_ai.operation.name must be plan.
	if attrs[AttrGenAIOperationName] != OpPlan {
		t.Errorf("expected %s=%q, got %v", AttrGenAIOperationName, OpPlan, attrs[AttrGenAIOperationName])
	}

	if s.SpanKind != trace.SpanKindInternal {
		t.Errorf("expected SpanKindInternal, got %v", s.SpanKind)
	}
}

// TestStartToolSpan asserts the tool execution span has correct shape.
func TestStartToolSpan(t *testing.T) {
	exporter, provider := setup(t)
	ctx := context.Background()

	runID := "run-333"
	jobID := "job-444"
	toolName := "execute_sql"

	_, span := StartToolSpan(ctx, runID, jobID, toolName)
	span.End()

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Logf("force flush error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]
	attrs := getAttributes(s)

	if s.Name != OpExecuteTool {
		t.Errorf("expected span name %q, got %q", OpExecuteTool, s.Name)
	}

	// Required owned attributes.
	if attrs[AttrRunID] != runID {
		t.Errorf("expected %s=%q, got %v", AttrRunID, runID, attrs[AttrRunID])
	}
	if attrs[AttrJobID] != jobID {
		t.Errorf("expected %s=%q, got %v", AttrJobID, jobID, attrs[AttrJobID])
	}
	if attrs[AttrToolName] != toolName {
		t.Errorf("expected %s=%q, got %v", AttrToolName, toolName, attrs[AttrToolName])
	}

	// gen_ai.operation.name must be execute_tool.
	if attrs[AttrGenAIOperationName] != OpExecuteTool {
		t.Errorf("expected %s=%q, got %v", AttrGenAIOperationName, OpExecuteTool, attrs[AttrGenAIOperationName])
	}

	// Decoration.
	if attrs[AttrGenAIToolName] != toolName {
		t.Errorf("expected %s=%q, got %v", AttrGenAIToolName, toolName, attrs[AttrGenAIToolName])
	}

	if s.SpanKind != trace.SpanKindInternal {
		t.Errorf("expected SpanKindInternal, got %v", s.SpanKind)
	}
}

// TestStartModelSpan asserts the model-call span has correct shape with options.
func TestStartModelSpan(t *testing.T) {
	tests := []struct {
		name          string
		runID         string
		jobID         string
		opts          []ModelOption
		expectedAttrs map[string]interface{}
	}{
		{
			name:  "model span with provider and model",
			runID: "run-555",
			jobID: "job-666",
			opts: []ModelOption{
				WithProviderName("anthropic"),
				WithRequestModel("claude-3-opus"),
			},
			expectedAttrs: map[string]interface{}{
				AttrRunID:             "run-555",
				AttrJobID:             "job-666",
				AttrGenAIOperationName: OpModelCall,
				AttrGenAIProviderName:  "anthropic",
				AttrGenAIRequestModel:  "claude-3-opus",
			},
		},
		{
			name:  "model span with no options",
			runID: "run-777",
			jobID: "job-888",
			opts:  nil,
			expectedAttrs: map[string]interface{}{
				AttrRunID:             "run-777",
				AttrJobID:             "job-888",
				AttrGenAIOperationName: OpModelCall,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter, provider := setup(t)
			ctx := context.Background()

			_, span := StartModelSpan(ctx, tt.runID, tt.jobID, tt.opts...)
			span.End()

			if err := provider.ForceFlush(context.Background()); err != nil {
				t.Logf("force flush error: %v", err)
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}

			s := spans[0]
			attrs := getAttributes(s)

			if s.Name != OpModelCall {
				t.Errorf("expected span name %q, got %q", OpModelCall, s.Name)
			}

			// Check all expected attributes.
			for key, expectedVal := range tt.expectedAttrs {
				if attrs[key] != expectedVal {
					t.Errorf("expected %s=%v, got %v", key, expectedVal, attrs[key])
				}
			}

			if s.SpanKind != trace.SpanKindClient {
				t.Errorf("expected SpanKindClient, got %v", s.SpanKind)
			}
		})
	}
}

// TestRecordError asserts that RecordError sets span status to ERROR and records exception.
func TestRecordError(t *testing.T) {
	exporter, provider := setup(t)
	ctx := context.Background()

	_, span := StartPlanSpan(ctx, "run-999", "job-101112")

	testErr := errors.New("something went wrong")
	RecordError(span, testErr)
	span.End()

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Logf("force flush error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]

	// Span status must be ERROR.
	if s.Status.Code != codes.Error {
		t.Errorf("expected status code Error, got %v", s.Status.Code)
	}
	if s.Status.Description != testErr.Error() {
		t.Errorf("expected status description %q, got %q", testErr.Error(), s.Status.Description)
	}

	// Exception event must be recorded.
	if len(s.Events) == 0 {
		t.Errorf("expected exception event to be recorded, got no events")
	}

	hasExceptionEvent := false
	for _, evt := range s.Events {
		if evt.Name == "exception" {
			hasExceptionEvent = true
			break
		}
	}
	if !hasExceptionEvent {
		t.Errorf("expected 'exception' event, events: %v", s.Events)
	}
}

// TestRecordErrorWithNil asserts that RecordError handles nil gracefully.
func TestRecordErrorWithNil(t *testing.T) {
	exporter, provider := setup(t)
	ctx := context.Background()

	_, span := StartPlanSpan(ctx, "run-202122", "job-232425")

	// RecordError should not fail on nil.
	RecordError(span, nil)
	span.End()

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Logf("force flush error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]

	// Status should not be set to Error.
	if s.Status.Code == codes.Error {
		t.Errorf("expected status code not to be Error when error is nil, got %v", s.Status.Code)
	}
}

// TestRunIDPresentOnAllSpans asserts that run_id is present on all span types.
func TestRunIDPresentOnAllSpans(t *testing.T) {
	testCases := []struct {
		name       string
		startSpan  func(context.Context, string) (context.Context, trace.Span)
		expectedOp string
	}{
		{
			name: "workflow",
			startSpan: func(ctx context.Context, runID string) (context.Context, trace.Span) {
				return StartWorkflowSpan(ctx, runID, "test-workflow")
			},
			expectedOp: OpInvokeWorkflow,
		},
		{
			name: "agent",
			startSpan: func(ctx context.Context, runID string) (context.Context, trace.Span) {
				return StartAgentSpan(ctx, runID, "job-1", "agent-1")
			},
			expectedOp: OpInvokeAgent,
		},
		{
			name: "plan",
			startSpan: func(ctx context.Context, runID string) (context.Context, trace.Span) {
				return StartPlanSpan(ctx, runID, "job-2")
			},
			expectedOp: OpPlan,
		},
		{
			name: "tool",
			startSpan: func(ctx context.Context, runID string) (context.Context, trace.Span) {
				return StartToolSpan(ctx, runID, "job-3", "test-tool")
			},
			expectedOp: OpExecuteTool,
		},
		{
			name: "model",
			startSpan: func(ctx context.Context, runID string) (context.Context, trace.Span) {
				return StartModelSpan(ctx, runID, "job-4")
			},
			expectedOp: OpModelCall,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exporter, provider := setup(t)
			ctx := context.Background()
			runID := "universal-run-id"

			_, span := tc.startSpan(ctx, runID)
			span.End()

			if err := provider.ForceFlush(context.Background()); err != nil {
				t.Logf("force flush error: %v", err)
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}

			s := spans[0]
			attrs := getAttributes(s)

			if attrs[AttrRunID] != runID {
				t.Errorf("expected %s=%q, got %v", AttrRunID, runID, attrs[AttrRunID])
			}
			if attrs[AttrGenAIOperationName] != tc.expectedOp {
				t.Errorf("expected %s=%q, got %v", AttrGenAIOperationName, tc.expectedOp, attrs[AttrGenAIOperationName])
			}
		})
	}
}

// TestJobIDAbsentOnWorkflowSpan asserts that job_id is NOT present on workflow spans.
func TestJobIDAbsentOnWorkflowSpan(t *testing.T) {
	exporter, provider := setup(t)
	ctx := context.Background()

	_, span := StartWorkflowSpan(ctx, "run-final", "workflow-final")
	span.End()

	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Logf("force flush error: %v", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}

	s := spans[0]
	attrs := getAttributes(s)

	if _, ok := attrs[AttrJobID]; ok {
		t.Errorf("expected %s to be absent on workflow span", AttrJobID)
	}
}

// TestJobIDPresentOnTaskSpans asserts that job_id is present on all task-level spans.
func TestJobIDPresentOnTaskSpans(t *testing.T) {
	testCases := []struct {
		name      string
		startSpan func(context.Context, string, string) (context.Context, trace.Span)
	}{
		{
			name: "agent",
			startSpan: func(ctx context.Context, runID, jobID string) (context.Context, trace.Span) {
				return StartAgentSpan(ctx, runID, jobID, "agent-x")
			},
		},
		{
			name: "plan",
			startSpan: func(ctx context.Context, runID, jobID string) (context.Context, trace.Span) {
				return StartPlanSpan(ctx, runID, jobID)
			},
		},
		{
			name: "tool",
			startSpan: func(ctx context.Context, runID, jobID string) (context.Context, trace.Span) {
				return StartToolSpan(ctx, runID, jobID, "tool-x")
			},
		},
		{
			name: "model",
			startSpan: func(ctx context.Context, runID, jobID string) (context.Context, trace.Span) {
				return StartModelSpan(ctx, runID, jobID)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			exporter, provider := setup(t)
			ctx := context.Background()
			runID := "run-task"
			jobID := "job-task-" + tc.name

			_, span := tc.startSpan(ctx, runID, jobID)
			span.End()

			if err := provider.ForceFlush(context.Background()); err != nil {
				t.Logf("force flush error: %v", err)
			}

			spans := exporter.GetSpans()
			if len(spans) != 1 {
				t.Fatalf("expected 1 span, got %d", len(spans))
			}

			s := spans[0]
			attrs := getAttributes(s)

			if attrs[AttrJobID] != jobID {
				t.Errorf("expected %s=%q on %s span, got %v", AttrJobID, jobID, tc.name, attrs[AttrJobID])
			}
		})
	}
}
