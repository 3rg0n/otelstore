package receiver

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/3rg0n/otelstore/internal/store"
	"google.golang.org/protobuf/proto"

	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

// newTestStore opens an in-memory store with the schema applied.
func newTestStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	return s, ctx
}

// captureLogs redirects the standard logger for the duration of a test and
// returns a func yielding everything written to it.
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return buf.String
}

// TestTraceIngestJSON is the regression test for issue #7: a client exporting
// OTLP/JSON (Content-Type: application/json) must be accepted and its spans
// stored, not rejected with 400. Previously every JSON body hit proto.Unmarshal
// and failed.
func TestTraceIngestJSON(t *testing.T) {
	s, ctx := newTestStore(t)
	h := NewHandler(s)

	// A real OTLP/JSON body: hex trace/span ids and string-encoded nanos, which
	// is how protojson represents bytes and 64-bit ints on the wire.
	body := `{
	  "resourceSpans": [{
	    "resource": {"attributes": []},
	    "scopeSpans": [{
	      "spans": [{
	        "traceId": "000102030405060708090a0b0c0d0e0f",
	        "spanId": "0102030405060708",
	        "name": "json-span",
	        "kind": 1,
	        "startTimeUnixNano": "1000000000",
	        "endTimeUnixNano": "2000000000",
	        "status": {},
	        "attributes": [
	          {"key": "run_id", "value": {"stringValue": "RJSON"}}
	        ]
	      }]
	    }]
	  }]
	}`

	httpReq := httptest.NewRequest("POST", "/v1/traces", strings.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for OTLP/JSON, got %d (body %q)", w.Code, w.Body.String())
	}

	// The span must actually be persisted — a 200 alone would not prove decoding
	// worked, since an empty request also returns 200.
	spans, _, err := s.QueryByKey(ctx, "run_id", "RJSON", 10)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 stored span from the JSON export, got %d", len(spans))
	}
	if got := spans[0]["name"]; got != "json-span" {
		t.Errorf("stored span name = %v, want json-span", got)
	}

	// A JSON client must get a JSON response, not protobuf.
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("response Content-Type = %q, want application/json", ct)
	}
}

// TestJSONIngestWithCharsetAndUnknownFields covers two things real exporters do:
// send "application/json; charset=utf-8", and emit fields newer than our pinned
// OTLP proto. Neither may cause a 400.
func TestJSONIngestWithCharsetAndUnknownFields(t *testing.T) {
	s, ctx := newTestStore(t)
	h := NewHandler(s)

	body := `{
	  "resourceSpans": [{
	    "scopeSpans": [{
	      "spans": [{
	        "traceId": "000102030405060708090a0b0c0d0e0f",
	        "spanId": "0102030405060708",
	        "name": "fwd-compat",
	        "startTimeUnixNano": "1000000000",
	        "endTimeUnixNano": "2000000000",
	        "attributes": [
	          {"key": "run_id", "value": {"stringValue": "RFWD"}}
	        ],
	        "someFutureFieldNotInOurProto": {"nested": true}
	      }]
	    }]
	  }]
	}`

	httpReq := httptest.NewRequest("POST", "/v1/traces", strings.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for charset + unknown field, got %d (body %q)", w.Code, w.Body.String())
	}

	spans, _, err := s.QueryByKey(ctx, "run_id", "RFWD", 10)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("expected 1 stored span, got %d", len(spans))
	}
}

