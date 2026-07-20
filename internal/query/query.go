package query

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/otel/internal/store"
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
