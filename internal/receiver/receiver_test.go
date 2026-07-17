package receiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/otel/internal/store"
	"google.golang.org/protobuf/proto"

	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	otplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
)

func TestTraceIngest(t *testing.T) {
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

	// Build a minimal trace request
	traceID := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	span := &otlptracev1.Span{
		TraceId:           traceID,
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:              "test-span",
		Kind:              otlptracev1.Span_SPAN_KIND_INTERNAL,
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

	req := &collectortracesv1.ExportTraceServiceRequest{
		ResourceSpans: []*otlptracev1.ResourceSpans{
			{
				Resource: &otlpresourcev1.Resource{
					Attributes: []*otlpcommonv1.KeyValue{},
				},
				ScopeSpans: []*otlptracev1.ScopeSpans{
					{
						Spans: []*otlptracev1.Span{span},
					},
				},
			},
		},
	}

	reqBytes, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	// POST to handler
	httpReq := httptest.NewRequest("POST", "/v1/traces", bytes.NewReader(reqBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestLogsIngest(t *testing.T) {
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

	// Build a minimal logs request
	log := &otplogsv1.LogRecord{
		TimeUnixNano: 1000000000,
		Body: &otlpcommonv1.AnyValue{
			Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "test log"},
		},
		Attributes: []*otlpcommonv1.KeyValue{
			{
				Key: "run_id",
				Value: &otlpcommonv1.AnyValue{
					Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"},
				},
			},
		},
	}

	req := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*otplogsv1.ResourceLogs{
			{
				Resource: &otlpresourcev1.Resource{
					Attributes: []*otlpcommonv1.KeyValue{},
				},
				ScopeLogs: []*otplogsv1.ScopeLogs{
					{
						LogRecords: []*otplogsv1.LogRecord{log},
					},
				},
			},
		},
	}

	reqBytes, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	// POST to handler
	httpReq := httptest.NewRequest("POST", "/v1/logs", bytes.NewReader(reqBytes))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
