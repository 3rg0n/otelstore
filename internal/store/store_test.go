package store

import (
	"context"
	"testing"

	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestStoreSpanRoundTrip(t *testing.T) {
	ctx := context.Background()

	// Open in-memory store
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	// Initialize schema
	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Create a test span with attributes
	span := &otlptracev1.Span{
		TraceId:           []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		ParentSpanId:      []byte{},
		Name:              "test-span",
		Kind:              otlptracev1.Span_SPAN_KIND_INTERNAL,
		StartTimeUnixNano: 1000000000,
		EndTimeUnixNano:   2000000000,
		Attributes: []*otlpcommonv1.KeyValue{
			{
				Key: "run_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"},
				},
			},
			{
				Key: "job_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "J1"},
				},
			},
			{
				Key: "custom_attr",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "value1"},
				},
			},
		},
		Status: &otlptracev1.Status{
			Code:    0,
			Message: "",
		},
	}

	// Insert span
	if err := st.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil); err != nil {
		t.Fatalf("InsertSpans: %v", err)
	}

	// Query by trace_id
	spans, logs, err := st.QueryByKey(ctx, "trace_id", "000102030405060708090a0b0c0d0e0f", 1000)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}

	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 logs, got %d", len(logs))
	}

	s := spans[0]
	if s["name"] != "test-span" {
		t.Errorf("expected name 'test-span', got '%v'", s["name"])
	}
	if s["run_id"] != "R1" {
		t.Errorf("expected run_id 'R1', got '%v'", s["run_id"])
	}
	if s["job_id"] != "J1" {
		t.Errorf("expected job_id 'J1', got '%v'", s["job_id"])
	}

	// Check attributes are preserved
	attrs, ok := s["attributes"].(map[string]any)
	if !ok {
		t.Errorf("attributes not a map")
	}
	if attrs["custom_attr"] != "value1" {
		t.Errorf("expected custom_attr='value1', got %v", attrs["custom_attr"])
	}
}

func TestFilterByKey(t *testing.T) {
	ctx := context.Background()

	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Insert spans with different run_id and job_id
	for i := 0; i < 3; i++ {
		span := &otlptracev1.Span{
			TraceId:           []byte{byte(i), 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
			SpanId:            []byte{byte(i), 2, 3, 4, 5, 6, 7, 8},
			Name:              "span",
			StartTimeUnixNano: uint64(1000000000 + int64(i)*1000000),
			EndTimeUnixNano:   uint64(2000000000 + int64(i)*1000000),
			Status:            &otlptracev1.Status{},
			Attributes: []*otlpcommonv1.KeyValue{
				{
					Key: "run_id",
					Value: &otlpcommonv1.AnyValue{
						Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"},
					},
				},
				{
					Key: "job_id",
					Value: &otlpcommonv1.AnyValue{
						Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "J" + string(rune(i+49))},
					},
				},
			},
		}
		if err := st.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil); err != nil {
			t.Fatalf("InsertSpans: %v", err)
		}
	}

	// Query by run_id should get all 3
	spans, _, err := st.QueryByKey(ctx, "run_id", "R1", 1000)
	if err != nil {
		t.Fatalf("QueryByKey run_id: %v", err)
	}
	if len(spans) != 3 {
		t.Errorf("expected 3 spans for run_id, got %d", len(spans))
	}

	// Query by job_id should get 1
	spans, _, err = st.QueryByKey(ctx, "job_id", "J1", 1000)
	if err != nil {
		t.Fatalf("QueryByKey job_id: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("expected 1 span for job_id, got %d", len(spans))
	}
	if spans[0]["job_id"] != "J1" {
		t.Errorf("expected job_id J1, got %v", spans[0]["job_id"])
	}
}

func TestQueryByKeyBadFilter(t *testing.T) {
	ctx := context.Background()

	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Query with an invalid key should return error
	_, _, err = st.QueryByKey(ctx, "invalid_key", "value", 1000)
	if err == nil {
		t.Errorf("expected error for invalid key, got nil")
	}

	// Query with nonexistent value should return empty results
	spans, logs, err := st.QueryByKey(ctx, "run_id", "nonexistent", 1000)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}
	if len(spans) != 0 || len(logs) != 0 {
		t.Errorf("expected empty results, got %d spans and %d logs", len(spans), len(logs))
	}
}

func TestErrorSpans(t *testing.T) {
	spans := []map[string]any{
		{
			"trace_id":    "abc123",
			"span_id":     "span1",
			"name":        "successful_span",
			"status_code": int64(0),
		},
		{
			"trace_id":    "abc123",
			"span_id":     "span2",
			"name":        "error_span",
			"status_code": int64(2),
		},
		{
			"trace_id":    "abc123",
			"span_id":     "span3",
			"name":        "another_error",
			"status_code": int64(2),
		},
	}

	errors := ErrorSpans(spans)

	if len(errors) != 2 {
		t.Errorf("Expected 2 error spans, got %d", len(errors))
	}

	for _, span := range errors {
		code := span["status_code"].(int64)
		if code != 2 {
			t.Errorf("Expected status_code 2, got %d", code)
		}
	}
}

func TestErrorSpansEmpty(t *testing.T) {
	spans := []map[string]any{
		{
			"trace_id":    "abc123",
			"span_id":     "span1",
			"name":        "successful_span",
			"status_code": int64(0),
		},
	}

	errors := ErrorSpans(spans)

	if len(errors) != 0 {
		t.Errorf("Expected 0 error spans, got %d", len(errors))
	}
}
