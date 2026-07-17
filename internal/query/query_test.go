package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/otel/internal/store"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestGetTrace(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Insert a span
	traceID := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	span := &otlptracev1.Span{
		TraceId:           traceID,
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:              "test-span",
		StartTimeUnixNano: 1000000000,
		EndTimeUnixNano:   2000000000,
		Status:            &otlptracev1.Status{},
		Attributes: []*otlpcommonv1.KeyValue{
			{
				Key: "run_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"},
				},
			},
		},
	}

	s.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil)

	h := NewHandler(s)

	// GET /v1/traces/{trace_id}
	req := httptest.NewRequest("GET", "/v1/traces/000102030405060708090a0b0c0d0e0f", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	if traceIDResult, ok := result["trace_id"]; !ok || traceIDResult != "000102030405060708090a0b0c0d0e0f" {
		t.Errorf("expected trace_id in result")
	}

	spans := result["spans"].([]interface{})
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

func TestQueryWithJobID(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// Insert spans
	span := &otlptracev1.Span{
		TraceId:           []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:              "test-span",
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
		},
	}

	s.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil)

	h := NewHandler(s)

	// GET /v1/query?job_id=J1
	req := httptest.NewRequest("GET", "/v1/query?job_id=J1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	spans := result["spans"].([]interface{})
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}
}

func TestQueryNoFilter(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	h := NewHandler(s)

	// GET /v1/query with no filter
	req := httptest.NewRequest("GET", "/v1/query", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestQueryMultipleFilters(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	h := NewHandler(s)

	// GET /v1/query with multiple filters
	req := httptest.NewRequest("GET", "/v1/query?job_id=J1&run_id=R1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}
