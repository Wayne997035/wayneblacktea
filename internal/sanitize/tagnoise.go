package sanitize

import (
	"errors"
	"regexp"
)

// ErrTagNoise is returned when a string contains tool-call serialization
// fragments (XML tags leaked from Claude Code's MCP serializer). These are
// never valid user input and indicate a caller bug, not user error.
var ErrTagNoise = errors.New("input contains tool-call serialization fragments")

// xmlTagRe matches closing tags of known MCP tool-call field names (case-insensitive).
// Using an allowlist prevents false positives on legitimate HTML like </b>, </p>, </a>.
var xmlTagRe = regexp.MustCompile(`(?i)</(?:intent|context_summary|repo_name|rationale|alternatives|invoke|parameter|decision|context)>`)

// paramTagRe matches <parameter name= patterns (case-insensitive).
var paramTagRe = regexp.MustCompile(`(?i)<parameter\s+name=`)

// invokeTagRe matches <invoke...> or </invoke> serialization fragments (case-insensitive).
var invokeTagRe = regexp.MustCompile(`(?i)</?invoke[> ]`)

// ContainsToolCallFragment returns true when s contains known tool-call
// serialization artifacts. Returns ErrTagNoise as a sentinel for callers
// that want to surface a consistent error message.
func ContainsToolCallFragment(s string) bool {
	return xmlTagRe.MatchString(s) ||
		paramTagRe.MatchString(s) ||
		invokeTagRe.MatchString(s)
}

// ValidateNoTagNoise returns ErrTagNoise if s contains tool-call fragments,
// nil otherwise. Intended for use at store write boundaries.
func ValidateNoTagNoise(s string) error {
	if ContainsToolCallFragment(s) {
		return ErrTagNoise
	}
	return nil
}
