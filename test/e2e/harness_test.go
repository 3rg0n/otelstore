//go:build e2e

// Package e2e drives the COMPILED otelstore binary as a subprocess over real
// sockets — no in-process handlers, no httptest. It verifies that live OTLP
// data (traces, logs, metrics over gRPC and HTTP), sent with auth, actually
// lands and is queryable, and that data survives a restart.
//
// Run with:  go test -tags e2e ./test/e2e/ -v
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	kv1 "go.opentelemetry.io/proto/otlp/common/v1"
	clogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	cmetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	ctracesv1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracesv1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const authToken = "e2e-secret"

// instance is a running otelstore subprocess plus the ports it listens on.
type instance struct {
	proc      *exec.Cmd
	grpcAddr  string
	httpAddr  string
	queryAddr string
	dbPath    string
	logBuf    *bytes.Buffer
}

// freePort asks the OS for an unused TCP port and returns ":<port>".
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer l.Close()
	return fmt.Sprintf(":%d", l.Addr().(*net.TCPAddr).Port)
}

// buildBinary compiles otelstore once into a temp dir (CGO-free) and returns
// the path. Fails the test if the build itself fails.
func buildBinary(t *testing.T) string {
	t.Helper()
	name := "otelstore-e2e"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	if _, err := os.Stat("../../go.work"); err != nil {
		t.Fatalf("expected repo root two levels up: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/otelstore")
	cmd.Dir = "../.." // repo root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build otelstore: %v\n%s", err, out)
	}
	return bin
}

// launch starts the binary on the given ports with auth + a file-backed DB and
// waits until the query port accepts connections. dbPath lets a restart reuse
// the same file.
func launch(t *testing.T, bin, dbPath string) *instance {
	t.Helper()
	inst := &instance{
		grpcAddr:  freePort(t),
		httpAddr:  freePort(t),
		queryAddr: freePort(t),
		dbPath:    dbPath,
		logBuf:    &bytes.Buffer{},
	}
	mcpAddr := freePort(t)

	inst.proc = exec.Command(bin,
		"-db-path", dbPath,
		"-grpc-port", inst.grpcAddr,
		"-ingest-port", inst.httpAddr,
		"-query-port", inst.queryAddr,
		"-mcp-addr", mcpAddr,
		"-auth-token", authToken,
	)
	inst.proc.Stdout = inst.logBuf
	inst.proc.Stderr = inst.logBuf
	if err := inst.proc.Start(); err != nil {
		t.Fatalf("start otelstore: %v", err)
	}

	// Wait until the query port is actually accepting connections.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", "127.0.0.1"+inst.queryAddr, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return inst
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("otelstore did not become ready in time; logs:\n%s", inst.logBuf.String())
	return nil
}

func (inst *instance) stop() {
	if inst.proc != nil && inst.proc.Process != nil {
		_ = inst.proc.Process.Kill()
		_, _ = inst.proc.Process.Wait()
	}
}

// --- OTLP senders (real wire path) ---

func grpcConn(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient("127.0.0.1"+addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc dial: %v", err)
	}
	return conn
}

func authCtx(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func sendTraceGRPC(t *testing.T, conn *grpc.ClientConn, ctx context.Context, traceID []byte, runID, jobID, name string, statusCode tracesv1.Status_StatusCode) error {
	t.Helper()
	cl := ctracesv1.NewTraceServiceClient(conn)
	now := uint64(time.Now().UnixNano())
	_, err := cl.Export(ctx, &ctracesv1.ExportTraceServiceRequest{
		ResourceSpans: []*tracesv1.ResourceSpans{{
			Resource: &resourcev1.Resource{},
			ScopeSpans: []*tracesv1.ScopeSpans{{
				Spans: []*tracesv1.Span{{
					TraceId:           traceID,
					SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
					Name:              name,
					StartTimeUnixNano: now,
					EndTimeUnixNano:   now + 1000,
					Status:            &tracesv1.Status{Code: statusCode},
					Attributes: []*kv1.KeyValue{
						strAttr("run_id", runID),
						strAttr("job_id", jobID),
					},
				}},
			}},
		}},
	})
	return err
}

func sendMetricGRPC(t *testing.T, conn *grpc.ClientConn, ctx context.Context, name string, value float64, jobID string) error {
	t.Helper()
	cl := cmetricsv1.NewMetricsServiceClient(conn)
	_, err := cl.Export(ctx, &cmetricsv1.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricsv1.ResourceMetrics{{
			Resource: &resourcev1.Resource{},
			ScopeMetrics: []*metricsv1.ScopeMetrics{{
				Metrics: []*metricsv1.Metric{{
					Name: name,
					Data: &metricsv1.Metric_Gauge{Gauge: &metricsv1.Gauge{
						DataPoints: []*metricsv1.NumberDataPoint{{
							TimeUnixNano: uint64(time.Now().UnixNano()),
							Value:        &metricsv1.NumberDataPoint_AsDouble{AsDouble: value},
							Attributes:   []*kv1.KeyValue{strAttr("job_id", jobID)},
						}},
					}},
				}},
			}},
		}},
	})
	return err
}

// sendLogHTTP posts an OTLP log over HTTP/protobuf to /v1/logs with a bearer token.
func sendLogHTTP(t *testing.T, httpAddr, token, runID, jobID, body string) (int, error) {
	t.Helper()
	req := &clogsv1.ExportLogsServiceRequest{
		ResourceLogs: []*logsv1.ResourceLogs{{
			Resource: &resourcev1.Resource{},
			ScopeLogs: []*logsv1.ScopeLogs{{
				LogRecords: []*logsv1.LogRecord{{
					TimeUnixNano:   uint64(time.Now().UnixNano()),
					SeverityText:   "INFO",
					SeverityNumber: 9,
					Body:           &kv1.AnyValue{Value: &kv1.AnyValue_StringValue{StringValue: body}},
					Attributes:     []*kv1.KeyValue{strAttr("run_id", runID), strAttr("job_id", jobID)},
				}},
			}},
		}},
	}
	raw, err := proto.Marshal(req)
	if err != nil {
		return 0, err
	}
	httpReq, err := http.NewRequest(http.MethodPost, "http://127.0.0.1"+httpAddr+"/v1/logs", bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func strAttr(k, v string) *kv1.KeyValue {
	return &kv1.KeyValue{Key: k, Value: &kv1.AnyValue{Value: &kv1.AnyValue_StringValue{StringValue: v}}}
}

// --- query helpers ---

type queryResult struct {
	Spans []map[string]any `json:"spans"`
	Logs  []map[string]any `json:"logs"`
}

func getJSON(t *testing.T, url, token string, out any) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("decode %s: %v (body: %s)", url, err, body)
		}
	}
	return resp.StatusCode
}

// eventually retries fn until it returns nil or the deadline passes — export is
// async on the server side, so queries may need a moment.
func eventually(t *testing.T, why string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		if last = fn(); last == nil {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s: not satisfied within timeout: %v", why, last)
}
