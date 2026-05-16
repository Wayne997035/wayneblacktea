// Package validator provides input quality checks for GTD entries and related
// user-facing fields. All checks use simple string operations (no complex
// backtracking regexes) to prevent ReDoS on adversarial input.
package validator

import (
	"strings"
	"unicode/utf8"
)

// vaguenessMarkers is the exact-match set (checked case-insensitively after
// strings.ToUpper). Each entry triggers a warning on its own.
var vaguenessMarkers = []string{
	"TBD", "???", "N/A", "TODO", "FIXME", "WIP", "(BLANK)",
}

// vaguenessPhrasesLower is the phrase set (checked case-insensitively via
// strings.Contains on a lower-cased copy of the input). Each entry triggers
// a warning when found anywhere in the text.
var vaguenessPhrasesLower = []string{
	"see audit",
	"see above",
	"see below",
	"見上面",
	"見對話",
	"見 chat",
	"as discussed",
	"various items",
	"several things",
	"multiple issues",
	"更多細節",
	"詳見",
	"詳述",
	"略",
	"等等",
}

// minDescriptionRunes is the minimum rune count below which a text that also
// lacks a file:line reference is flagged as vague.
const minDescriptionRunes = 30

// isFilePathByte reports whether b is a valid byte in a file path segment
// (word characters, dot, dash, slash).
func isFilePathByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_' || b == '.' || b == '-' || b == '/'
}

// extractPathBeforeColon returns the path segment immediately before the colon
// at position colonIdx in text. Returns "" when no usable segment exists.
func extractPathBeforeColon(text string, colonIdx int) string {
	if colonIdx <= 0 {
		return ""
	}
	start := colonIdx - 1
	for start >= 0 && isFilePathByte(text[start]) {
		start--
	}
	start++
	if start >= colonIdx {
		return ""
	}
	return text[start:colonIdx]
}

// hasFileLineRef returns true when text contains a file:line pattern of the
// form word.go:number or word/word:number. Uses only simple byte-scan
// operations — no regex — to prevent ReDoS.
func hasFileLineRef(text string) bool {
	if !strings.ContainsRune(text, ':') {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] != ':' {
			continue
		}
		if i+1 >= len(text) || text[i+1] < '0' || text[i+1] > '9' {
			continue
		}
		seg := extractPathBeforeColon(text, i)
		if seg == "" {
			continue
		}
		if strings.HasSuffix(seg, ".go") || strings.ContainsRune(seg, '/') {
			return true
		}
	}
	return false
}

// CheckVagueness inspects text for vague markers and patterns. field is the
// human-readable field name used in warning messages (e.g. "description").
//
// kind == "chore" skips all checks — chore tasks have no required body structure.
//
// Returns a slice of warning strings; empty means no issues detected.
// All checks use plain string operations (no backtracking regex) to avoid ReDoS.
func CheckVagueness(field, text, kind string) []string {
	if kind == "chore" {
		return nil
	}

	upper := strings.ToUpper(text)
	lower := strings.ToLower(text)
	trimmed := strings.TrimSpace(text)

	var warnings []string

	// Exact-marker check.
	for _, marker := range vaguenessMarkers {
		if strings.Contains(upper, marker) {
			warnings = append(warnings, field+": contains vague marker "+marker)
		}
	}

	// Phrase check (case-insensitive).
	for _, phrase := range vaguenessPhrasesLower {
		if strings.Contains(lower, phrase) {
			warnings = append(warnings, field+": contains vague phrase \""+phrase+"\"")
		}
	}

	// Short + no file:line check.
	if utf8.RuneCountInString(trimmed) < minDescriptionRunes && !hasFileLineRef(text) {
		warnings = append(warnings, field+": too short (< 30 chars) and no file:line reference")
	}

	return warnings
}
