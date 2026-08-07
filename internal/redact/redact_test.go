package redact

import (
	"strings"
	"testing"
)

// TestNewReturnsNilForNoUsableRules covers the negative-boundary case that makes
// the zero-config path free: with no rules (or only blank ones) New must return
// nil, and a nil *Redactor must be a safe no-op rather than a panic.
func TestNewReturnsNilForNoUsableRules(t *testing.T) {
	cases := map[string][]string{
		"nil slice":      nil,
		"empty slice":    {},
		"empty string":   {""},
		"whitespace":     {"   ", "\t"},
		"commas only":    ParseRules(",,,"),
		"blank env-ish":  ParseRules("   "),
		"empty then tab": {"", "\t"},
	}

	for name, rules := range cases {
		t.Run(name, func(t *testing.T) {
			r := New(rules)
			if r != nil {
				t.Fatalf("New(%q) = %v, want nil so the no-config path costs nothing", rules, r.Rules())
			}
			// All methods must tolerate the nil receiver.
			if r.Match("anything") {
				t.Error("nil Redactor matched a key")
			}
			attrs := map[string]any{"authorization": "Bearer secret"}
			if n := r.Apply(attrs); n != 0 {
				t.Errorf("nil Redactor redacted %d keys, want 0", n)
			}
			if got := attrs["authorization"]; got != "Bearer secret" {
				t.Errorf("nil Redactor mutated the value to %v", got)
			}
			if rs := r.Rules(); rs != nil {
				t.Errorf("nil Redactor Rules() = %v, want nil", rs)
			}
		})
	}
}

// TestMatchBoundaries pins exactly what does and does not match. The negative
// half matters more than the positive: an over-broad prefix rule would silently
// redact unrelated telemetry, which looks like data loss rather than a config
// error.
func TestMatchBoundaries(t *testing.T) {
	r := New([]string{"authorization", "gen_ai.prompt*", "  api.key  "})
	if r == nil {
		t.Fatal("New returned nil for real rules")
	}

	shouldMatch := []string{
		"authorization",
		"Authorization",          // case-insensitive: SDKs vary
		"AUTHORIZATION",          //
		"gen_ai.prompt",          // prefix rule matches the bare prefix
		"gen_ai.prompt.0.text",   // ...and anything under it
		"GEN_AI.PROMPT.messages", // prefix, case-insensitively
		"api.key",                // rule had surrounding whitespace, trimmed
	}
	for _, k := range shouldMatch {
		if !r.Match(k) {
			t.Errorf("Match(%q) = false, want true", k)
		}
	}

	shouldNotMatch := []string{
		"authorizatio",           // shorter than the exact rule
		"authorizationx",         // exact rule must not behave like a prefix
		"x-authorization",        // exact rule must not behave like a suffix/substring
		"proxy-authorization",    //
		"gen_ai.promp",           // one char short of the prefix
		"gen_ai.response.text",   // sibling under the same namespace
		"gen_ai",                 // parent of the prefix
		"api.keyring",            // exact rule "api.key" must not match a longer key
		"api",                    //
		"run_id",                 // ordinary correlation key
		"http.request.method",    //
		"service.name",           //
		"",                       // empty key
		"prompt",                 // prefix rule is anchored at the start
		"my.gen_ai.prompt.thing", //
	}
	for _, k := range shouldNotMatch {
		if r.Match(k) {
			t.Errorf("Match(%q) = true, want false (over-broad match redacts unrelated telemetry)", k)
		}
	}
}

// TestApplyRedactsValuesAndCountsThem asserts the positive path: matching values
// are replaced with the placeholder, non-matching values are untouched
// byte-for-byte, and the returned count reflects what changed.
func TestApplyRedactsValuesAndCountsThem(t *testing.T) {
	r := New([]string{"authorization", "gen_ai.prompt*"})

	attrs := map[string]any{
		"authorization":       "Bearer sk-live-abc123",
		"gen_ai.prompt.0":     "the user's private prompt",
		"gen_ai.prompt.count": int64(3),
		"gen_ai.response":     "kept",
		"run_id":              "R1",
		"service.name":        "svc",
	}

	n := r.Apply(attrs)
	if n != 3 {
		t.Errorf("Apply redacted %d keys, want 3", n)
	}

	for _, k := range []string{"authorization", "gen_ai.prompt.0", "gen_ai.prompt.count"} {
		if got := attrs[k]; got != Placeholder {
			t.Errorf("attrs[%q] = %v, want %q", k, got, Placeholder)
		}
	}
	// The secret must be gone, not merely shadowed.
	if got := attrs["authorization"]; got == "Bearer sk-live-abc123" {
		t.Error("the secret value survived redaction")
	}
	// Untouched keys must be exactly as supplied.
	if got := attrs["gen_ai.response"]; got != "kept" {
		t.Errorf("attrs[gen_ai.response] = %v, want kept", got)
	}
	if got := attrs["run_id"]; got != "R1" {
		t.Errorf("attrs[run_id] = %v, want R1", got)
	}
	if len(attrs) != 6 {
		t.Errorf("Apply changed the key set (len=%d, want 6); redaction replaces values, it does not drop keys", len(attrs))
	}
}

