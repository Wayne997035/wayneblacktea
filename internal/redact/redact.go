// Package redact provides credential-pattern scrubbing for strings shipped
// to upstream LLM providers, log sinks, or persisted fields. The function
// set is intentionally regex-based (vs. structured parsing) so it MUST be
// applied as a defence-in-depth layer alongside structured payload design,
// not as the only credential filter.
//
// Pattern set is derived from backend-security-design.md §3.1 (cross-language
// rule). Even imperfect regex is better than nothing — coverage > precision.
package redact

import "regexp"

// pattern bundles a compiled regex with the placeholder used for matches.
type pattern struct {
	re    *regexp.Regexp
	label string
}

// orderedPatterns lists credential regexes in priority order. More-specific
// patterns (sk_live, ghp_, AKIA…) MUST run before catch-alls (kv-secret,
// dsn) so the resulting placeholder names the actual credential type.
var orderedPatterns = []pattern{
	// Stripe live secret keys
	{regexp.MustCompile(`sk_live_[A-Za-z0-9]+`), "[REDACTED:stripe-key]"},
	// GitHub personal-access tokens
	{regexp.MustCompile(`ghp_[A-Za-z0-9]{30,}`), "[REDACTED:github-token]"},
	// GitHub fine-grained PATs
	{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{60,}`), "[REDACTED:github-token]"},
	// Slack bot tokens
	{regexp.MustCompile(`xoxb-[A-Za-z0-9-]+`), "[REDACTED:slack-token]"},
	// Slack user/app tokens
	{regexp.MustCompile(`xox[apr]-[A-Za-z0-9-]+`), "[REDACTED:slack-token]"},
	// AWS access key IDs
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "[REDACTED:aws-access-key]"},
	// OpenAI / Anthropic-style API keys (sk-...)
	{regexp.MustCompile(`sk-[A-Za-z0-9_\-]{20,}`), "[REDACTED:openai-key]"},
	// Generic Bearer tokens (RFC 6750). Matches "Bearer <token>" anywhere.
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-]+`), "[REDACTED:bearer]"},
	// DSN-style URLs with embedded credentials (postgres://, mongodb://, redis://, mysql://, https://user:pass@…)
	// Match scheme://user:pass@host — the credentials live between :// and @.
	{regexp.MustCompile(`[a-z][a-z0-9+\-.]*://[^:\s/@]+:[^@\s]+@[^/\s]+`), "[REDACTED:dsn]"},
	// Catch-all for KV-style "password=…", "api_key=…", "secret=…", "token=…" assignments.
	// MUST be last because it would otherwise eat narrower matches. The negative
	// lookahead-style guard "[^\[\s'\"]" on the value's first char prevents this
	// pattern from re-redacting already-replaced segments such as
	// "token=[REDACTED:github-token]" — whose value begins with "[".
	{regexp.MustCompile(`(?i)(password|api[_-]?key|secret|token)\s*[=:]\s*['"]?[^\[\s'"]+`), "[REDACTED:kv-secret]"},
}

// ForLLM scrubs known credential patterns from s, replacing each match with a
// labelled placeholder. Empty input returns empty string. Order of patterns
// is fixed and deterministic so the same input always produces the same
// redacted output (useful for snapshot tests + log-based audit).
//
// The function MUST stay cheap (called per-tool-invocation in the MCP
// middleware path); regexes are compiled once at package init.
func ForLLM(s string) string {
	if s == "" {
		return s
	}
	out := s
	for _, p := range orderedPatterns {
		out = p.re.ReplaceAllString(out, p.label)
	}
	return out
}
