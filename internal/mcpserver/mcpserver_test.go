package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/3rg0n/otelstore/internal/store"
)

func seedTestStore(t *testing.T) *store.Store {
	ctx := context.Background()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Insert a successful span
	successSpan := &otlptracev1.Span{
		TraceId:           []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:              "successful_op",
		StartTimeUnixNano: 1000000000,
		EndTimeUnixNano:   2000000000,
		Attributes: []*otlpcommonv1.KeyValue{
			{
				Key: "job_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "J1"},
				},
			},
			{
				Key: "run_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"},
				},
			},
		},
		Status: &otlptracev1.Status{
			Code:    0,
			Message: "OK",
		},
	}

	// Insert an error span
	errorSpan := &otlptracev1.Span{
		TraceId:           []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{2, 2, 3, 4, 5, 6, 7, 8},
		Name:              "failed_op",
		StartTimeUnixNano: 2000000001,
		EndTimeUnixNano:   3000000000,
		Attributes: []*otlpcommonv1.KeyValue{
			{
				Key: "job_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "J1"},
				},
			},
			{
				Key: "run_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"},
				},
			},
		},
		Status: &otlptracev1.Status{
			Code:    2,
			Message: "Internal error",
		},
	}

	if err := st.InsertSpans(ctx, []*otlptracev1.Span{successSpan, errorSpan}, nil, nil); err != nil {
		t.Fatalf("InsertSpans: %v", err)
	}

	// Insert a correlated log
	log := &otplogsv1.LogRecord{
		TraceId:      []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:       []byte{2, 2, 3, 4, 5, 6, 7, 8},
		TimeUnixNano: 2500000000,
		Body: &otlpcommonv1.AnyValue{
			Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "Operation failed"},
		},
		Attributes: []*otlpcommonv1.KeyValue{
			{
				Key: "job_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "J1"},
				},
			},
			{
				Key: "run_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"},
				},
			},
		},
	}

	if err := st.InsertLogs(ctx, []*otplogsv1.LogRecord{log}, nil, nil); err != nil {
		t.Fatalf("InsertLogs: %v", err)
	}

	return st
}

func TestQueryJobTool(t *testing.T) {
	st := seedTestStore(t)
	defer st.Close()

	// NewServer returns a valid MCP server - we test by calling handlers directly
	_ = NewServer(st)

	ctx := context.Background()
	input := QueryJobInput{JobID: "J1"}

	// Call query_job handler directly
	output, err := callQueryJobHandler(ctx, st, input)
	if err != nil {
		t.Fatalf("query_job handler: %v", err)
	}

	if len(output.Spans) != 2 {
		t.Errorf("expected 2 spans, got %d", len(output.Spans))
	}

	if len(output.Logs) != 1 {
		t.Errorf("expected 1 log, got %d", len(output.Logs))
	}

	if len(output.Errors) != 1 {
		t.Errorf("expected 1 error, got %d", len(output.Errors))
	}

	if output.Errors[0]["name"] != "failed_op" {
		t.Errorf("expected error span name 'failed_op', got %v", output.Errors[0]["name"])
	}

	statusCode := output.Errors[0]["status_code"]
	if statusCode != int64(2) {
		t.Errorf("expected status_code 2, got %v", statusCode)
	}
}

func TestQueryJobToolEmptyJobID(t *testing.T) {
	st := seedTestStore(t)
	defer st.Close()

	ctx := context.Background()
	input := QueryJobInput{JobID: ""}

	_, err := callQueryJobHandler(ctx, st, input)
	if err == nil {
		t.Errorf("expected error for empty job_id, got nil")
	}
}

func TestQueryRunTool(t *testing.T) {
	st := seedTestStore(t)
	defer st.Close()

	ctx := context.Background()
	input := QueryRunInput{RunID: "R1"}

	output, err := callQueryRunHandler(ctx, st, input)
	if err != nil {
		t.Fatalf("query_run handler: %v", err)
	}

	if len(output.Spans) != 2 {
		t.Errorf("expected 2 spans, got %d", len(output.Spans))
	}

	if len(output.Logs) != 1 {
		t.Errorf("expected 1 log, got %d", len(output.Logs))
	}
}

func TestQueryRunToolEmptyRunID(t *testing.T) {
	st := seedTestStore(t)
	defer st.Close()

	ctx := context.Background()
	input := QueryRunInput{RunID: ""}

	_, err := callQueryRunHandler(ctx, st, input)
	if err == nil {
		t.Errorf("expected error for empty run_id, got nil")
	}
}

func TestGetTraceTool(t *testing.T) {
	st := seedTestStore(t)
	defer st.Close()

	ctx := context.Background()
	input := GetTraceInput{TraceID: "000102030405060708090a0b0c0d0e0f"}

	output, err := callGetTraceHandler(ctx, st, input)
	if err != nil {
		t.Fatalf("get_trace handler: %v", err)
	}

	if output.TraceID != "000102030405060708090a0b0c0d0e0f" {
		t.Errorf("expected trace_id '000102030405060708090a0b0c0d0e0f', got %s", output.TraceID)
	}

	if len(output.Spans) != 2 {
		t.Errorf("expected 2 spans, got %d", len(output.Spans))
	}
}

func TestGetTraceToolEmptyTraceID(t *testing.T) {
	st := seedTestStore(t)
	defer st.Close()

	ctx := context.Background()
	input := GetTraceInput{TraceID: ""}

	_, err := callGetTraceHandler(ctx, st, input)
	if err == nil {
		t.Errorf("expected error for empty trace_id, got nil")
	}
}

// These wrappers call the REAL tool handlers from mcpserver.go (not a
// reimplementation), so a break in the actual tool logic fails these tests.
func callQueryJobHandler(ctx context.Context, st *store.Store, input QueryJobInput) (QueryJobOutput, error) {
	return queryJobHandler(ctx, st, input)
}

func callQueryRunHandler(ctx context.Context, st *store.Store, input QueryRunInput) (QueryRunOutput, error) {
	return queryRunHandler(ctx, st, input)
}

func callGetTraceHandler(ctx context.Context, st *store.Store, input GetTraceInput) (GetTraceOutput, error) {
	return getTraceHandler(ctx, st, input)
}

// TestJSONSerialization ensures the output structs can be marshaled to JSON
func TestJSONSerialization(t *testing.T) {
	output := QueryJobOutput{
		Spans: []map[string]any{{"name": "span1"}},
		Logs:  []map[string]any{{"body": "log1"}},
		Errors: []map[string]any{
			{"status_code": int64(2), "name": "error_span"},
		},
	}

	data, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded QueryJobOutput
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if len(decoded.Spans) != 1 || len(decoded.Logs) != 1 || len(decoded.Errors) != 1 {
		t.Errorf("JSON serialization mismatch")
	}
}
