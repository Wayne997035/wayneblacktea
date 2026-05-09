package ai

import "regexp"

// credPatterns redacts common credential patterns from text before persisting
// LLM-emitted content. Not a complete defence — a probabilistic supplement to
// the system-prompt instruction. Replace matched spans with [REDACTED].
var credPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_\-]?key)\s*[=:]\s*\S+`),
	regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-._~+/]+=*`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`sk[_\-]live[_\-][A-Za-z0-9]+`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`postgres://[^:]+:[^@]+@`),
	regexp.MustCompile(`mongodb://[^:]+:[^@]+@`),
}

// RedactCredentials replaces common credential patterns with [REDACTED].
// Applied to LLM-emitted atom content before DB persistence (backend-security §3.1).
func RedactCredentials(s string) string {
	for _, re := range credPatterns {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}
