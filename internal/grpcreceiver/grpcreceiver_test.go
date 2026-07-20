package grpcreceiver

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/otel/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	collectormetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestGRPCExportSpansAndMetrics(t *testing.T) {
	ctx := context.Background()

	// Create in-memory store
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Create gRPC server without auth
	grpcSrv := NewGRPCServer(st, "")

	// Start server on ephemeral port
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			t.Logf("gRPC server error: %v", err)
		}
	}()
	defer grpcSrv.GracefulStop()

	// Connect client
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	// Test exporting a span via gRPC
	traceClient := collectortracesv1.NewTraceServiceClient(conn)
	traceReq := &collectortracesv1.ExportTraceServiceRequest{
		ResourceSpans: []*otlptracev1.ResourceSpans{
			{
				Resource: nil,
				ScopeSpans: []*otlptracev1.ScopeSpans{
					{
						Scope: nil,
						Spans: []*otlptracev1.Span{
							{
								TraceId:           []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
								SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
								Name:              "test-span",
								StartTimeUnixNano: 1000000000,
								EndTimeUnixNano:   2000000000,
								Attributes: []*otlpcommonv1.KeyValue{
									{
										Key: "run_id",
										Value: &otlpcommonv1.AnyValue{
											Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	traceResp, err := traceClient.Export(ctx, traceReq)
	if err != nil {
		t.Fatalf("failed to export traces: %v", err)
	}
	if traceResp == nil {
		t.Errorf("expected non-nil response, got nil")
	}

	// Verify span was inserted
	spans, _, err := st.QueryByKey(ctx, "trace_id", "000102030405060708090a0b0c0d0e0f", 1000)
	if err != nil {
		t.Fatalf("failed to query spans: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}

	// Test exporting a metric via gRPC
	metricsClient := collectormetricsv1.NewMetricsServiceClient(conn)
	metricsReq := &collectormetricsv1.ExportMetricsServiceRequest{
		ResourceMetrics: []*otlpmetricsv1.ResourceMetrics{
			{
				Resource: nil,
				ScopeMetrics: []*otlpmetricsv1.ScopeMetrics{
					{
						Scope: nil,
						Metrics: []*otlpmetricsv1.Metric{
							{
								Name: "cpu_usage",
								Data: &otlpmetricsv1.Metric_Gauge{
									Gauge: &otlpmetricsv1.Gauge{
										DataPoints: []*otlpmetricsv1.NumberDataPoint{
											{
												TimeUnixNano: 1000000000,
												Value: &otlpmetricsv1.NumberDataPoint_AsDouble{
													AsDouble: 85.5,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	metricsResp, err := metricsClient.Export(ctx, metricsReq)
	if err != nil {
		t.Fatalf("failed to export metrics: %v", err)
	}
	if metricsResp == nil {
		t.Errorf("expected non-nil response, got nil")
	}

	// Verify metric was inserted
	metrics, err := st.QueryMetrics(ctx, "cpu_usage", 1000)
	if err != nil {
		t.Fatalf("failed to query metrics: %v", err)
	}
	if len(metrics) != 1 {
		t.Errorf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0]["value_double"] != 85.5 {
		t.Errorf("expected value_double 85.5, got %v", metrics[0]["value_double"])
	}
}

func TestGRPCAuthRequired(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer st.Close()

	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}

	// Create gRPC server WITH auth
	authToken := "secret-token-123"
	grpcSrv := NewGRPCServer(st, authToken)

	_ = authToken // Use authToken to fix unused warning if needed

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	go func() {
		if err := grpcSrv.Serve(lis); err != nil {
			t.Logf("gRPC server error: %v", err)
		}
	}()
	defer grpcSrv.GracefulStop()

	// Connect client without auth
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	defer conn.Close()

	traceClient := collectortracesv1.NewTraceServiceClient(conn)
	traceReq := &collectortracesv1.ExportTraceServiceRequest{}

	// Should fail without auth header
	_, err = traceClient.Export(ctx, traceReq)
	if err == nil {
		t.Errorf("expected error when no auth header provided, got nil")
	}

	// Retry with correct auth header
	ctxWithAuth := metadata.AppendToOutgoingContext(ctx, "authorization", fmt.Sprintf("Bearer %s", authToken))
	traceResp, err := traceClient.Export(ctxWithAuth, traceReq)
	if err != nil {
		t.Errorf("expected success with correct auth, got error: %v", err)
	}
	if traceResp == nil {
		t.Errorf("expected non-nil response")
	}

	// Retry with incorrect token
	ctxWithBadAuth := metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer wrong-token")
	_, err = traceClient.Export(ctxWithBadAuth, traceReq)
	if err == nil {
		t.Errorf("expected error with wrong token, got nil")
	}
}

// Token comparison is now covered by internal/auth (auth.TokenValid); the
// duplicate helper and its test were removed when the gRPC interceptors were
// switched to the shared, prefix-validating implementation.
