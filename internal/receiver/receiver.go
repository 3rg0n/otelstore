package receiver

import (
	"io"
	"log"
	"mime"
	"net/http"
	"strings"
	"unicode"

	"github.com/3rg0n/otelstore/internal/store"
	rpccode "google.golang.org/genproto/googleapis/rpc/code"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
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
// client-controlled fields like the request path or Content-Type. It also
// strips Unicode line/paragraph separators (U+2028/U+2029) and format
// characters (category Cf, e.g. bidi overrides), which some log viewers and
// aggregators render as a line break or use to disguise a forged line even
// though Go's logger does not.
func sanitizeForLog(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f:
			return '_'
		case unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
			return '_'
		case unicode.Is(unicode.Cf, r):
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
// read is the number of body bytes actually read — not the size the client
// claimed. On an over-cap body it is the 64 MiB cap, so it says how much was
// consumed rather than implying the payload was that size.
func logReject(r *http.Request, status int, stage string, err error, read int) {
	// #nosec G706 -- every interpolated field passes through sanitizeForLog
	// (strips CR/LF, control chars and Unicode line separators), neutralizing
	// log injection.
	log.Printf("ingest: rejected %s %s %d %s: content-type=%q read=%d err=%s",
		sanitizeForLog(r.Method), sanitizeForLog(r.URL.Path), status, stage,
		sanitizeForLog(r.Header.Get("Content-Type")), read,
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

// writeError responds to a failed ingest request. The OTLP/HTTP spec requires
// that 4xx/5xx bodies be a google.rpc.Status message and that the response use
// the same Content-Type as the request, so a plain-text http.Error would be
// non-conformant for both encodings. msg is a fixed, non-attacker-controlled
// string: the underlying error goes to the log (see logReject), not to the
// client, so ingest failures can't be used to probe server internals.
func writeError(w http.ResponseWriter, r *http.Request, status int, code rpccode.Code, msg string) {
	st := &statuspb.Status{Code: int32(code), Message: msg}

	var (
		body        []byte
		err         error
		contentType string
	)
	if requestIsJSON(r) {
		contentType = contentTypeJSON
		body, err = protojson.Marshal(st)
	} else {
		contentType = contentTypeProto
		body, err = proto.Marshal(st)
	}
	if err != nil {
		// Marshaling a two-field Status cannot realistically fail; fall back to
		// a bare status code rather than pretend to send a body.
		logWriteFailure(r, err)
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		logWriteFailure(r, err)
	}
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
// immediately. The returned count is the body bytes read, so a later rejection
// (e.g. a store failure) can log the request size instead of a bare 0.
func (h *Handler) readAndDecode(w http.ResponseWriter, r *http.Request, msg proto.Message) (int, bool) {
	if r.Method != http.MethodPost {
		logReject(r, http.StatusMethodNotAllowed, "method", nil, 0)
		writeError(w, r, http.StatusMethodNotAllowed, rpccode.Code_UNIMPLEMENTED, "method not allowed")
		return 0, false
	}

	body, err := readBody(w, r)
	defer r.Body.Close()
	if err != nil {
		logReject(r, http.StatusBadRequest, "read body", err, len(body))
		writeError(w, r, http.StatusBadRequest, rpccode.Code_INVALID_ARGUMENT, "read request body")
		return len(body), false
	}

	if err := decodeRequest(r, body, msg); err != nil {
		logReject(r, http.StatusBadRequest, "unmarshal", err, len(body))
		writeError(w, r, http.StatusBadRequest, rpccode.Code_INVALID_ARGUMENT, "unmarshal error")
		return len(body), false
	}

	return len(body), true
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
		writeError(w, r, http.StatusInternalServerError, rpccode.Code_INTERNAL, "marshal response")
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
		writeError(w, r, http.StatusNotFound, rpccode.Code_NOT_FOUND, "unknown route")
	}
}

// handleTraces handles POST /v1/traces
func (h *Handler) handleTraces(w http.ResponseWriter, r *http.Request) {
	var req collectortracesv1.ExportTraceServiceRequest
	read, ok := h.readAndDecode(w, r, &req)
	if !ok {
		return
	}

	// Walk ResourceSpans -> ScopeSpans -> Span
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			if err := h.store.InsertSpans(r.Context(), ss.Spans, rs.Resource, ss.Scope); err != nil {
				logReject(r, http.StatusInternalServerError, "store insert", err, read)
				writeError(w, r, http.StatusInternalServerError, rpccode.Code_INTERNAL, "store insert")
				return
			}
		}
	}

	h.writeResponse(w, r, &collectortracesv1.ExportTraceServiceResponse{})
}

// handleLogs handles POST /v1/logs
func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	var req collectorlogsv1.ExportLogsServiceRequest
	read, ok := h.readAndDecode(w, r, &req)
	if !ok {
		return
	}

	// Walk ResourceLogs -> ScopeLogs -> LogRecord
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			if err := h.store.InsertLogs(r.Context(), sl.LogRecords, rl.Resource, sl.Scope); err != nil {
				logReject(r, http.StatusInternalServerError, "store insert", err, read)
				writeError(w, r, http.StatusInternalServerError, rpccode.Code_INTERNAL, "store insert")
				return
			}
		}
	}

	h.writeResponse(w, r, &collectorlogsv1.ExportLogsServiceResponse{})
}

// handleMetrics handles POST /v1/metrics
func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	var req collectormetricsv1.ExportMetricsServiceRequest
	read, ok := h.readAndDecode(w, r, &req)
	if !ok {
		return
	}

	// Walk ResourceMetrics -> ScopeMetrics -> Metric
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			if err := h.store.InsertMetrics(r.Context(), sm.Metrics, rm.Resource, sm.Scope); err != nil {
				logReject(r, http.StatusInternalServerError, "store insert", err, read)
				writeError(w, r, http.StatusInternalServerError, rpccode.Code_INTERNAL, "store insert")
				return
			}
		}
	}

	h.writeResponse(w, r, &collectormetricsv1.ExportMetricsServiceResponse{})
}
