package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/3rg0n/otelstore/emit"
	"github.com/3rg0n/otelstore/internal/query"
	"github.com/3rg0n/otelstore/internal/receiver"
	"github.com/3rg0n/otelstore/internal/store"
	"google.golang.org/protobuf/proto"

	"go.opentelemetry.io/otel"
	otlptracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	otplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
)

// TestEndToEnd drives the FULL loop: the emit/go helper produces spans, a real
// OTLP HTTP exporter ships them over the wire to the receiver, the store
// persists them, and the query API returns them correlated by job_id. The spans
// are NOT hand-built — they are emitted by emit.Start*Span so this test proves
// the emitter's span shapes survive the round trip.
func TestEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	ingestServer := httptest.NewServer(receiver.NewHandler(s))
	defer ingestServer.Close()
	queryServer := httptest.NewServer(query.NewHandler(s))
	defer queryServer.Close()

	runID, workflowName := "R1", "test-workflow"
	jobID, agentID, toolName := "J1", "A1", "exec"

	// --- Wire a REAL OTLP HTTP exporter pointed at the ingest receiver, and
	// install it as the global tracer provider. emit uses otel.Tracer(...) (the
	// global provider), so spans emitted below actually travel over OTLP HTTP. ---
	exp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(ingestServer.URL+"/v1/traces"),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("create otlp exporter: %v", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	otel.SetTracerProvider(tp)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	// --- Emit spans via the emit helper (the spec requirement). ---
	ctx1, workflowSpan := emit.StartWorkflowSpan(ctx, runID, workflowName)
	ctx2, agentSpan := emit.StartAgentSpan(ctx1, runID, jobID, agentID)
	_, toolSpan := emit.StartToolSpan(ctx2, runID, jobID, toolName)
	emit.RecordError(toolSpan, fmt.Errorf("tool execution failed"))

	// Capture the SDK-generated trace id (hex) before ending, to correlate the log.
	traceHex := workflowSpan.SpanContext().TraceID().String()
	tid := workflowSpan.SpanContext().TraceID()

	// End in child-first order and flush so the exporter POSTs before we query.
	toolSpan.End()
	agentSpan.End()
	workflowSpan.End()
	if err := tp.ForceFlush(ctx); err != nil {
		t.Fatalf("force flush exporter: %v", err)
	}

	// --- Ingest a correlated log via OTLP /v1/logs (emit has no log helper). ---
	logReq := &collectorlogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*otplogsv1.ResourceLogs{{
			Resource: &otlpresourcev1.Resource{Attributes: []*otlpcommonv1.KeyValue{}},
			ScopeLogs: []*otplogsv1.ScopeLogs{{
				LogRecords: []*otplogsv1.LogRecord{{
					TraceId:        tid[:],
					TimeUnixNano:   uint64(time.Now().UnixNano()),
					SeverityNumber: 9,
					SeverityText:   "INFO",
					Body:           &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "tool starting"}},
					Attributes: []*otlpcommonv1.KeyValue{
						{Key: emit.AttrRunID, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: runID}}},
						{Key: emit.AttrJobID, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: jobID}}},
					},
				}},
			}},
		}},
	}
	logBytes, err := proto.Marshal(logReq)
	if err != nil {
		t.Fatalf("marshal log req: %v", err)
	}
	logHTTP, err := http.NewRequestWithContext(ctx, http.MethodPost, ingestServer.URL+"/v1/logs", bytes.NewReader(logBytes))
	if err != nil {
		t.Fatalf("build log request: %v", err)
	}
	logHTTP.Header.Set("Content-Type", "application/x-protobuf")
	logResp, err := http.DefaultClient.Do(logHTTP)
	if err != nil {
		t.Fatalf("post logs: %v", err)
	}
	_ = logResp.Body.Close()
	if logResp.StatusCode != http.StatusOK {
		t.Fatalf("log ingest status %d", logResp.StatusCode)
	}

	// --- Query by job_id: the healer's core question. ---
	qResp, err := http.Get(queryServer.URL + "/v1/query?job_id=" + jobID)
	if err != nil {
		t.Fatalf("query by job_id: %v", err)
	}
	defer func() { _ = qResp.Body.Close() }()
	if qResp.StatusCode != http.StatusOK {
		t.Fatalf("query status %d", qResp.StatusCode)
	}

	var qr struct {
		Spans []map[string]any `json:"spans"`
		Logs  []map[string]any `json:"logs"`
	}
	if err := json.NewDecoder(qResp.Body).Decode(&qr); err != nil {
		t.Fatalf("decode query result: %v", err)
	}

	// The agent + tool spans carry job_id=J1 (workflow span does not).
	if len(qr.Spans) < 2 {
		t.Fatalf("expected >=2 spans for job_id=%s, got %d", jobID, len(qr.Spans))
	}
	if len(qr.Logs) < 1 {
		t.Fatalf("expected >=1 correlated log, got %d", len(qr.Logs))
	}

	// Prove the EMIT HELPER's span shapes survived the round trip.
	names := map[string]bool{}
	var errorSpanSeen bool
	for _, sp := range qr.Spans {
		if n, ok := sp["name"].(string); ok {
			names[n] = true
		}
		if code, ok := sp["status_code"].(float64); ok && int(code) == 2 {
			errorSpanSeen = true
		}
		// Every task-level span must carry the promoted job_id.
		if got, _ := sp["job_id"].(string); got != jobID {
			t.Fatalf("span %v has job_id=%q, want %q", sp["name"], got, jobID)
		}
	}
	if !names[emit.OpInvokeAgent] {
		t.Fatalf("no span named %q (emit.StartAgentSpan) in result", emit.OpInvokeAgent)
	}
	if !names[emit.OpExecuteTool] {
		t.Fatalf("no span named %q (emit.StartToolSpan) in result", emit.OpExecuteTool)
	}
	if !errorSpanSeen {
		t.Fatalf("no error span (status_code=2) from emit.RecordError")
	}

	// --- Query the full trace tree; assert ordering by start_ns. ---
	tResp, err := http.Get(queryServer.URL + "/v1/traces/" + traceHex)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	defer func() { _ = tResp.Body.Close() }()
	if tResp.StatusCode != http.StatusOK {
		t.Fatalf("get trace status %d", tResp.StatusCode)
	}

	var tr struct {
		TraceID string           `json:"trace_id"`
		Spans   []map[string]any `json:"spans"`
	}
	if err := json.NewDecoder(tResp.Body).Decode(&tr); err != nil {
		t.Fatalf("decode trace result: %v", err)
	}
	// All three emitted spans share the trace (workflow has no job_id, so it only
	// appears here, not in the job_id query).
	if len(tr.Spans) != 3 {
		t.Fatalf("expected 3 spans in trace %s, got %d", traceHex, len(tr.Spans))
	}
	var prev float64 = -1
	for _, sp := range tr.Spans {
		start, _ := sp["start_ns"].(float64)
		if start < prev {
			t.Fatalf("spans not ordered by start_ns")
		}
		prev = start
	}

	t.Logf("MVP proof: emit helper -> OTLP exporter -> receiver -> store -> query OK "+
		"(%d spans by job_id, %d logs, trace %s has 3 spans)", len(qr.Spans), len(qr.Logs), traceHex)
}
