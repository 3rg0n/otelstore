//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"testing"

	tracesv1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

// TestLiveDataLandsAndIsQueryable is the core acceptance test: send every
// signal over the wire (with auth) to the running binary, then query each back.
func TestLiveDataLandsAndIsQueryable(t *testing.T) {
	bin := buildBinary(t)
	inst := launch(t, bin, dbFile(t))
	defer inst.stop()

	conn := grpcConn(t, inst.grpcAddr)
	defer conn.Close()
	ctx := authCtx(authToken)

	const runID, jobID = "RUN-e2e", "JOB-e2e"
	traceID := []byte{0xAA, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	traceHex := "aa0102030405060708090a0b0c0d0e0f"

	// 1. trace over gRPC (error status, so we can assert it surfaces)
	if err := sendTraceGRPC(t, conn, ctx, traceID, runID, jobID, "execute_tool", tracesv1.Status_STATUS_CODE_ERROR); err != nil {
		t.Fatalf("send trace over gRPC: %v", err)
	}
	// 2. metric over gRPC
	if err := sendMetricGRPC(t, conn, ctx, "claude_code.cost.usage", 1.23, jobID); err != nil {
		t.Fatalf("send metric over gRPC: %v", err)
	}
	// 3. log over HTTP
	code, err := sendLogHTTP(t, inst.httpAddr, authToken, runID, jobID, "tool starting")
	if err != nil || code != http.StatusOK {
		t.Fatalf("send log over HTTP: code=%d err=%v", code, err)
	}

	base := "http://" + inst.queryAddr

	// trace + correlated log come back under job_id
	eventually(t, "job_id query returns span + log", func() error {
		var qr queryResult
		if c := getJSON(t, base+"/v1/query?job_id="+jobID, authToken, &qr); c != http.StatusOK {
			return fmt.Errorf("status %d", c)
		}
		if len(qr.Spans) < 1 {
			return fmt.Errorf("no spans yet")
		}
		if len(qr.Logs) < 1 {
			return fmt.Errorf("no logs yet")
		}
		// error span must surface with status_code=2
		foundErr := false
		for _, s := range qr.Spans {
			if code, ok := s["status_code"].(float64); ok && int(code) == 2 {
				foundErr = true
			}
			if s["name"] != "execute_tool" {
				return fmt.Errorf("unexpected span name %v", s["name"])
			}
		}
		if !foundErr {
			return fmt.Errorf("error span (status_code=2) not present")
		}
		return nil
	})

	// trace tree by trace_id
	eventually(t, "trace tree query", func() error {
		var tr struct {
			TraceID string           `json:"trace_id"`
			Spans   []map[string]any `json:"spans"`
		}
		if c := getJSON(t, base+"/v1/traces/"+traceHex, authToken, &tr); c != http.StatusOK {
			return fmt.Errorf("status %d", c)
		}
		if len(tr.Spans) < 1 {
			return fmt.Errorf("no spans in trace")
		}
		return nil
	})

	// metric by name
	eventually(t, "metric query", func() error {
		var mr struct {
			Metrics []map[string]any `json:"metrics"`
		}
		if c := getJSON(t, base+"/v1/metrics?name=claude_code.cost.usage", authToken, &mr); c != http.StatusOK {
			return fmt.Errorf("status %d", c)
		}
		if len(mr.Metrics) < 1 {
			return fmt.Errorf("no metrics yet")
		}
		if v, ok := mr.Metrics[0]["value_double"].(float64); !ok || v != 1.23 {
			return fmt.Errorf("metric value = %v, want 1.23", mr.Metrics[0]["value_double"])
		}
		return nil
	})
}

// TestAuthEnforced confirms the running binary rejects unauthenticated queries
// and accepts authenticated ones.
func TestAuthEnforced(t *testing.T) {
	bin := buildBinary(t)
	inst := launch(t, bin, dbFile(t))
	defer inst.stop()

	base := "http://" + inst.queryAddr

	if c := getJSON(t, base+"/v1/query?job_id=x", "", nil); c != http.StatusUnauthorized {
		t.Fatalf("no-token query: got %d, want 401", c)
	}
	if c := getJSON(t, base+"/v1/query?job_id=x", "wrong-token", nil); c != http.StatusUnauthorized {
		t.Fatalf("wrong-token query: got %d, want 401", c)
	}
	if c := getJSON(t, base+"/v1/query?job_id=x", authToken, nil); c != http.StatusOK {
		t.Fatalf("correct-token query: got %d, want 200", c)
	}

	// HTTP ingest must also reject a missing token.
	if code, _ := sendLogHTTP(t, inst.httpAddr, "", "r", "j", "b"); code != http.StatusUnauthorized {
		t.Fatalf("no-token log ingest: got %d, want 401", code)
	}
}

// TestPersistsAcrossRestart proves data written to a file-backed DB survives a
// full process restart — i.e. it's really on disk, not just in memory.
func TestPersistsAcrossRestart(t *testing.T) {
	bin := buildBinary(t)
	db := dbFile(t)

	// First process: ingest a metric, confirm it's queryable, then stop.
	inst1 := launch(t, bin, db)
	conn := grpcConn(t, inst1.grpcAddr)
	ctx := authCtx(authToken)
	if err := sendMetricGRPC(t, conn, ctx, "persist.check", 42, "JOBP"); err != nil {
		t.Fatalf("send metric: %v", err)
	}
	base1 := "http://" + inst1.queryAddr
	eventually(t, "metric visible in first process", func() error {
		var mr struct {
			Metrics []map[string]any `json:"metrics"`
		}
		if c := getJSON(t, base1+"/v1/metrics?name=persist.check", authToken, &mr); c != http.StatusOK {
			return fmt.Errorf("status %d", c)
		}
		if len(mr.Metrics) < 1 {
			return fmt.Errorf("not visible yet")
		}
		return nil
	})
	conn.Close()
	inst1.stop()

	// Second process on the SAME db file: the metric must still be there.
	inst2 := launch(t, bin, db)
	defer inst2.stop()
	base2 := "http://" + inst2.queryAddr
	var mr struct {
		Metrics []map[string]any `json:"metrics"`
	}
	if c := getJSON(t, base2+"/v1/metrics?name=persist.check", authToken, &mr); c != http.StatusOK {
		t.Fatalf("query after restart: got %d", c)
	}
	if len(mr.Metrics) < 1 {
		t.Fatalf("metric did not persist across restart (got 0)")
	}
	if v, ok := mr.Metrics[0]["value_double"].(float64); !ok || v != 42 {
		t.Fatalf("persisted metric value = %v, want 42", mr.Metrics[0]["value_double"])
	}
}

// dbFile returns a unique temp SQLite path for a test.
func dbFile(t *testing.T) string {
	t.Helper()
	return t.TempDir() + "/telemetry.db"
}
