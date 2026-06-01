package ai

import "github.com/Wayne997035/wayneblacktea/internal/redact"

// RedactCredentials replaces common credential patterns with [REDACTED].
// Applied to LLM-emitted atom content before DB persistence (backend-security §3.1).
// Delegates to redact.ForLLM which maintains the full 15-pattern set including
// xox*, github_pat_, JWTs, Google API keys, and KV catch-alls.
func RedactCredentials(s string) string {
	return redact.ForLLM(s)
}
