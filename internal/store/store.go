package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"

	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Store is a SQLite-backed OTLP telemetry store.
type Store struct {
	db *sql.DB
}

// Open opens or creates a SQLite database at the given path.
// If path is ":memory:", an in-memory database is used.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			// Close failed; log and continue with original error
			_ = closeErr
		}
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Store{db: db}, nil
}

// InitSchema creates the spans and logs tables.
func (s *Store) InitSchema(ctx context.Context) error {
	spansSchema := `
	CREATE TABLE IF NOT EXISTS spans (
		trace_id TEXT NOT NULL,
		span_id TEXT NOT NULL,
		parent_span_id TEXT,
		name TEXT NOT NULL,
		kind INTEGER DEFAULT 0,
		start_ns INTEGER NOT NULL,
		end_ns INTEGER NOT NULL,
		status_code INTEGER DEFAULT 0,
		status_message TEXT,
		run_id TEXT,
		job_id TEXT,
		attributes TEXT,
		events TEXT,
		PRIMARY KEY (trace_id, span_id)
	);
	CREATE INDEX IF NOT EXISTS idx_spans_trace_id ON spans(trace_id);
	CREATE INDEX IF NOT EXISTS idx_spans_run_id ON spans(run_id);
	CREATE INDEX IF NOT EXISTS idx_spans_job_id ON spans(job_id);
	`

	logsSchema := `
	CREATE TABLE IF NOT EXISTS logs (
		trace_id TEXT,
		span_id TEXT,
		time_ns INTEGER NOT NULL,
		severity_number INTEGER,
		severity_text TEXT,
		body TEXT,
		run_id TEXT,
		job_id TEXT,
		attributes TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_logs_run_id ON logs(run_id);
	CREATE INDEX IF NOT EXISTS idx_logs_job_id ON logs(job_id);
	CREATE INDEX IF NOT EXISTS idx_logs_trace_id ON logs(trace_id);
	`

	if _, err := s.db.ExecContext(ctx, spansSchema); err != nil {
		return fmt.Errorf("failed to create spans table: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, logsSchema); err != nil {
		return fmt.Errorf("failed to create logs table: %w", err)
	}

	return nil
}

// convertAnyValue converts a protobuf AnyValue to a Go value.
// Handles string, bool, int64, double types; defaults to string for unknown types.
func convertAnyValue(av *otlpcommonv1.AnyValue) any {
	if av == nil {
		return nil
	}

	switch v := av.Value.(type) {
	case *otlpcommonv1.AnyValue_StringValue:
		return v.StringValue
	case *otlpcommonv1.AnyValue_BoolValue:
		return v.BoolValue
	case *otlpcommonv1.AnyValue_IntValue:
		return v.IntValue
	case *otlpcommonv1.AnyValue_DoubleValue:
		return v.DoubleValue
	case *otlpcommonv1.AnyValue_ArrayValue:
		if v.ArrayValue == nil {
			return []any{}
		}
		result := make([]any, len(v.ArrayValue.Values))
		for i, val := range v.ArrayValue.Values {
			result[i] = convertAnyValue(val)
		}
		return result
	case *otlpcommonv1.AnyValue_KvlistValue:
		if v.KvlistValue == nil {
			return map[string]any{}
		}
		result := make(map[string]any)
		for _, kv := range v.KvlistValue.Values {
			result[kv.Key] = convertAnyValue(kv.Value)
		}
		return result
	default:
		return fmt.Sprintf("%v", v)
	}
}

// mergeAttributes merges resource, scope, and span attributes.
// Span attributes take precedence, then scope, then resource.
func mergeAttributes(
	resource *otlpresourcev1.Resource,
	scope *otlpcommonv1.InstrumentationScope,
	spanAttrs []*otlpcommonv1.KeyValue,
) map[string]any {
	merged := make(map[string]any)

	// Start with resource attributes
	if resource != nil && resource.Attributes != nil {
		for _, kv := range resource.Attributes {
			merged[kv.Key] = convertAnyValue(kv.Value)
		}
	}

	// Merge scope attributes (override resource)
	if scope != nil && scope.Attributes != nil {
		for _, kv := range scope.Attributes {
			merged[kv.Key] = convertAnyValue(kv.Value)
		}
	}

	// Merge span attributes (override both)
	for _, kv := range spanAttrs {
		merged[kv.Key] = convertAnyValue(kv.Value)
	}

	return merged
}

// extractMetadata extracts run_id and job_id from attributes.
func extractMetadata(attrs map[string]any) (runID, jobID string) {
	if v, ok := attrs["run_id"]; ok {
		if s, ok := v.(string); ok {
			runID = s
		}
	}
	if v, ok := attrs["job_id"]; ok {
		if s, ok := v.(string); ok {
			jobID = s
		}
	}
	return runID, jobID
}

// InsertSpans inserts OTLP spans into the store.
func (s *Store) InsertSpans(
	ctx context.Context,
	spans []*otlptracev1.Span,
	resource *otlpresourcev1.Resource,
	scope *otlpcommonv1.InstrumentationScope,
) error {
	if len(spans) == 0 {
		return nil
	}

	stmt, err := s.db.PrepareContext(ctx, `
		INSERT INTO spans (
			trace_id, span_id, parent_span_id, name, kind,
			start_ns, end_ns, status_code, status_message,
			run_id, job_id, attributes, events
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, span := range spans {
		// Merge attributes
		attrs := mergeAttributes(resource, scope, span.Attributes)
		runID, jobID := extractMetadata(attrs)

		// Hex-encode trace/span IDs
		traceID := hex.EncodeToString(span.TraceId)
		spanID := hex.EncodeToString(span.SpanId)
		parentSpanID := ""
		if len(span.ParentSpanId) > 0 {
			parentSpanID = hex.EncodeToString(span.ParentSpanId)
		}

		// Marshal attributes
		attrsJSON, err := json.Marshal(attrs)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		// Marshal events
		var eventsJSON []byte
		if len(span.Events) > 0 {
			events := make([]map[string]any, len(span.Events))
			for i, evt := range span.Events {
				eventAttrs := make(map[string]any)
				for _, kv := range evt.Attributes {
					eventAttrs[kv.Key] = convertAnyValue(kv.Value)
				}
				events[i] = map[string]any{
					"name":       evt.Name,
					"time_ns":    evt.TimeUnixNano,
					"attributes": eventAttrs,
				}
			}
			eventsJSON, _ = json.Marshal(events)
		}

		statusCode := 0
		statusMessage := ""
		if span.Status != nil {
			statusCode = int(span.Status.Code)
			statusMessage = span.Status.Message
		}

		_, err = stmt.ExecContext(ctx,
			traceID, spanID, parentSpanID, span.Name, span.Kind,
			span.StartTimeUnixNano, span.EndTimeUnixNano,
			statusCode, statusMessage,
			runID, jobID, string(attrsJSON), string(eventsJSON),
		)
		if err != nil {
			return fmt.Errorf("failed to insert span: %w", err)
		}
	}

	return nil
}

// InsertLogs inserts OTLP logs into the store.
func (s *Store) InsertLogs(
	ctx context.Context,
	logs []*otplogsv1.LogRecord,
	resource *otlpresourcev1.Resource,
	scope *otlpcommonv1.InstrumentationScope,
) error {
	if len(logs) == 0 {
		return nil
	}

	stmt, err := s.db.PrepareContext(ctx, `
		INSERT INTO logs (
			trace_id, span_id, time_ns, severity_number, severity_text,
			body, run_id, job_id, attributes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, log := range logs {
		// Merge attributes
		attrs := mergeAttributes(resource, scope, log.Attributes)
		runID, jobID := extractMetadata(attrs)

		// Hex-encode trace/span IDs
		traceID := ""
		if len(log.TraceId) > 0 {
			traceID = hex.EncodeToString(log.TraceId)
		}
		spanID := ""
		if len(log.SpanId) > 0 {
			spanID = hex.EncodeToString(log.SpanId)
		}

		// Marshal attributes
		attrsJSON, err := json.Marshal(attrs)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		// Get body text
		body := ""
		if log.Body != nil {
			bodyVal := convertAnyValue(log.Body)
			if s, ok := bodyVal.(string); ok {
				body = s
			}
		}

		_, err = stmt.ExecContext(ctx,
			traceID, spanID, log.TimeUnixNano,
			log.SeverityNumber, log.SeverityText,
			body, runID, jobID, string(attrsJSON),
		)
		if err != nil {
			return fmt.Errorf("failed to insert log: %w", err)
		}
	}

	return nil
}

// QueryByKey queries spans and logs by a specific key.
// key must be one of: trace_id, run_id, job_id
func (s *Store) QueryByKey(
	ctx context.Context,
	key string,
	value string,
	limit int,
) (spans []map[string]any, logs []map[string]any, err error) {

	if limit <= 0 {
		limit = 1000
	}
	if limit > 10000 {
		limit = 10000
	}

	// Query spans using static queries per key (avoids gosec G201)
	var spansQuery string
	switch key {
	case "trace_id":
		spansQuery = "SELECT * FROM spans WHERE trace_id = ? ORDER BY start_ns LIMIT ?"
	case "run_id":
		spansQuery = "SELECT * FROM spans WHERE run_id = ? ORDER BY start_ns LIMIT ?"
	case "job_id":
		spansQuery = "SELECT * FROM spans WHERE job_id = ? ORDER BY start_ns LIMIT ?"
	default:
		return nil, nil, fmt.Errorf("invalid key: %s", key)
	}

	rows, err := s.db.QueryContext(ctx, spansQuery, value, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query spans: %w", err)
	}
	defer rows.Close()

	spans, err = scanSpans(rows)
	if err != nil {
		return nil, nil, err
	}

	// Query logs using static queries per key (avoids gosec G201)
	var logsQuery string
	switch key {
	case "trace_id":
		logsQuery = "SELECT * FROM logs WHERE trace_id = ? ORDER BY time_ns LIMIT ?"
	case "run_id":
		logsQuery = "SELECT * FROM logs WHERE run_id = ? ORDER BY time_ns LIMIT ?"
	case "job_id":
		logsQuery = "SELECT * FROM logs WHERE job_id = ? ORDER BY time_ns LIMIT ?"
	}

	rows, err = s.db.QueryContext(ctx, logsQuery, value, limit)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query logs: %w", err)
	}
	defer rows.Close()

	logs, err = scanLogs(rows)
	if err != nil {
		return nil, nil, err
	}

	return spans, logs, nil
}

// GetTrace retrieves all spans for a given trace ID.
func (s *Store) GetTrace(
	ctx context.Context,
	traceID string,
) (spans []map[string]any, err error) {
	query := "SELECT * FROM spans WHERE trace_id = ? ORDER BY start_ns"
	rows, err := s.db.QueryContext(ctx, query, traceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query trace: %w", err)
	}
	defer rows.Close()

	return scanSpans(rows)
}

// scanSpans scans rows from a spans query into maps.
func scanSpans(rows *sql.Rows) ([]map[string]any, error) {
	results := make([]map[string]any, 0)

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	for rows.Next() {
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range cols {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		entry := make(map[string]any)
		for i, col := range cols {
			var v any
			val := values[i]
			b, ok := val.([]byte)
			if ok {
				v = string(b)
			} else {
				v = val
			}
			entry[col] = v
		}

		// Decode JSON attributes and events
		if attrsStr, ok := entry["attributes"].(string); ok && attrsStr != "" {
			var attrs map[string]any
			if err := json.Unmarshal([]byte(attrsStr), &attrs); err == nil {
				entry["attributes"] = attrs
			}
		}

		if eventsStr, ok := entry["events"].(string); ok && eventsStr != "" {
			var events []map[string]any
			if err := json.Unmarshal([]byte(eventsStr), &events); err == nil {
				entry["events"] = events
			}
		}

		results = append(results, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return results, nil
}

// scanLogs scans rows from a logs query into maps.
func scanLogs(rows *sql.Rows) ([]map[string]any, error) {
	results := make([]map[string]any, 0)

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	for rows.Next() {
		values := make([]any, len(cols))
		valuePtrs := make([]any, len(cols))
		for i := range cols {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		entry := make(map[string]any)
		for i, col := range cols {
			var v any
			val := values[i]
			b, ok := val.([]byte)
			if ok {
				v = string(b)
			} else {
				v = val
			}
			entry[col] = v
		}

		// Decode JSON attributes
		if attrsStr, ok := entry["attributes"].(string); ok && attrsStr != "" {
			var attrs map[string]any
			if err := json.Unmarshal([]byte(attrsStr), &attrs); err == nil {
				entry["attributes"] = attrs
			}
		}

		results = append(results, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return results, nil
}

// ErrorSpans filters spans with error status (status_code == 2).
func ErrorSpans(spans []map[string]any) []map[string]any {
	errors := make([]map[string]any, 0)
	for _, span := range spans {
		if statusCode, ok := span["status_code"]; ok {
			if code, ok := statusCode.(int64); ok && code == 2 {
				errors = append(errors, span)
			}
		}
	}
	return errors
}

// Close closes the database connection.
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
