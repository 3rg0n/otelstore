package store

import (
	"context"
	"testing"

	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
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

func TestInsertMetrics(t *testing.T) {
	ctx := context.Background()

	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Create a Gauge metric with a double value
	gaugeMetric := &otlpmetricsv1.Metric{
		Name: "cpu_usage",
		Data: &otlpmetricsv1.Metric_Gauge{
			Gauge: &otlpmetricsv1.Gauge{
				DataPoints: []*otlpmetricsv1.NumberDataPoint{
					{
						TimeUnixNano: 1000000000,
						Value: &otlpmetricsv1.NumberDataPoint_AsDouble{
							AsDouble: 42.5,
						},
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
						},
					},
				},
			},
		},
	}

	// Create a Sum metric with an int value
	sumMetric := &otlpmetricsv1.Metric{
		Name: "request_count",
		Data: &otlpmetricsv1.Metric_Sum{
			Sum: &otlpmetricsv1.Sum{
				DataPoints: []*otlpmetricsv1.NumberDataPoint{
					{
						TimeUnixNano: 2000000000,
						Value: &otlpmetricsv1.NumberDataPoint_AsInt{
							AsInt: 100,
						},
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
									Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "J2"},
								},
							},
						},
					},
				},
			},
		},
	}

	// Insert metrics
	if err := st.InsertMetrics(ctx, []*otlpmetricsv1.Metric{gaugeMetric, sumMetric}, nil, nil); err != nil {
		t.Fatalf("InsertMetrics: %v", err)
	}

	// Query gauge metric
	metrics, err := st.QueryMetrics(ctx, "cpu_usage", 1000)
	if err != nil {
		t.Fatalf("QueryMetrics cpu_usage: %v", err)
	}
	if len(metrics) != 1 {
		t.Errorf("expected 1 cpu_usage metric, got %d", len(metrics))
	}
	gauge := metrics[0]
	if gauge["value_double"] != 42.5 {
		t.Errorf("expected value_double 42.5, got %v", gauge["value_double"])
	}
	if gauge["run_id"] != "R1" {
		t.Errorf("expected run_id R1, got %v", gauge["run_id"])
	}
	if gauge["job_id"] != "J1" {
		t.Errorf("expected job_id J1, got %v", gauge["job_id"])
	}

	// Query sum metric
	metrics, err = st.QueryMetrics(ctx, "request_count", 1000)
	if err != nil {
		t.Fatalf("QueryMetrics request_count: %v", err)
	}
	if len(metrics) != 1 {
		t.Errorf("expected 1 request_count metric, got %d", len(metrics))
	}
	sum := metrics[0]
	if sum["value_double"] != 100.0 {
		t.Errorf("expected value_double 100.0 (from int), got %v", sum["value_double"])
	}
	if sum["job_id"] != "J2" {
		t.Errorf("expected job_id J2, got %v", sum["job_id"])
	}
}

func TestDeleteBefore(t *testing.T) {
	ctx := context.Background()

	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()

	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Insert spans at old and new timestamps
	span := &otlptracev1.Span{
		TraceId:           []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:              "test",
		StartTimeUnixNano: 1000000000, // old
		EndTimeUnixNano:   2000000000,
	}
	if err := st.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil); err != nil {
		t.Fatalf("InsertSpans: %v", err)
	}

	span2 := &otlptracev1.Span{
		TraceId:           []byte{1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{2, 2, 3, 4, 5, 6, 7, 8},
		Name:              "test2",
		StartTimeUnixNano: 5000000000, // new
		EndTimeUnixNano:   6000000000,
	}
	if err := st.InsertSpans(ctx, []*otlptracev1.Span{span2}, nil, nil); err != nil {
		t.Fatalf("InsertSpans: %v", err)
	}

	// Delete before a cutoff that should remove the first span but keep the second
	deleted, err := st.DeleteBefore(ctx, 4000000000)
	if err != nil {
		t.Fatalf("DeleteBefore: %v", err)
	}

	if deleted < 1 {
		t.Errorf("expected at least 1 row deleted, got %d", deleted)
	}

	// Verify old span is gone, new span remains
	spans, _, err := st.QueryByKey(ctx, "trace_id", "000102030405060708090a0b0c0d0e0f", 1000)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}
	if len(spans) != 0 {
		t.Errorf("expected old span to be deleted, got %d", len(spans))
	}

	spans, _, err = st.QueryByKey(ctx, "trace_id", "010102030405060708090a0b0c0d0e0f", 1000)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("expected new span to remain, got %d", len(spans))
	}
}
