package receiver

import (
	"io"
	"net/http"

	"github.com/3rg0n/otelstore/internal/store"
	"google.golang.org/protobuf/proto"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// maxBodyBytes caps an OTLP HTTP ingest request body to bound memory per
// request (defense against an unbounded POST exhausting the heap).
const maxBodyBytes = 64 << 20 // 64 MiB

// Handler handles OTLP HTTP ingest endpoints.
type Handler struct {
	store *store.Store
}

// NewHandler creates a new OTLP HTTP handler.
func NewHandler(s *store.Store) *Handler {
	return &Handler{store: s}
}

// readBody reads the request body with a hard size cap. On overflow,
// http.MaxBytesReader makes io.ReadAll return an error, which the caller maps to
// 400.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	return io.ReadAll(r.Body)
}

// ServeHTTP routes requests to the appropriate handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/traces":
		h.handleTraces(w, r)
	case "/v1/logs":
		h.handleLogs(w, r)
	case "/v1/metrics":
		h.handleMetrics(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleTraces handles POST /v1/traces
func (h *Handler) handleTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := readBody(w, r)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req collectortracesv1.ExportTraceServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte("unmarshal error")); err != nil {
			// Response write failed; log and continue
			_ = err
		}
		return
	}

	// Walk ResourceSpans -> ScopeSpans -> Span
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			if err := h.store.InsertSpans(r.Context(), ss.Spans, rs.Resource, ss.Scope); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// Return empty ExportTraceServiceResponse
	resp := &collectortracesv1.ExportTraceServiceResponse{}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBytes); err != nil {
		// Response write failed; log and continue
		_ = err
	}
}

// handleLogs handles POST /v1/logs
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := readBody(w, r)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req collectorlogsv1.ExportLogsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte("unmarshal error")); err != nil {
			// Response write failed; log and continue
			_ = err
		}
		return
	}

	// Walk ResourceLogs -> ScopeLogs -> LogRecord
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			if err := h.store.InsertLogs(r.Context(), sl.LogRecords, rl.Resource, sl.Scope); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// Return empty ExportLogsServiceResponse
	resp := &collectorlogsv1.ExportLogsServiceResponse{}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBytes); err != nil {
		// Response write failed; log and continue
		_ = err
	}
}

// handleMetrics handles POST /v1/metrics
func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := readBody(w, r)
	if err != nil {
		http.Error(w, "read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var req collectormetricsv1.ExportMetricsServiceRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte("unmarshal error")); err != nil {
			// Response write failed; log and continue
			_ = err
		}
		return
	}

	// Walk ResourceMetrics -> ScopeMetrics -> Metric
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			if err := h.store.InsertMetrics(r.Context(), sm.Metrics, rm.Resource, sm.Scope); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	// Return empty ExportMetricsServiceResponse
	resp := &collectormetricsv1.ExportMetricsServiceResponse{}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBytes); err != nil {
		// Response write failed; log and continue
		_ = err
	}
}
