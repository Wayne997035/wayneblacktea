package guard

import (
	"encoding/json"
	"regexp"
)

// RedactToolInput scrubs common credential / secret patterns from a JSON
// payload before it is persisted in guard_events.tool_input.
//
// The function:
//  1. Unmarshals the JSON into an interface{} tree.
//  2. Walks the tree and applies regex scrubbing to every string leaf.
//  3. Re-marshals and returns the result.
//
// On any error (malformed JSON, unmarshal failure), the original raw
// payload is returned unchanged — guard is observe-only and the redaction
// layer must never break event capture.
//
// Patterns scrubbed (highest-precedence first):
//
//   - Stripe / GitHub / Slack / AWS API keys (full token replaced with
//     a typed marker).
//   - Bearer JWT and Postgres / MongoDB / MySQL DSNs (user:pass redacted,
//     host preserved so audit trail keeps the operational signal).
//   - Generic "password=" / "api_key=" / "secret=" / "token=" key=value
//     pairs (value redacted, key preserved).
//
// Even imperfect regex is better than zero: an LLM emitting a literal
// `Bearer ey…` JWT into a Bash command will not be persisted in
// plaintext — the audit row records that *some* JWT was emitted, not
// which one.
func RedactToolInput(input json.RawMessage) json.RawMessage {
	if len(input) == 0 {
		return input
	}
	var tree any
	if err := json.Unmarshal(input, &tree); err != nil {
		// Not valid JSON — apply regex to the raw bytes as a fallback so
		// even malformed payloads don't leak credentials.
		return json.RawMessage(redactString(string(input)))
	}
	tree = redactWalk(tree)
	out, err := json.Marshal(tree)
	if err != nil {
		// Unlikely (round-trip), but preserve fail-open contract.
		return input
	}
	return out
}

// redactWalk recursively scrubs string leaves in a JSON-decoded tree.
func redactWalk(v any) any {
	switch t := v.(type) {
	case string:
		return redactString(t)
	case []any:
		for i, item := range t {
			t[i] = redactWalk(item)
		}
		return t
	case map[string]any:
		for k, item := range t {
			t[k] = redactWalk(item)
		}
		return t
	default:
		return v
	}
}

// redactRule is one regex + replacement pair. Compiled once at package init.
type redactRule struct {
	re   *regexp.Regexp
	repl string
}

// redactRules is the ordered list of credential-pattern scrubbers. Order
// matters when a string could match multiple patterns: the more specific
// rules run first so the final replacement is the most informative.
//
//nolint:gochecknoglobals // pre-compiled regex table; immutable after init.
var redactRules = []redactRule{
	// Stripe API keys.
	{regexp.MustCompile(`sk_live_[A-Za-z0-9_-]+`), "[REDACTED:stripe-live-key]"},
	{regexp.MustCompile(`sk_test_[A-Za-z0-9_-]+`), "[REDACTED:stripe-test-key]"},

	// GitHub tokens. The `{36,}` lower bound matches GitHub's documented
	// minimum length; classic PATs are 40 chars, fine-grained are 76+.
	{regexp.MustCompile(`ghp_[A-Za-z0-9]{36,}`), "[REDACTED:github-pat]"},
	{regexp.MustCompile(`gho_[A-Za-z0-9]{36,}`), "[REDACTED:github-oauth]"},

	// Slack tokens.
	{regexp.MustCompile(`xoxb-[A-Za-z0-9-]+`), "[REDACTED:slack-bot-token]"},
	{regexp.MustCompile(`xoxp-[A-Za-z0-9-]+`), "[REDACTED:slack-user-token]"},

	// Bearer JWT (Authorization header).
	// "ey" prefix matches the base64url-encoded JSON header that JWTs
	// always start with ({"alg":...} → eyJ...).
	{regexp.MustCompile(`Bearer ey[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`), "[REDACTED:jwt-bearer]"},

	// Postgres / MongoDB / MySQL DSNs — preserve scheme + host, redact
	// user:pass. The "host" segment ends at the first "/" or whitespace.
	{regexp.MustCompile(`postgres(ql)?://[^:\s]+:[^@\s]+@([^/\s]+)`), "postgres://[REDACTED]:[REDACTED]@$2"},
	{regexp.MustCompile(`mongodb(\+srv)?://[^:\s]+:[^@\s]+@([^/\s]+)`), "mongodb://[REDACTED]:[REDACTED]@$2"},
	{regexp.MustCompile(`mysql://[^:\s]+:[^@\s]+@([^/\s]+)`), "mysql://[REDACTED]:[REDACTED]@$1"},

	// AWS access key ID.
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "[REDACTED:aws-access-key]"},

	// Generic key=value secrets. Case-insensitive on the key; value is
	// captured up to the next whitespace / quote / & / ;.
	// We deliberately keep the key visible so the audit trail shows which
	// kind of secret was emitted.
	{
		regexp.MustCompile(`(?i)(password|api[_-]?key|secret|token)(\s*[=:]\s*)['"]?([^\s'"&;]+)`),
		`$1$2[REDACTED]`,
	},
}

// redactString applies every redactRule in order and returns the scrubbed
// string. Each rule is a global replace, so multiple occurrences of the
// same pattern in one string all get scrubbed.
func redactString(s string) string {
	for _, r := range redactRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
