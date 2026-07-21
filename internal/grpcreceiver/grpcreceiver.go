package grpcreceiver

import (
	"context"

	"github.com/3rg0n/otelstore/internal/auth"
	"github.com/3rg0n/otelstore/internal/store"
	collectorlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TraceServer implements collectortracesv1.TraceServiceServer.
type TraceServer struct {
	store     *store.Store
	authToken string
	collectortracesv1.UnimplementedTraceServiceServer
}

// LogsServer implements collectorlogsv1.LogsServiceServer.
type LogsServer struct {
	store     *store.Store
	authToken string
	collectorlogsv1.UnimplementedLogsServiceServer
}

// MetricsServer implements collectormetricsv1.MetricsServiceServer.
type MetricsServer struct {
	store     *store.Store
	authToken string
	collectormetricsv1.UnimplementedMetricsServiceServer
}

// NewTraceServer creates a trace service server.
func NewTraceServer(s *store.Store, authToken string) *TraceServer {
	return &TraceServer{store: s, authToken: authToken}
}

// NewLogsServer creates a logs service server.
func NewLogsServer(s *store.Store, authToken string) *LogsServer {
	return &LogsServer{store: s, authToken: authToken}
}

// NewMetricsServer creates a metrics service server.
func NewMetricsServer(s *store.Store, authToken string) *MetricsServer {
	return &MetricsServer{store: s, authToken: authToken}
}

// Export implements collectortracesv1.TraceServiceServer.
func (ts *TraceServer) Export(ctx context.Context, req *collectortracesv1.ExportTraceServiceRequest) (*collectortracesv1.ExportTraceServiceResponse, error) {
	if err := checkAuth(ctx, ts.authToken); err != nil {
		return nil, err
	}

	// Walk ResourceSpans -> ScopeSpans -> Span
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			if err := ts.store.InsertSpans(ctx, ss.Spans, rs.Resource, ss.Scope); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to insert spans: %v", err)
			}
		}
	}

	return &collectortracesv1.ExportTraceServiceResponse{}, nil
}

// Export implements collectorlogsv1.LogsServiceServer.
func (ls *LogsServer) Export(ctx context.Context, req *collectorlogsv1.ExportLogsServiceRequest) (*collectorlogsv1.ExportLogsServiceResponse, error) {
	if err := checkAuth(ctx, ls.authToken); err != nil {
		return nil, err
	}

	// Walk ResourceLogs -> ScopeLogs -> LogRecord
	for _, rl := range req.ResourceLogs {
		for _, sl := range rl.ScopeLogs {
			if err := ls.store.InsertLogs(ctx, sl.LogRecords, rl.Resource, sl.Scope); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to insert logs: %v", err)
			}
		}
	}

	return &collectorlogsv1.ExportLogsServiceResponse{}, nil
}

// Export implements collectormetricsv1.MetricsServiceServer.
func (ms *MetricsServer) Export(ctx context.Context, req *collectormetricsv1.ExportMetricsServiceRequest) (*collectormetricsv1.ExportMetricsServiceResponse, error) {
	if err := checkAuth(ctx, ms.authToken); err != nil {
		return nil, err
	}

	// Walk ResourceMetrics -> ScopeMetrics -> Metric
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			if err := ms.store.InsertMetrics(ctx, sm.Metrics, rm.Resource, sm.Scope); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to insert metrics: %v", err)
			}
		}
	}

	return &collectormetricsv1.ExportMetricsServiceResponse{}, nil
}

// checkAuth checks for a valid Authorization header if authToken is set.
func checkAuth(ctx context.Context, authToken string) error {
	// If no auth token required, skip check
	if authToken == "" {
		return nil
	}

	// Get metadata from context
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}

	// Get authorization header
	authHeaders := md.Get("authorization")
	if len(authHeaders) == 0 {
		return status.Error(codes.Unauthenticated, "missing authorization header")
	}

	if !auth.TokenValid(authHeaders[0], authToken) {
		return status.Error(codes.Unauthenticated, "invalid token")
	}

	return nil
}

// NewGRPCServer creates and registers a gRPC server with all OTLP services.
func NewGRPCServer(s *store.Store, authToken string) *grpc.Server {
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(makeAuthInterceptor(authToken)),
	)
	collectortracesv1.RegisterTraceServiceServer(grpcSrv, NewTraceServer(s, authToken))
	collectorlogsv1.RegisterLogsServiceServer(grpcSrv, NewLogsServer(s, authToken))
	collectormetricsv1.RegisterMetricsServiceServer(grpcSrv, NewMetricsServer(s, authToken))
	return grpcSrv
}

// makeAuthInterceptor creates a gRPC UnaryInterceptor that validates auth tokens.
func makeAuthInterceptor(authToken string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if authToken == "" {
			// No auth required
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		authHeaders := md.Get("authorization")
		if len(authHeaders) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		if !auth.TokenValid(authHeaders[0], authToken) {
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		return handler(ctx, req)
	}
}
