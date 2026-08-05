package receiver

import (
	"io"
	"log"
	"mime"
	"net/http"
	"strings"

	"github.com/3rg0n/otelstore/internal/store"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// maxBodyBytes caps an OTLP HTTP ingest request body to bound memory per
// request (defense against an unbounded POST exhausting the heap).
const maxBodyBytes = 64 << 20 // 64 MiB

// Content types for the two OTLP/HTTP encodings. The spec has servers accept
// both on the same port, distinguished only by the request Content-Type.
const (
	contentTypeJSON  = "application/json"
	contentTypeProto = "application/x-protobuf"
)

// Handler handles OTLP HTTP ingest endpoints.
type Handler struct {
	store *store.Store
}

// NewHandler creates a new OTLP HTTP handler.
func NewHandler(s *store.Store) *Handler {
	return &Handler{store: s}
}

// sanitizeForLog strips control characters (notably CR/LF) from a value before
// it is written to a log line, preventing log-injection/forging (CWE-117) via
// client-controlled fields like the request path or Content-Type.
func sanitizeForLog(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, s)
}

// errString renders an error for a log line, tolerating nil (rejections such as
// a bad method carry no underlying error).
func errString(err error) string {
	if err == nil {
		return "<none>"
	}
	return err.Error()
}

// logReject records a non-2xx ingest outcome. A telemetry sink that is up but
// silently refusing every export is the failure mode most in need of a log line
// — "healthy process, empty database" should never be diagnosed by guesswork —
// so no rejection path here is allowed to stay quiet. The success path stays
// silent to avoid a line per export.
func logReject(r *http.Request, status int, stage string, err error, bodyLen int) {
	// #nosec G706 -- every interpolated field passes through sanitizeForLog
	// (strips CR/LF and control chars), neutralizing log injection.
	log.Printf("ingest: rejected %s %s %d %s: content-type=%q len=%d err=%s",
		sanitizeForLog(r.Method), sanitizeForLog(r.URL.Path), status, stage,
		sanitizeForLog(r.Header.Get("Content-Type")), bodyLen,
		sanitizeForLog(errString(err)))
}

// logWriteFailure reports a failed response write. Nothing can be done for the
// request at this point, but the operator should still see it.
func logWriteFailure(r *http.Request, err error) {
	// #nosec G706 -- fields sanitized as in logReject.
	log.Printf("ingest: response write failed for %s %s: %s",
		sanitizeForLog(r.Method), sanitizeForLog(r.URL.Path), sanitizeForLog(errString(err)))
}

// requestIsJSON reports whether the body is OTLP/JSON rather than binary
// protobuf. An absent or unparseable Content-Type falls back to protobuf, which
// is the OTLP default encoding.
func requestIsJSON(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if ct == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mediaType == contentTypeJSON
}

// readBody reads the request body with a hard size cap. On overflow,
// http.MaxBytesReader makes io.ReadAll return an error, which the caller maps to
// 400.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	return io.ReadAll(r.Body)
}

// decodeRequest unmarshals an OTLP export request in whichever encoding the
// client used. JSON decoding discards unknown fields: real exporters emit
// fields newer than our pinned OTLP proto, and a strict unmarshal would turn a
// forward-compatible payload into a 400.
func decodeRequest(r *http.Request, body []byte, msg proto.Message) error {
	if requestIsJSON(r) {
		return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(body, msg)
	}
	return proto.Unmarshal(body, msg)
}

// readAndDecode runs the prologue shared by all three signals: method check,
// bounded body read, and encoding-aware unmarshal. It writes the error response
// and logs the rejection itself; a false return means the caller must return
// immediately.
func (h *Handler) readAndDecode(w http.ResponseWriter, r *http.Request, msg proto.Message) bool {
	if r.Method != http.MethodPost {
		logReject(r, http.StatusMethodNotAllowed, "method", nil, 0)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}

	body, err := readBody(w, r)
	defer r.Body.Close()
	if err != nil {
		logReject(r, http.StatusBadRequest, "read body", err, len(body))
		http.Error(w, "read request body", http.StatusBadRequest)
		return false
	}

	if err := decodeRequest(r, body, msg); err != nil {
		logReject(r, http.StatusBadRequest, "unmarshal", err, len(body))
		w.WriteHeader(http.StatusBadRequest)
		if _, werr := w.Write([]byte("unmarshal error")); werr != nil {
			logWriteFailure(r, werr)
		}
		return false
	}

	return true
}

// writeResponse marshals the empty export response in the request's encoding —
// a JSON client gets a JSON response, not protobuf.
func (h *Handler) writeResponse(w http.ResponseWriter, r *http.Request, msg proto.Message) {
	var (
		respBytes   []byte
		err         error
		contentType string
	)
	if requestIsJSON(r) {
		contentType = contentTypeJSON
		respBytes, err = protojson.Marshal(msg)
	} else {
		contentType = contentTypeProto
		respBytes, err = proto.Marshal(msg)
	}
	if err != nil {
		logReject(r, http.StatusInternalServerError, "marshal response", err, 0)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(respBytes); err != nil {
		logWriteFailure(r, err)
	}
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
		logReject(r, http.StatusNotFound, "unknown route", nil, 0)
		http.NotFound(w, r)
	}
}

// handleTraces handles POST /v1/traces
func (h *Handler) handleTraces(w http.ResponseWriter, r *http.Request) {
	var req collectortracesv1.ExportTraceServiceRequest
	if !h.readAndDecode(w, r, &req) {
		return
	}

	// Walk ResourceSpans -> ScopeSpans -> Span
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			if err := h.store.InsertSpans(r.Context(), ss.Spans, rs.Resource, ss.Scope); err != nil {
				logReject(r, http.StatusInternalServerError, "store insert", err, 0)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	h.writeResponse(w, r, &collectortracesv1.ExportTraceServiceResponse{})
}

// handleLogs handles POST /v1/logs
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	var req collectorlogsv1.ExportLogsServiceRequest
	if !h.readAndDecode(w, r, &req) {
		return
	}

	// Walk ResourceLogs -> ScopeLogs -> LogRecord
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			if err := h.store.InsertLogs(r.Context(), sl.LogRecords, rl.Resource, sl.Scope); err != nil {
				logReject(r, http.StatusInternalServerError, "store insert", err, 0)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	h.writeResponse(w, r, &collectorlogsv1.ExportLogsServiceResponse{})
}

// handleMetrics handles POST /v1/metrics
func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var req collectormetricsv1.ExportMetricsServiceRequest
	if !h.readAndDecode(w, r, &req) {
		return
	}

	// Walk ResourceMetrics -> ScopeMetrics -> Metric
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			if err := h.store.InsertMetrics(r.Context(), sm.Metrics, rm.Resource, sm.Scope); err != nil {
				logReject(r, http.StatusInternalServerError, "store insert", err, 0)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	h.writeResponse(w, r, &collectormetricsv1.ExportMetricsServiceResponse{})
}
