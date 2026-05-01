package mcp

import (
	"regexp"
	"strings"
)

const (
	// maxTagLen is the maximum byte length for a single tag.
	maxTagLen = 100
	// maxTagCount is the maximum number of tags allowed in one call.
	maxTagCount = 20
	// maxFieldLen is the maximum byte length for text fields (title, context, decision, rationale).
	maxFieldLen = 5000
)

// tagAllowedRe matches only the characters permitted in a tag value.
// Anything outside [\w\-_./] is stripped by sanitizeTags.
var tagAllowedRe = regexp.MustCompile(`[^\w\-_./ ]`)

// scriptTagRe detects a <script …> or </script> string (case-insensitive) in a field value.
var scriptTagRe = regexp.MustCompile(`(?i)<\s*/?script[\s>]`)

// noiseReason is a human-readable explanation returned with -32602 rejections.
type noiseReason = string

// checkField returns a non-empty noiseReason when value violates noise heuristics
// for a named text field. An empty return means the value is acceptable.
func checkField(name, value string) noiseReason {
	if len(value) > maxFieldLen {
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

// checkDecisionNoise validates the four required text fields of log_decision.
// Returns a non-empty noiseReason when any field is noisy, empty string otherwise.
func checkDecisionNoise(title, ctx, decision, rationale string) noiseReason {
	for name, val := range map[string]string{
		"title":     title,
		"context":   ctx,
		"decision":  decision,
		"rationale": rationale,
	} {
		if reason := checkField(name, val); reason != "" {
			return reason
		}
	}
	// Identical decision + rationale signals low-information content.
	if decision != "" && decision == rationale {
		return "decision and rationale are identical (low-information)"
	}
	return ""
}

// checkHandoffNoise validates the text fields of set_session_handoff.
// Returns a non-empty noiseReason when any field is noisy, empty string otherwise.
func checkHandoffNoise(intent, contextSummary string) noiseReason {
	for name, val := range map[string]string{
		"intent":          intent,
		"context_summary": contextSummary,
	} {
		if reason := checkField(name, val); reason != "" {
			return reason
		}
	}
	return ""
}

// sanitizeTags accepts a slice of raw tag strings (already split from a comma-separated
// input) and returns a cleaned slice. Rules applied per tag:
//
//   - Tags longer than maxTagLen bytes are dropped.
//   - Characters outside [\w\-_./] are stripped; if the result is empty the tag is dropped.
//
// If the input slice exceeds maxTagCount entries, a nil slice and a rejection reason are
// returned so the caller can respond with -32602.
func sanitizeTags(raw []string) ([]string, noiseReason) {
	if len(raw) > maxTagCount {
		return nil, "tags array exceeds 20-entry limit"
	}
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.TrimSpace(t)
		if len(t) > maxTagLen {
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
