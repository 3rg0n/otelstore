package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/3rg0n/otelstore/internal/redact"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

// --- -db-path validation (THREAT_MODEL #14) -------------------------------

// TestValidateDBPathAccepted covers the paths an operator legitimately passes.
// The point of the negative half below is that adding validation must not break
// these.
func TestValidateDBPathAccepted(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{MemoryPath, MemoryPath},
		{"telemetry.db", "telemetry.db"},
		{"./telemetry.db", "telemetry.db"},                                  // Clean strips "./"
		{"data//telemetry.db", filepath.Join("data", "telemetry.db")},       // Clean collapses separators
		{"data/./telemetry.db", filepath.Join("data", "telemetry.db")},      //
		{"data/sub/../telemetry.db", filepath.Join("data", "telemetry.db")}, // Clean resolves ".."
		{"/var/lib/otelstore/telemetry.db", filepath.Clean("/var/lib/otelstore/telemetry.db")},
		{"../sibling/telemetry.db", filepath.Clean("../sibling/telemetry.db")}, // relative escape is the operator's call
		{"weird name with spaces.db", "weird name with spaces.db"},
		{"file-ish.db", "file-ish.db"},       // "file" only matters as a "file:" scheme
		{"myfile:name.db", "myfile:name.db"}, // a colon that is not a leading scheme
	}
	for _, tc := range cases {
		got, err := validateDBPath(tc.in)
		if err != nil {
			t.Errorf("validateDBPath(%q) returned error %v, want accepted", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("validateDBPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestValidateDBPathRejectsURIForms is the negative boundary: the driver would
// honour pragmas embedded in a URI-form path, so a path that looks like a
// filename but reconfigures the engine must be refused rather than silently
// reinterpreted.
func TestValidateDBPathRejectsURIForms(t *testing.T) {
	uriCases := []string{
		"file:telemetry.db",
		"file:telemetry.db?_pragma=journal_mode(delete)",
		"file:/var/lib/otelstore/telemetry.db",
		"FILE:telemetry.db",             // scheme check is case-insensitive
		"File:telemetry.db",             //
		"file::memory:?cache=shared",    // shared-cache in-memory DB
		"telemetry.db?_pragma=foo(bar)", // query string without a scheme
		"?_pragma=busy_timeout(0)",
	}
	for _, in := range uriCases {
		got, err := validateDBPath(in)
		if err == nil {
			t.Errorf("validateDBPath(%q) = %q, want ErrDBPathURI", in, got)
			continue
		}
		if !errors.Is(err, ErrDBPathURI) {
			t.Errorf("validateDBPath(%q) error = %v, want ErrDBPathURI", in, err)
		}
	}

	// Empty is rejected too, but it is a different failure than a URI: an empty
	// path would otherwise become a driver-defined default.
	if _, err := validateDBPath(""); err == nil {
		t.Error(`validateDBPath("") returned no error, want a rejection`)
	} else if errors.Is(err, ErrDBPathURI) {
		t.Errorf(`validateDBPath("") error = %v, want a distinct "empty" error, not ErrDBPathURI`, err)
	}
}

// TestOpenRejectsURIPath confirms the validation is actually on the Open path —
// a unit test of validateDBPath alone would pass even if Open never called it.
func TestOpenRejectsURIPath(t *testing.T) {
	st, err := Open("file:" + filepath.Join(t.TempDir(), "x.db") + "?_pragma=journal_mode(delete)")
	if err == nil {
		st.Close()
		t.Fatal("Open accepted a URI-form db path, want ErrDBPathURI")
	}
	if !errors.Is(err, ErrDBPathURI) {
		t.Fatalf("Open error = %v, want ErrDBPathURI", err)
	}

	// And the positive counterpart: a plain path in the same directory opens.
	st, err = Open(filepath.Join(t.TempDir(), "plain.db"))
	if err != nil {
		t.Fatalf("Open on a plain path failed: %v", err)
	}
	defer st.Close()
	if err := st.InitSchema(context.Background()); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
}

// --- attribute redaction (THREAT_MODEL #10) -------------------------------

func strAttr(k, v string) *otlpcommonv1.KeyValue {
	return &otlpcommonv1.KeyValue{
		Key:   k,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: v}},
	}
}

// rawAttrs returns the attributes column exactly as persisted, bypassing the
// query layer's JSON decode. Asserting on the stored text is the whole point:
// redaction has to keep the secret out of the database file, not merely out of
// one query's response.
func rawAttrs(t *testing.T, st *Store, table string) []string {
	t.Helper()
	var query string
	switch table {
	case "spans":
		query = "SELECT attributes FROM spans"
	case "logs":
		query = "SELECT attributes FROM logs"
	case "metrics":
		query = "SELECT attributes FROM metrics"
	default:
		t.Fatalf("rawAttrs: unknown table %q", table)
	}
	rows, err := st.db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("rawAttrs %s: %v", table, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("rawAttrs scan: %v", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rawAttrs rows: %v", err)
	}
	return out
}

const secret = "Bearer sk-live-DO-NOT-PERSIST"

// TestRedactionAppliesToEverySignal is the positive case, and the reason
// mergedAttrs exists as a single choke point: all three signals must redact.
// A per-signal implementation would let one path leak.
func TestRedactionAppliesToEverySignal(t *testing.T) {
	ctx := context.Background()
	st, err := Open(MemoryPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	st.SetRedactor(redact.New([]string{"authorization", "gen_ai.prompt*"}))

	span := &otlptracev1.Span{
		TraceId:           []byte{1, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:              "chat",
		StartTimeUnixNano: 1,
		EndTimeUnixNano:   2,
		Attributes: []*otlpcommonv1.KeyValue{
			strAttr("run_id", "R1"),
			strAttr("authorization", secret),
			strAttr("gen_ai.prompt.0.content", "private user text"),
			strAttr("gen_ai.response.text", "kept"),
		},
	}
	if err := st.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil); err != nil {
		t.Fatalf("InsertSpans: %v", err)
	}

	logRec := &otlplogsv1.LogRecord{
		TimeUnixNano: 10,
		Body:         &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: "b"}},
		Attributes: []*otlpcommonv1.KeyValue{
			strAttr("run_id", "R1"),
			strAttr("authorization", secret),
		},
	}
	if err := st.InsertLogs(ctx, []*otlplogsv1.LogRecord{logRec}, nil, nil); err != nil {
		t.Fatalf("InsertLogs: %v", err)
	}

	metric := &otlpmetricsv1.Metric{
		Name: "tokens",
		Data: &otlpmetricsv1.Metric_Gauge{Gauge: &otlpmetricsv1.Gauge{
			DataPoints: []*otlpmetricsv1.NumberDataPoint{{
				TimeUnixNano: 20,
				Value:        &otlpmetricsv1.NumberDataPoint_AsInt{AsInt: 5},
				Attributes: []*otlpcommonv1.KeyValue{
					strAttr("run_id", "R1"),
					strAttr("authorization", secret),
				},
			}},
		}},
	}
	if err := st.InsertMetrics(ctx, []*otlpmetricsv1.Metric{metric}, nil, nil); err != nil {
		t.Fatalf("InsertMetrics: %v", err)
	}

	for _, table := range []string{"spans", "logs", "metrics"} {
		got := rawAttrs(t, st, table)
		if len(got) == 0 {
			t.Fatalf("%s: nothing persisted", table)
		}
		for _, stored := range got {
			if strings.Contains(stored, secret) {
				t.Errorf("%s: the secret reached the database file: %s", table, stored)
			}
			if !strings.Contains(stored, redact.Placeholder) {
				t.Errorf("%s: no %s marker in stored attributes: %s", table, redact.Placeholder, stored)
			}
			// The key must survive; only the value is withheld.
			if !strings.Contains(stored, "authorization") {
				t.Errorf("%s: redaction dropped the key instead of the value: %s", table, stored)
			}
		}
	}

	// Non-matching attributes must be intact, and the prefix rule must not have
	// swallowed a sibling namespace.
	spans, _, err := st.QueryByKey(ctx, "run_id", "R1", 10)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("QueryByKey returned %d spans, want 1", len(spans))
	}
	attrs, ok := spans[0]["attributes"].(map[string]any)
	if !ok {
		t.Fatalf("attributes decoded as %T, want map", spans[0]["attributes"])
	}
	if got := attrs["gen_ai.response.text"]; got != "kept" {
		t.Errorf("attrs[gen_ai.response.text] = %v, want kept (prefix rule over-matched)", got)
	}
	if got := attrs["gen_ai.prompt.0.content"]; got != redact.Placeholder {
		t.Errorf("attrs[gen_ai.prompt.0.content] = %v, want %q", got, redact.Placeholder)
	}
	if got := attrs["run_id"]; got != "R1" {
		t.Errorf("attrs[run_id] = %v, want R1 (redaction touched an unconfigured key)", got)
	}
}

// TestNoRedactorPersistsVerbatim is the negative boundary on the feature itself:
// with no rules configured the store must store attributes unchanged. Redaction
// is opt-in, so a default-config run must not silently lose data.
func TestNoRedactorPersistsVerbatim(t *testing.T) {
	ctx := context.Background()
	st, err := Open(MemoryPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	// Deliberately not calling SetRedactor; also assert the explicit-nil form is
	// safe, since main.go only calls SetRedactor when rules exist.
	st.SetRedactor(nil)

	span := &otlptracev1.Span{
		TraceId:           []byte{2, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:              "chat",
		StartTimeUnixNano: 1,
		EndTimeUnixNano:   2,
		Attributes: []*otlpcommonv1.KeyValue{
			strAttr("run_id", "R2"),
			strAttr("authorization", secret),
		},
	}
	if err := st.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil); err != nil {
		t.Fatalf("InsertSpans: %v", err)
	}

	stored := rawAttrs(t, st, "spans")
	if len(stored) != 1 {
		t.Fatalf("persisted %d rows, want 1", len(stored))
	}
	if !strings.Contains(stored[0], secret) {
		t.Errorf("unconfigured store altered attributes: %s", stored[0])
	}
	if strings.Contains(stored[0], redact.Placeholder) {
		t.Errorf("unconfigured store redacted something: %s", stored[0])
	}
}

// TestRedactionAppliesBeforeColumnPromotion covers the ordering bug this code is
// arranged to avoid: run_id and job_id are copied out of attributes into indexed
// columns, so extracting before redacting would leave the unredacted value in a
// column even though the JSON blob looked clean.
func TestRedactionAppliesBeforeColumnPromotion(t *testing.T) {
	ctx := context.Background()
	st, err := Open(MemoryPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	// An operator who considers the correlation key itself sensitive (it can
	// carry a customer or tenant name) redacts it. Both the column and the JSON
	// must reflect that.
	st.SetRedactor(redact.New([]string{"run_id"}))

	const sensitiveRun = "acme-corp-tenant-42"
	span := &otlptracev1.Span{
		TraceId:           []byte{3, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
		SpanId:            []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:              "chat",
		StartTimeUnixNano: 1,
		EndTimeUnixNano:   2,
		Attributes: []*otlpcommonv1.KeyValue{
			strAttr("run_id", sensitiveRun),
			strAttr("job_id", "J1"),
		},
	}
	if err := st.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil); err != nil {
		t.Fatalf("InsertSpans: %v", err)
	}

	var runCol any
	err = st.db.QueryRowContext(ctx, "SELECT run_id FROM spans").Scan(&runCol)
	if err != nil {
		t.Fatalf("select run_id: %v", err)
	}
	if s, ok := runCol.(string); !ok || s != redact.Placeholder {
		t.Errorf("run_id column = %v (%T), want %q — redaction must run before column promotion",
			runCol, runCol, redact.Placeholder)
	}
	if stored := rawAttrs(t, st, "spans"); strings.Contains(stored[0], sensitiveRun) {
		t.Errorf("sensitive run_id survived in attributes JSON: %s", stored[0])
	}

	// job_id was not configured, so it must still be promoted normally — proving
	// redaction did not break the promotion path wholesale.
	spans, _, err := st.QueryByKey(ctx, "job_id", "J1", 10)
	if err != nil {
		t.Fatalf("QueryByKey job_id: %v", err)
	}
	if len(spans) != 1 {
		t.Errorf("QueryByKey job_id returned %d spans, want 1", len(spans))
	}
}

// --- in-memory pool sizing -------------------------------------------------

// TestConcurrentInsertsOnMemoryStore is a regression test for a bug the gRPC
// concurrency test surfaced: each connection to a plain ":memory:" DSN gets its
// own private database, so the second pooled connection reported
// "no such table: spans". :memory: is the default -db-path, so concurrent ingest
// on a default run failed. Sequential tests cannot catch it — the pool only ever
// hands back one connection.
func TestConcurrentInsertsOnMemoryStore(t *testing.T) {
	ctx := context.Background()
	st, err := Open(MemoryPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	const workers = 16
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func(n int) {
			span := &otlptracev1.Span{
				TraceId:           []byte{9, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
				SpanId:            []byte{byte(n), byte(n >> 8), 0, 0, 0, 0, 0, 1},
				Name:              "s",
				StartTimeUnixNano: uint64(n + 1),
				EndTimeUnixNano:   uint64(n + 2),
				Attributes:        []*otlpcommonv1.KeyValue{strAttr("run_id", "RC")},
			}
			errs <- st.InsertSpans(ctx, []*otlptracev1.Span{span}, nil, nil)
		}(i)
	}
	for i := 0; i < workers; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent InsertSpans failed: %v", err)
		}
	}

	spans, _, err := st.QueryByKey(ctx, "run_id", "RC", 100)
	if err != nil {
		t.Fatalf("QueryByKey: %v", err)
	}
	if len(spans) != workers {
		t.Errorf("stored %d spans, want %d — writes landed in separate in-memory databases", len(spans), workers)
	}
}
