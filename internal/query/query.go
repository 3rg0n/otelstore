package query

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/3rg0n/otelstore/internal/store"
)

// Handler handles REST query endpoints.
type Handler struct {
	store *store.Store
	mux   *http.ServeMux
}

// NewHandler creates a new query handler.
func NewHandler(s *store.Store) *Handler {
	h := &Handler{store: s, mux: http.NewServeMux()}
	h.mux.HandleFunc("/v1/traces/", h.handleGetTrace)
	h.mux.HandleFunc("/v1/query", h.handleQuery)
	h.mux.HandleFunc("/v1/metrics", h.handleGetMetrics)
	h.mux.HandleFunc("/v1/logs", h.handleGetLogs)
	h.mux.HandleFunc("/healthz", h.handleHealthz)
	h.mux.HandleFunc("/readyz", h.handleReadyz)
	return h
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// handleGetTrace handles GET /v1/traces/{trace_id}
func (h *Handler) handleGetTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract trace_id from path /v1/traces/{trace_id}
	traceID := r.URL.Path[len("/v1/traces/"):]
	if traceID == "" {
		http.Error(w, "trace_id required", http.StatusBadRequest)
		return
	}

	spans, err := h.store.GetTrace(r.Context(), traceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Ensure array is not null when empty
	if spans == nil {
		spans = []map[string]interface{}{}
	}

	result := map[string]interface{}{
		"trace_id": traceID,
		"spans":    spans,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		// Encoding failed; client may have disconnected
		_ = err
	}
}

// handleQuery handles GET /v1/query?job_id=|run_id=|trace_id=&limit=
func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	q := r.URL.Query()
	jobID := q.Get("job_id")
	runID := q.Get("run_id")
	traceID := q.Get("trace_id")

	// Count supplied filters
	filterCount := 0
	if jobID != "" {
		filterCount++
	}
	if runID != "" {
		filterCount++
	}
	if traceID != "" {
		filterCount++
	}

	// Enforce exactly one filter
	if filterCount != 1 {
		http.Error(w, "exactly one of job_id, run_id, or trace_id required", http.StatusBadRequest)
		return
	}

	// Parse limit
	limit := 1000
	if limitStr := q.Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			if l > 0 && l <= 10000 {
				limit = l
			}
		}
	}

	var filterKey, filterValue string
	if jobID != "" {
		filterKey = "job_id"
		filterValue = jobID
	} else if runID != "" {
		filterKey = "run_id"
		filterValue = runID
	} else {
		filterKey = "trace_id"
		filterValue = traceID
	}

	spans, logs, err := h.store.QueryByKey(r.Context(), filterKey, filterValue, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Ensure arrays are not null when empty
	if spans == nil {
		spans = []map[string]interface{}{}
	}
	if logs == nil {
		logs = []map[string]interface{}{}
	}

	result := map[string]interface{}{
		"spans": spans,
		"logs":  logs,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		// Encoding failed; client may have disconnected
		_ = err
	}
}

// handleGetMetrics handles GET /v1/metrics?name=&limit=
func (h *Handler) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	q := r.URL.Query()
	name := q.Get("name")
	if name == "" {
		http.Error(w, "name parameter required", http.StatusBadRequest)
		return
	}

	// Parse limit
	limit := 1000
	if limitStr := q.Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			if l > 0 && l <= 10000 {
				limit = l
			}
		}
	}

	metrics, err := h.store.QueryMetrics(r.Context(), name, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Ensure array is not null when empty
	if metrics == nil {
		metrics = []map[string]interface{}{}
	}

	result := map[string]interface{}{
		"metrics": metrics,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		// Encoding failed; client may have disconnected
		_ = err
	}
}

// handleGetLogs handles GET /v1/logs?event_name=&min_severity=&limit=
// Both filters are optional: no event_name matches all events; min_severity=0
// applies no floor. This is the events query path (OTLP events are logs with an
// event.name).
func (h *Handler) handleGetLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	eventName := q.Get("event_name")

	minSeverity := 0
	if sevStr := q.Get("min_severity"); sevStr != "" {
		if sev, err := strconv.Atoi(sevStr); err == nil && sev > 0 {
			minSeverity = sev
		}
	}

	limit := 1000
	if limitStr := q.Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			if l > 0 && l <= 10000 {
				limit = l
			}
		}
	}

	logs, err := h.store.QueryLogs(r.Context(), eventName, minSeverity, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if logs == nil {
		logs = []map[string]interface{}{}
	}

	result := map[string]interface{}{
		"logs": logs,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(result); err != nil {
		_ = err
	}
}

// handleHealthz is a liveness probe: the process is up.
func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok")); err != nil {
		_ = err
	}
}

// handleReadyz is a readiness probe: the store answers. Returns 503 if not.
func (h *Handler) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Ping(r.Context()); err != nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ready")); err != nil {
		_ = err
	}
}
