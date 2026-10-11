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
//
// The "AUTO-CAPTURED FROM ..." entries flag placeholder descriptions
// historically emitted by the auto-capture path when a real description was
// missing. They are a defence-in-depth safety net — the structural fix lives
// in the auto-capture handler; this exists so future regressions cannot
// silently re-introduce blank placeholders.
var vaguenessMarkers = []string{
	"TBD", "???", "N/A", "TODO", "FIXME", "WIP", "(BLANK)",
	"AUTO-CAPTURED FROM MCP",
	"AUTO-CAPTURED FROM ACTIVITY LOG",
}

// vaguenessPhrasesLower is the phrase set (checked case-insensitively via
// strings.Contains on a lower-cased copy of the input). Each entry triggers
// a warning when found anywhere in the text.
//
// The lower-cased "auto-captured from ..." variants are explicit defence in
// depth: even if upstream code drops the uppercase form for some encoding /
// case-flip path, the phrase set still catches the literal placeholder.
//
// The single character 略 is NOT in this set: a substring match flags it
// inside ordinary words (省略 策略 忽略 ...), so hasPlaceholderLue handles it
// with a position-based allowlist scan instead. [F1011-21]
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
	"等等",
	"auto-captured from mcp",
	"auto-captured from activity log",
}

// minDescriptionRunes is the minimum rune count below which a text that also
// lacks a file:line reference is flagged as vague.
const minDescriptionRunes = 30

// lueChar is the elision character the allowlist scan looks for. It has no
// case, so strings.ToLower leaves it unchanged and the scan can run on the
// same lower-cased copy the phrase loop uses. [F1011-21]
const lueChar = "略"

// lueCompounds are ordinary words that contain 略 but are not placeholders.
// A 略 inside one of them is exempt wherever the word occurs, including at the
// end of a clause ("影響可忽略。" is a normal statement). [F1011-21]
//
// Every entry MUST hold exactly one 略 and at least 2 runes: the offset
// arithmetic in lueCovers depends on it. [F1011-22] (TestLueCompounds_Invariants)
var lueCompounds = []string{
	"忽略", "策略", "概略", "粗略", "約略", "大略", "戰略", "領略", "侵略", "攻略",
}

// lueOmitCompounds are words meaning "leave out / skip / shorten". At the end
// of a clause they read as an elision marker ("以下省略"), so their 略 is exempt
// only when more content follows on the same line (see contentFollows). Same
// one-略, 2-rune invariant as lueCompounds. [F1011-21]
var lueOmitCompounds = []string{
	"省略", "略過", "簡略", "縮略",
}

// lueTerminators end a clause when they directly follow an omit-class word.
// The closing brackets ) ] ） ］ 】 are terminators, so "(省略)" is called the
// same way "(略)" is. Quotes, emphasis marks and the other closing brackets are
// lueClosers instead: one that directly follows the word is skipped by
// contentFollows, one that follows whitespace opens the next token and counts
// as content.
const lueTerminators = ",.;:!?)]，。、；：！？…）］】"

// lueClosers are the quote, emphasis and bracket marks that can close a
// wrapped omit-class word ("以下「省略」", "**以下省略**"). See lueTerminators
// for how contentFollows treats them. [PR #200 R1-01]
const lueClosers = "」』”’＂〞»》〉〕}>｝\"'`*_~＊｀"

// contentFollows reports whether real content follows byte offset end of s on
// the same line. Horizontal whitespace is skipped; the text ending, a line
// break or a terminator means nothing follows. A lueClosers mark directly after
// the word is skipped too, so a wrapped clause-final omit word is still called
// (decision ea7e6826: quotes do not exempt); once whitespace has been skipped,
// such a mark opens the next token and counts as content ("略過 *.pb.go").
// It decodes runes instead of indexing bytes: 0xE3 starts both U+3000 and 。,
// so a byte-wise skip would eat half of 。 and report content after "細節略過。".
// [F1011-21][PR #200 R1-01]
func contentFollows(s string, end int) bool {
	sawSpace := false
	for end < len(s) {
		r, size := utf8.DecodeRuneInString(s[end:])
		switch r {
		case ' ', '\t', '\u00a0', '\u3000':
			sawSpace = true
			end += size
		case '\n', '\r':
			return false
		default:
			if strings.ContainsRune(lueClosers, r) {
				if sawSpace {
					return true
				}
				end += size
				continue
			}
			return !strings.ContainsRune(lueTerminators, r)
		}
	}
	return false
}

// lueCovers reports whether word w, positioned so that its 略 sits at byte
// offset pos of lower, occurs there. It returns the byte offset just past w.
func lueCovers(lower string, pos int, w string) (int, bool) {
	start := pos - strings.Index(w, lueChar)
	if start < 0 || !strings.HasPrefix(lower[start:], w) {
		return 0, false
	}
	return start + len(w), true
}

// lueExempt reports whether the 略 at byte offset pos of lower belongs to an
// allowlisted compound. Any one qualifying covering word is enough. [F1011-21]
func lueExempt(lower string, pos int) bool {
	for _, w := range lueCompounds {
		if _, ok := lueCovers(lower, pos, w); ok {
			return true
		}
	}
	for _, w := range lueOmitCompounds {
		if end, ok := lueCovers(lower, pos, w); ok && contentFollows(lower, end) {
			return true
		}
	}
	return false
}

// hasPlaceholderLue reports whether lower holds a 略 outside every exempt
// compound. It scans occurrence by occurrence with strings.Index (no regex,
// no backtracking) and each whitespace run is read by at most one occurrence,
// so the cost is linear in len(lower). It must NOT strip the compounds and
// then search for 略: that is order-dependent and splices ("略省略過" would
// collapse to nothing and lose the placeholder). [F1011-21]
func hasPlaceholderLue(lower string) bool {
	from := 0
	for {
		rel := strings.Index(lower[from:], lueChar)
		if rel < 0 {
			return false
		}
		pos := from + rel
		if !lueExempt(lower, pos) {
			return true
		}
		from = pos + len(lueChar)
	}
}

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

	// 略 check: position-based allowlist scan, one warning per call. [F1011-21]
	if hasPlaceholderLue(lower) {
		warnings = append(warnings, field+": contains vague phrase \"略\"")
	}

	// Short + no file:line check.
	if utf8.RuneCountInString(trimmed) < minDescriptionRunes && !hasFileLineRef(text) {
		warnings = append(warnings, field+": too short (< 30 chars) and no file:line reference")
	}

	return warnings
}
