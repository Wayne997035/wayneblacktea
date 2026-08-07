package validator

import (
	"regexp"
	"strings"
)

const (
	// MaxTagLen is the maximum byte length for a single tag.
	MaxTagLen = 100
	// MaxTagCount is the maximum number of tags allowed in one call.
	MaxTagCount = 20
	// MaxFieldLen is the maximum byte length for text fields (title, context, decision, rationale).
	MaxFieldLen = 5000
)

// tagAllowedRe matches only the characters permitted in a tag value.
// Anything outside [\w\-_./] is stripped by SanitizeTags.
var tagAllowedRe = regexp.MustCompile(`[^\w\-_./ ]`)

// scriptTagRe detects a <script …> or </script> string (case-insensitive) in a field value.
var scriptTagRe = regexp.MustCompile(`(?i)<\s*/?script[\s>]`)

// CheckField returns a non-empty reason when value violates noise heuristics
// for a named text field (byte-length cap, <script> tag, markdown fence).
// An empty return means the value is acceptable. Shared by both the MCP and
// HTTP entry points for log_decision / set_session_handoff / worksession
// title fields — this is the single implementation; callers must not
// duplicate the <script> / fence detection logic.
func CheckField(name, value string) string {
	if len(value) > MaxFieldLen {
		return name + " exceeds 5000-character limit"
	}
	if scriptTagRe.MatchString(value) {
		return name + " contains suspicious injection pattern (<script>)"
	}
	if strings.Contains(value, "```") {
		return name + " contains markdown fence block (possible injection)"
	}
	return ""
}

// CheckDecisionNoise validates the four required text fields of log_decision.
// Returns a non-empty reason when any field is noisy, empty string otherwise.
func CheckDecisionNoise(title, ctx, decision, rationale string) string {
	for name, val := range map[string]string{
		"title":     title,
		"context":   ctx,
		"decision":  decision,
		"rationale": rationale,
	} {
		if reason := CheckField(name, val); reason != "" {
			return reason
		}
	}
	// Identical decision + rationale signals low-information content.
	if decision != "" && decision == rationale {
		return "decision and rationale are identical (low-information)"
	}
	return ""
}

// CheckCommandField rejects strings that contain ASCII control characters
// (< 0x20, excluding tab \t=0x09), carriage return \r, newline \n, or null
// byte \x00. These characters are adversarial in shell command or expected-
// output fields: a prompt-injected agent could store "\ngit push" to make
// the LLM interpret the second line as a separate shell instruction.
//
// Returns a non-empty reason when the value is unsafe, empty string otherwise.
func CheckCommandField(name, value string) string {
	for _, r := range value {
		// Reject newline, carriage return, null byte explicitly for clarity.
		if r == '\n' || r == '\r' || r == 0x00 {
			return name + " must not contain newline, carriage return, or null byte"
		}
		// Reject other ASCII control characters (< 0x20) except tab (0x09).
		if r < 0x20 && r != '\t' {
			return name + " must not contain ASCII control characters"
		}
		// Reject Unicode line separator (U+2028) and paragraph separator (U+2029).
		// Some terminals and log processors treat these as newlines.
		if r == ' ' || r == ' ' {
			return name + " must not contain Unicode line/paragraph separator"
		}
	}
	return ""
}

// CheckHandoffNoise validates the text fields of set_session_handoff.
// Returns a non-empty reason when any field is noisy, empty string otherwise.
func CheckHandoffNoise(intent, contextSummary string) string {
	for name, val := range map[string]string{
		"intent":          intent,
		"context_summary": contextSummary,
	} {
		if reason := CheckField(name, val); reason != "" {
			return reason
		}
	}
	return ""
}

// SanitizeTags accepts a slice of raw tag strings (already split from a comma-separated
// input) and returns a cleaned slice. Rules applied per tag:
//
//   - Tags longer than MaxTagLen bytes are dropped.
//   - Characters outside [\w\-_./] are stripped; if the result is empty the tag is dropped.
//
// If the input slice exceeds MaxTagCount entries, a nil slice and a rejection reason are
// returned so the caller can respond with -32602 (MCP) or 400 (HTTP).
func SanitizeTags(raw []string) ([]string, string) {
	if len(raw) > MaxTagCount {
		return nil, "tags array exceeds 20-entry limit"
	}
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if len(t) > MaxTagLen {
			// Drop tags that are too long — they are almost certainly noise.
			continue
		}
		cleaned := tagAllowedRe.ReplaceAllString(t, "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			out = append(out, cleaned)
		}
	}
	return out, ""
}
