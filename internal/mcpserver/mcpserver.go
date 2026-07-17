package mcpserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/otel/internal/store"
)

// QueryJobInput is the input for the query_job tool.
type QueryJobInput struct {
	JobID string `json:"job_id"`
}

// QueryJobOutput is the output for the query_job tool.
type QueryJobOutput struct {
	Spans  []map[string]any `json:"spans"`
	Logs   []map[string]any `json:"logs"`
	Errors []map[string]any `json:"errors"`
}

// QueryRunInput is the input for the query_run tool.
type QueryRunInput struct {
	RunID string `json:"run_id"`
}

// QueryRunOutput is the output for the query_run tool.
type QueryRunOutput struct {
	Spans []map[string]any `json:"spans"`
	Logs  []map[string]any `json:"logs"`
}

// GetTraceInput is the input for the get_trace tool.
type GetTraceInput struct {
	TraceID string `json:"trace_id"`
}

// GetTraceOutput is the output for the get_trace tool.
type GetTraceOutput struct {
	TraceID string         `json:"trace_id"`
	Spans   []map[string]any `json:"spans"`
}

// queryJobHandler is the query_job tool logic. It is a named function (not an
// anonymous closure) so tests exercise the SAME code the tool registers —
// preventing a duplicated-logic test that passes while the real tool is broken.
func queryJobHandler(ctx context.Context, s *store.Store, input QueryJobInput) (QueryJobOutput, error) {
	if input.JobID == "" {
		return QueryJobOutput{}, fmt.Errorf("job_id required")
	}

	spans, logs, err := s.QueryByKey(ctx, "job_id", input.JobID, 1000)
	if err != nil {
		return QueryJobOutput{}, err
	}
	if spans == nil {
		spans = []map[string]any{}
	}
	if logs == nil {
		logs = []map[string]any{}
	}

	return QueryJobOutput{
		Spans:  spans,
		Logs:   logs,
		Errors: store.ErrorSpans(spans),
	}, nil
}

// queryRunHandler is the query_run tool logic.
func queryRunHandler(ctx context.Context, s *store.Store, input QueryRunInput) (QueryRunOutput, error) {
	if input.RunID == "" {
		return QueryRunOutput{}, fmt.Errorf("run_id required")
	}

	spans, logs, err := s.QueryByKey(ctx, "run_id", input.RunID, 1000)
	if err != nil {
		return QueryRunOutput{}, err
	}
	if spans == nil {
		spans = []map[string]any{}
	}
	if logs == nil {
		logs = []map[string]any{}
	}

	return QueryRunOutput{Spans: spans, Logs: logs}, nil
}

// getTraceHandler is the get_trace tool logic.
func getTraceHandler(ctx context.Context, s *store.Store, input GetTraceInput) (GetTraceOutput, error) {
	if input.TraceID == "" {
		return GetTraceOutput{}, fmt.Errorf("trace_id required")
	}

	spans, err := s.GetTrace(ctx, input.TraceID)
	if err != nil {
		return GetTraceOutput{}, err
	}
	if spans == nil {
		spans = []map[string]any{}
	}

	return GetTraceOutput{TraceID: input.TraceID, Spans: spans}, nil
}

// NewServer creates a new MCP server with query tools. The tool handlers
// delegate to the named *Handler functions above, which are what tests invoke.
func NewServer(s *store.Store) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "otelstore", Version: "0.1.0"}, nil)

	mcp.AddTool(srv,
		&mcp.Tool{Name: "query_job", Description: "Query spans, logs, and errors for a specific job by job_id"},
		func(ctx context.Context, req *mcp.CallToolRequest, input QueryJobInput) (*mcp.CallToolResult, QueryJobOutput, error) {
			out, err := queryJobHandler(ctx, s, input)
			return nil, out, err
		})

	mcp.AddTool(srv,
		&mcp.Tool{Name: "query_run", Description: "Query spans and logs for a specific run by run_id"},
		func(ctx context.Context, req *mcp.CallToolRequest, input QueryRunInput) (*mcp.CallToolResult, QueryRunOutput, error) {
			out, err := queryRunHandler(ctx, s, input)
			return nil, out, err
		})

	mcp.AddTool(srv,
		&mcp.Tool{Name: "get_trace", Description: "Get all spans for a specific trace by trace_id"},
		func(ctx context.Context, req *mcp.CallToolRequest, input GetTraceInput) (*mcp.CallToolResult, GetTraceOutput, error) {
			out, err := getTraceHandler(ctx, s, input)
			return nil, out, err
		})

	return srv
}

// NewStreamableHTTPHandler creates an HTTP handler for the MCP server.
func NewStreamableHTTPHandler(srv *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return srv
	}, nil)
}
