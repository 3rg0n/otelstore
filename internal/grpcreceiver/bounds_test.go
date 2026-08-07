package grpcreceiver

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/3rg0n/otelstore/internal/store"
	collectortracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// newTestServer starts NewGRPCServer on a loopback listener and returns its
// address. Driving the real server over a real socket is the point: the bounds
// under test are constructor options, and asserting on constants would pass even
// if NewGRPCServer stopped passing them.
func newTestServer(t *testing.T, authToken string) string {
	t.Helper()

	st, err := store.Open(store.MemoryPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := NewGRPCServer(st, authToken)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() {
		srv.Stop()
		st.Close()
	})
	return lis.Addr().String()
}

func dial(t *testing.T, addr string, opts ...grpc.DialOption) *grpc.ClientConn {
	t.Helper()
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// smallExportReq builds a one-span export. The span ID varies with n because the
// store enforces UNIQUE(trace_id, span_id): a fixed ID would make the second
// export fail on a storage constraint and look like a transport rejection.
func smallExportReq(n int) *collectortracesv1.ExportTraceServiceRequest {
	return &collectortracesv1.ExportTraceServiceRequest{
		ResourceSpans: []*otlptracev1.ResourceSpans{{
			ScopeSpans: []*otlptracev1.ScopeSpans{{
				Spans: []*otlptracev1.Span{{
					TraceId:           []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
					SpanId:            []byte{byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24), 5, 6, 7, 8},
					Name:              "s",
					StartTimeUnixNano: 1,
					EndTimeUnixNano:   2,
					Attributes: []*otlpcommonv1.KeyValue{{
						Key:   "run_id",
						Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "R1"}},
					}},
				}},
			}},
		}},
	}
}

// TestNormalExporterIsNotRejected is the positive boundary on the DoS bounds
// (THREAT_MODEL #15): the limits exist to deny pathological clients, so the
// first thing to prove is that an ordinary OTLP exporter still works. A too-tight
// keepalive enforcement policy would break real exporters with GOAWAY.
func TestNormalExporterIsNotRejected(t *testing.T) {
	addr := newTestServer(t, "")
	// Mimic an OTLP SDK exporter: it keeps one connection with keepalive pings
	// and no permanently-open stream.
	conn := dial(t, addr, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                60 * time.Second,
		Timeout:             20 * time.Second,
		PermitWithoutStream: true,
	}))
	client := collectortracesv1.NewTraceServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Several sequential exports over the same connection — the ordinary case.
	for i := 0; i < 5; i++ {
		if _, err := client.Export(ctx, smallExportReq(i)); err != nil {
			t.Fatalf("export %d failed: %v", i, err)
		}
	}
}

// TestConcurrentExportsWithinStreamLimitSucceed exercises the concurrency bound
// from below: requests up to the cap must all be served. HTTP/2 queues streams
// beyond MaxConcurrentStreams rather than failing them, so the observable
// contract is "everything completes", not "some are rejected" — a test asserting
// rejection above the cap would be testing a behaviour gRPC does not have.
func TestConcurrentExportsWithinStreamLimitSucceed(t *testing.T) {
	addr := newTestServer(t, "")
	conn := dial(t, addr)
	client := collectortracesv1.NewTraceServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Deliberately more in-flight requests than maxConcurrentStreams: the excess
	// must be queued and served, not dropped.
	const inFlight = maxConcurrentStreams + 32
	errs := make(chan error, inFlight)
	for i := 0; i < inFlight; i++ {
		go func(n int) {
			_, err := client.Export(ctx, smallExportReq(n))
			errs <- err
		}(i)
	}
	for i := 0; i < inFlight; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("export %d of %d failed: %v", i, inFlight, err)
		}
	}
}

// TestBoundsAreConfigured pins the values themselves. It is a deliberate
// tripwire, not a tautology: the numbers are a security posture recorded in
// THREAT_MODEL.md, so widening them should require editing a test that says so.
func TestBoundsAreConfigured(t *testing.T) {
	if maxRecvMsgBytes != 64<<20 {
		t.Errorf("maxRecvMsgBytes = %d, want 64 MiB (documented DoS bound)", maxRecvMsgBytes)
	}
	if maxConcurrentStreams == 0 {
		t.Error("maxConcurrentStreams = 0 means unlimited, which is the default this bound exists to replace")
	}
	if maxConnectionIdle <= 0 {
		t.Errorf("maxConnectionIdle = %v; a non-positive value leaves idle connections unbounded", maxConnectionIdle)
	}
	if minClientPingInterval <= 0 {
		t.Errorf("minClientPingInterval = %v; must be positive to bound keepalive floods", minClientPingInterval)
	}
	// A MinTime above a typical client keepalive interval would GOAWAY healthy
	// exporters. The OTel Go exporter default is 30s or higher.
	if minClientPingInterval > 30*time.Second {
		t.Errorf("minClientPingInterval = %v exceeds the common client keepalive interval; real exporters would be disconnected",
			minClientPingInterval)
	}
}

// TestIdleConnectionIsClosed is the negative boundary on MaxConnectionIdle: a
// connection that goes idle must eventually be reclaimed rather than pinned
// forever. maxConnectionIdle is minutes long, so this drives a purpose-built
// server with a short idle window — the assertion is that the option is honoured
// at all, which is what a wrong option name or a dropped ServerParameters would
// break.
func TestIdleConnectionIsClosed(t *testing.T) {
	st, err := store.Open(store.MemoryPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	if err := st.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := grpc.NewServer(grpc.KeepaliveParams(keepalive.ServerParameters{
		MaxConnectionIdle: 200 * time.Millisecond,
	}))
	collectortracesv1.RegisterTraceServiceServer(srv, NewTraceServer(st, ""))
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn := dial(t, lis.Addr().String())
	client := collectortracesv1.NewTraceServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := client.Export(ctx, smallExportReq(0)); err != nil {
		t.Fatalf("initial export: %v", err)
	}

	// Go idle past the window. The server sends GOAWAY; the client transport
	// leaves READY. Waiting for a state change is the observable signal.
	changed := make(chan struct{})
	go func() {
		defer close(changed)
		state := conn.GetState()
		for conn.WaitForStateChange(ctx, state) {
			if s := conn.GetState(); s.String() != "READY" {
				return
			}
			state = conn.GetState()
		}
	}()

	select {
	case <-changed:
		// The idle connection was torn down, as configured.
	case <-time.After(5 * time.Second):
		t.Fatal("idle connection was never reclaimed; MaxConnectionIdle is not in effect")
	}
}