// TestLogsAndMetricsIngestJSON asserts the JSON path is wired for all three
// signals, not just traces — the Copilot client in issue #7 sent mostly
// metrics and logs.
func TestLogsAndMetricsIngestJSON(t *testing.T) {
	s, _ := newTestStore(t)
	h := NewHandler(s)

	cases := []struct {
		name string
		path string
		body string
	}{
		{
			name: "logs",
			path: "/v1/logs",
			body: `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{` +
				`"timeUnixNano":"1000000000",` +
				`"body":{"stringValue":"json log"},` +
				`"attributes":[{"key":"run_id","value":{"stringValue":"RL"}}]` +
				`}]}]}]}`,
		},
		{
			name: "metrics",
			path: "/v1/metrics",
			body: `{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{` +
				`"name":"json.metric",` +
				`"gauge":{"dataPoints":[{"timeUnixNano":"1000000000","asDouble":1.5,` +
				`"attributes":[{"key":"run_id","value":{"stringValue":"RM"}}]}]}` +
				`}]}]}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			httpReq := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			httpReq.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httpReq)

			if w.Code != http.StatusOK {
				t.Fatalf("%s: expected 200, got %d (body %q)", tc.path, w.Code, w.Body.String())
			}
		})
	}
}

// TestProtobufStillWorksAfterJSONSupport guards against regressing the default
// encoding: no Content-Type, and an explicit protobuf one, must both still
// decode as binary protobuf.
func TestProtobufStillWorksAfterJSONSupport(t *testing.T) {
	s, ctx := newTestStore(t)
	h := NewHandler(s)

	// Each post carries a distinct span id — (trace_id, span_id) is unique in the
	// store, so reusing one would fail on the second insert for reasons unrelated
	// to encoding.
	for i, ct := range []string{"", "application/x-protobuf"} {
		req := &collectortracesv1.ExportTraceServiceRequest{
			ResourceSpans: []*otlptracev1.ResourceSpans{{
				Resource: &otlpresourcev1.Resource{Attributes: []*otlpcommonv1.KeyValue{}},
				ScopeSpans: []*otlptracev1.ScopeSpans{{
					Spans: []*otlptracev1.Span{{
						TraceId:           []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
						SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, byte(i)},
						Name:              "proto-span",
						StartTimeUnixNano: 1000000000,
						EndTimeUnixNano:   2000000000,
						Status:            &otlptracev1.Status{},
						Attributes: []*otlpcommonv1.KeyValue{{
							Key:   "run_id",
							Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "RPROTO"}},
						}},
					}},
				}},
			}},
		}
		reqBytes, err := proto.Marshal(req)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		httpReq := httptest.NewRequest("POST", "/v1/traces", bytes.NewReader(reqBytes))
		if ct != "" {
			httpReq.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httpReq)

		if w.Code != http.StatusOK {
			t.Fatalf("content-type %q: expected 200, got %d (body %q)", ct, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != "application/x-protobuf" {
			t.Errorf("content-type %q: response Content-Type = %q, want application/x-protobuf", ct, got)
		}
	}

	spans, _, err := s.QueryByKey(ctx, "run_id", "RPROTO", 10)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("expected 2 stored spans (one per encoding header), got %d", len(spans))
	}
}

// TestRejectionsAreLogged is the regression test for issue #8: no rejection path
// may be silent. Previously a receiver refusing 100% of traffic logged nothing.
func TestRejectionsAreLogged(t *testing.T) {
	s, _ := newTestStore(t)
	h := NewHandler(s)

	cases := []struct {
		name       string
		method     string
		path       string
		ct         string
		body       string
		wantStatus int
		wantLog    string
	}{
		{
			name:       "unmarshal failure",
			method:     "POST",
			path:       "/v1/traces",
			ct:         "application/x-protobuf",
			body:       "not a protobuf",
			wantStatus: http.StatusBadRequest,
			wantLog:    "unmarshal",
		},
		{
			name:       "malformed json",
			method:     "POST",
			path:       "/v1/metrics",
			ct:         "application/json",
			body:       `{"resourceMetrics": [`,
			wantStatus: http.StatusBadRequest,
			wantLog:    "unmarshal",
		},
		{
			name:       "wrong method",
			method:     "GET",
			path:       "/v1/logs",
			wantStatus: http.StatusMethodNotAllowed,
			wantLog:    "method",
		},
		{
			name:       "unknown route",
			method:     "POST",
			path:       "/v1/nope",
			wantStatus: http.StatusNotFound,
			wantLog:    "unknown route",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureLogs(t)

			httpReq := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.ct != "" {
				httpReq.Header.Set("Content-Type", tc.ct)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httpReq)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
			}

			out := logged()
			if out == "" {
				t.Fatal("rejection was silent: nothing logged")
			}
			if !strings.Contains(out, tc.wantLog) {
				t.Errorf("log %q does not mention %q", out, tc.wantLog)
			}
			if !strings.Contains(out, tc.path) {
				t.Errorf("log %q does not include the request path %q", out, tc.path)
			}
		})
	}
}

// TestSuccessPathStaysQuiet keeps the fix for #8 from becoming log spam: the ask
// was that failures are never silent, not that every export prints a line.
func TestSuccessPathStaysQuiet(t *testing.T) {
	s, _ := newTestStore(t)
	h := NewHandler(s)

	logged := captureLogs(t)

	httpReq := httptest.NewRequest("POST", "/v1/traces", strings.NewReader(`{"resourceSpans":[]}`))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httpReq)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if out := logged(); out != "" {
		t.Errorf("success path should be quiet, logged: %q", out)
	}
}

// TestLogInjectionIsNeutralized asserts CR/LF in a client-controlled field
// cannot forge extra log lines (CWE-117), matching the sanitization the auth
// middleware already applies.
func TestLogInjectionIsNeutralized(t *testing.T) {
	s, _ := newTestStore(t)
	h := NewHandler(s)

	logged := captureLogs(t)

	httpReq := httptest.NewRequest("POST", "/v1/traces", strings.NewReader("bad"))
	httpReq.Header.Set("Content-Type", "application/x-protobuf\r\ningest: FORGED ADMIN LINE")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httpReq)

	out := logged()
	if strings.Contains(out, "\r") {
		t.Error("carriage return survived into the log line")
	}
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
		t.Errorf("injected newline split the log into multiple lines: %q", out)
	}
}