// TestApplyRedactsStructuredValuesWholly covers the case a naive string-only
// implementation would leak: a matching key whose value is a nested map or slice
// must be replaced entirely, not walked and partially rewritten.
func TestApplyRedactsStructuredValuesWholly(t *testing.T) {
	r := New([]string{"secrets"})

	attrs := map[string]any{
		"secrets": map[string]any{
			"nested": map[string]any{"token": "sk-live-nested"},
			"list":   []any{"sk-live-in-list"},
		},
	}
	if n := r.Apply(attrs); n != 1 {
		t.Fatalf("Apply redacted %d, want 1", n)
	}
	if got := attrs["secrets"]; got != Placeholder {
		t.Fatalf("structured value = %#v, want the whole value replaced with %q", got, Placeholder)
	}
}

// TestApplyOnEmptyAndBareStar covers the two extremes: nothing to do, and a rule
// that deliberately matches everything.
func TestApplyOnEmptyAndBareStar(t *testing.T) {
	t.Run("empty attrs", func(t *testing.T) {
		r := New([]string{"authorization"})
		if n := r.Apply(map[string]any{}); n != 0 {
			t.Errorf("Apply on empty map redacted %d, want 0", n)
		}
		if n := r.Apply(nil); n != 0 {
			t.Errorf("Apply on nil map redacted %d, want 0", n)
		}
	})

	t.Run("bare star redacts every key", func(t *testing.T) {
		r := New([]string{"*"})
		if r == nil {
			t.Fatal(`New(["*"]) = nil; a bare "*" is a blunt but legitimate rule`)
		}
		attrs := map[string]any{"a": "1", "b": "2", "run_id": "R"}
		if n := r.Apply(attrs); n != 3 {
			t.Errorf("Apply redacted %d, want 3", n)
		}
		for k, v := range attrs {
			if v != Placeholder {
				t.Errorf("attrs[%q] = %v, want %q", k, v, Placeholder)
			}
		}
	})
}

// TestParseRules covers the flag-parsing boundary, including the whitespace-only
// input that must not produce a one-element slice of "".
func TestParseRules(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"   ", 0},
		{"\t\n", 0},
		{"authorization", 1},
		{"a,b", 2},
		{"a, b , c", 3},
		{",a,", 3}, // empty elements survive parsing; New drops them
	}
	for _, tc := range cases {
		if got := len(ParseRules(tc.in)); got != tc.want {
			t.Errorf("ParseRules(%q) length = %d, want %d", tc.in, got, tc.want)
		}
	}

	// The end-to-end contract that matters: a blank config yields no redactor.
	if r := New(ParseRules("  ")); r != nil {
		t.Error("blank config produced a non-nil Redactor")
	}
	// And a config of only separators does too.
	if r := New(ParseRules(",,")); r != nil {
		t.Error("separator-only config produced a non-nil Redactor")
	}
}

// TestRulesAreSanitizedForLogging covers the log-injection boundary: rules are
// echoed in the startup line naming what is redacted, so a rule carrying CR/LF
// must not be able to forge a log entry (CWE-117). The rule must still work for
// matching — sanitizing must not silently drop it.
func TestRulesAreSanitizedForLogging(t *testing.T) {
	r := New([]string{"auth\r\n2026/01/01 forged: admin login"})
	if r == nil {
		t.Fatal("New dropped a rule containing control characters instead of sanitizing it")
	}
	for _, rule := range r.Rules() {
		if strings.ContainsAny(rule, "\r\n") {
			t.Errorf("rule %q still contains CR/LF; it would forge log lines", rule)
		}
	}

	// A control character in the incoming key is normalized the same way, so a
	// sanitized rule still matches the key it was written for.
	attrs := map[string]any{"auth\r\n2026/01/01 forged: admin login": "secret"}
	if n := r.Apply(attrs); n != 0 {
		t.Errorf("Apply redacted %d; the raw key differs from the sanitized rule, so no match is expected", n)
	}
	// The sanitized form is what matches.
	sane := map[string]any{"auth__2026/01/01 forged: admin login": "secret"}
	if n := r.Apply(sane); n != 1 {
		t.Errorf("Apply on the sanitized key redacted %d, want 1", n)
	}
}
