// Package redact implements opt-in redaction of telemetry attribute values.
//
// Telemetry legitimately carries secrets and PII in attribute values —
// Authorization headers, prompts, user identifiers — and otelstore persists
// attributes verbatim as JSON. When the store outlives the incident it was
// opened for, that file becomes the exposure. Redaction lets an operator drop
// the values they know are sensitive at ingest, before anything is written.
//
// The key names live in operator configuration, never in code: otelstore's
// store is deliberately generic and interprets no attribute name (see
// CONTRIBUTING.md). A Redactor is a value the operator constructs; the store
// applies it without knowing what any key means.
package redact

import (
	"strings"
)

// Placeholder replaces a redacted value. A fixed marker rather than dropping the
// key: an operator reading a trace should see that an attribute was present and
// withheld, not be unable to distinguish that from never emitted.
const Placeholder = "[REDACTED]"

// Redactor matches attribute keys against operator-supplied rules. A nil
// *Redactor redacts nothing, so the zero-config path costs nothing.
type Redactor struct {
	exact    map[string]struct{}
	prefixes []string
}

// New builds a Redactor from attribute-key rules. A rule is either an exact key
// ("http.request.header.authorization") or a prefix glob ending in "*"
// ("gen_ai.prompt*"), which matches any key with that prefix. Matching is
// case-insensitive because attribute keys vary in case across SDKs and an
// operator who redacts "Authorization" means "authorization" too.
//
// Empty and whitespace-only rules are ignored. If no usable rule remains, New
// returns nil — the caller can hold the result unconditionally and a nil
// Redactor is a no-op.
func New(rules []string) *Redactor {
	r := &Redactor{exact: make(map[string]struct{})}
	for _, rule := range rules {
		rule = sanitizeRule(strings.ToLower(strings.TrimSpace(rule)))
		if rule == "" {
			continue
		}
		if after, ok := strings.CutSuffix(rule, "*"); ok {
			// A bare "*" would redact every attribute, which is a legitimate
			// (if blunt) request: keep it as the empty prefix, which matches all.
			r.prefixes = append(r.prefixes, after)
			continue
		}
		r.exact[rule] = struct{}{}
	}
	if len(r.exact) == 0 && len(r.prefixes) == 0 {
		return nil
	}
	return r
}

// sanitizeRule strips control characters from a rule. Rules are echoed in the
// startup log line naming what is being redacted, so a rule containing CR/LF
// could forge log entries (CWE-117). Attribute keys have no legitimate use for
// control characters, so replacing them costs nothing. Matching the same way the
// auth and receiver packages sanitize their logged fields.
func sanitizeRule(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, s)
}

// ParseRules splits a comma-separated rule list, as supplied by a command-line
// flag or environment variable.
func ParseRules(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// Match reports whether an attribute key should have its value redacted.
func (r *Redactor) Match(key string) bool {
	if r == nil {
		return false
	}
	k := strings.ToLower(key)
	if _, ok := r.exact[k]; ok {
		return true
	}
	for _, p := range r.prefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// Apply replaces matching values in attrs in place and reports how many were
// redacted. It mutates the caller's map because the caller owns a freshly merged
// attribute map per record; copying it per record would allocate on the hot
// ingest path for no benefit.
//
// Redaction is total, not structural: a matching key's value is replaced whole,
// including a map or slice value, so nothing sensitive survives nested inside
// the JSON that gets persisted.
func (r *Redactor) Apply(attrs map[string]any) int {
	if r == nil || len(attrs) == 0 {
		return 0
	}
	n := 0
	for k := range attrs {
		if r.Match(k) {
			attrs[k] = Placeholder
			n++
		}
	}
	return n
}

// Rules returns the configured rules for logging, so a startup line can state
// what is being redacted. Order is unspecified.
func (r *Redactor) Rules() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.exact)+len(r.prefixes))
	for k := range r.exact {
		out = append(out, k)
	}
	for _, p := range r.prefixes {
		out = append(out, p+"*")
	}
	return out
}
