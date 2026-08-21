package sanitize

import (
	"errors"
	"fmt"
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

// excerptWindowRunes is the number of runes captured on each side of a
// detected tool-call fragment for ValidateNoTagNoise's error message (U2).
// Bounded deliberately: every caller of ValidateNoTagNoise already wraps the
// returned error with the offending field name (e.g.
// fmt.Errorf("log_decision: rationale %w", err)), so echoing back the ENTIRE
// input alongside that name would turn a validation-error message into an
// unbounded reflection of whatever the caller sent — including anything
// past the tag-noise fragment itself. A tight window gives enough context to
// locate the fragment without that (backend-security-design.md §5.4 —
// user-supplied text surfaced in a message must be length-capped).
const excerptWindowRunes = 10

// findFragmentMatch returns the byte-offset range [start,end) of the
// left-most tool-call-serialization fragment matched by any of
// xmlTagRe/paramTagRe/invokeTagRe, and ok=false when none match.
func findFragmentMatch(s string) (start, end int, ok bool) {
	start = -1
	for _, re := range [...]*regexp.Regexp{xmlTagRe, paramTagRe, invokeTagRe} {
		if loc := re.FindStringIndex(s); loc != nil && (start == -1 || loc[0] < start) {
			start, end = loc[0], loc[1]
		}
	}
	return start, end, start != -1
}

// excerptAround returns a bounded rune window of s centred on the byte range
// [start,end) — see excerptWindowRunes for why the window is capped instead
// of returning the whole string.
func excerptAround(s string, start, end int) string {
	runes := []rune(s)
	startRune := len([]rune(s[:start]))
	endRune := startRune + len([]rune(s[start:end]))
	from := startRune - excerptWindowRunes
	if from < 0 {
		from = 0
	}
	to := endRune + excerptWindowRunes
	if to > len(runes) {
		to = len(runes)
	}
	return string(runes[from:to])
}

// ValidateNoTagNoise returns an error wrapping ErrTagNoise (errors.Is-
// compatible via %w) if s contains tool-call fragments, nil otherwise.
// Intended for use at store write boundaries. The error message includes a
// bounded excerpt around the matched fragment (see excerptWindowRunes) so a
// caller-side wrap like fmt.Errorf("log_decision: rationale %w", err) — the
// pattern every existing caller already uses — produces a message with both
// the field name and enough context to locate the offending text, instead
// of just "input contains tool-call serialization fragments" with no way to
// tell where.
func ValidateNoTagNoise(s string) error {
	start, end, ok := findFragmentMatch(s)
	if !ok {
		return nil
	}
	return fmt.Errorf("%w: near %q", ErrTagNoise, excerptAround(s, start, end))
}
