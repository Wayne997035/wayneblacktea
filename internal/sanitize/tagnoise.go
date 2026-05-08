package sanitize

import (
	"errors"
	"regexp"
)

// ErrTagNoise is returned when a string contains tool-call serialization
// fragments (XML tags leaked from Claude Code's MCP serializer). These are
// never valid user input and indicate a caller bug, not user error.
var ErrTagNoise = errors.New("input contains tool-call serialization fragments")

// xmlTagRe matches closing XML tags like </intent>, </context_summary>, </invoke>.
// These appear in corrupted MCP inputs when the harness serializes tool calls
// directly into parameter values instead of proper tool_use content blocks.
var xmlTagRe = regexp.MustCompile(`</[a-z_]+>`)

// paramTagRe matches <parameter name= patterns that appear when tool call
// parameters are serialized as literal XML text.
var paramTagRe = regexp.MustCompile(`<parameter\s+name=`)

// invokeTagRe matches <invoke name= or </invoke> serialization fragments.
var invokeTagRe = regexp.MustCompile(`</?invoke[> ]`)

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
