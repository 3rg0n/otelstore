package query

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/3rg0n/otelstore/internal/store"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
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

// seedEvent inserts one event (log with event.name) for handler tests.
func seedEvent(t *testing.T, s *store.Store, ctx context.Context, eventName string, sev uint32) {
	t.Helper()
	rec := &otlplogsv1.LogRecord{
		TimeUnixNano:   1000,
		SeverityNumber: otlplogsv1.SeverityNumber(sev),
		Body:           &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "b"}},
		Attributes: []*otlpcommonv1.KeyValue{
			{Key: "event.name", Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: eventName}}},
		},
	}
	if err := s.InsertLogs(ctx, []*otlplogsv1.LogRecord{rec}, nil, nil); err != nil {
		t.Fatalf("InsertLogs: %v", err)
	}
}

func TestGetLogsByEventName(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	seedEvent(t, s, ctx, "api_error", 17)
	seedEvent(t, s, ctx, "user_prompt", 9)

	h := NewHandler(s)

	req := httptest.NewRequest("GET", "/v1/logs?event_name=api_error", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	logs := result["logs"].([]interface{})
	if len(logs) != 1 {
		t.Fatalf("expected 1 log for event_name=api_error, got %d", len(logs))
	}

	// min_severity floor excludes the info-level user_prompt.
	req = httptest.NewRequest("GET", "/v1/logs?min_severity=13", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	json.NewDecoder(w.Body).Decode(&result)
	logs = result["logs"].([]interface{})
	if len(logs) != 1 {
		t.Fatalf("expected 1 log with min_severity=13, got %d", len(logs))
	}
}

func TestHealthAndReady(t *testing.T) {
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

	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, w.Code)
		}
	}
}
