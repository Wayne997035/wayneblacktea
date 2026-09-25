package validator

import (
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/safetext"
)

// KindGeneral is the default task kind used when none is supplied; centralised
// here so MCP / HTTP handlers don't duplicate the string literal (goconst).
const KindGeneral = "general"

// ValidTaskKinds is the allowlist of accepted task kind values. The CHECK
// constraint in the DB migration (000044) is a secondary defence; this
// allowlist is the primary client-side gate.
var ValidTaskKinds = []string{KindGeneral, "fix-pr", "feature", "refactor", "research", "chore"}

// IsValidKind reports whether kind is a known task kind.
func IsValidKind(kind string) bool {
	for _, k := range ValidTaskKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// maxKindWarningRunes bounds the kind value embedded in ResolveTaskKind's
// warning text. Valid kinds are short tokens (max 7 chars) — this exists to
// stop a hostile/malformed suggested_kind (no length cap anywhere upstream;
// proposal payloads are attacker-influenceable LLM tool input) from
// inflating the warning text
// landing in an HTTP/MCP response. [F0902-54]
const maxKindWarningRunes = 80

// ResolveTaskKind coerces a caller-supplied task kind to a valid one,
// returning a non-empty warning whenever coercion actually changed the
// value (i.e. kind was non-empty but not in ValidTaskKinds). Empty kind
// silently resolves to KindGeneral — that is the expected "no kind
// suggested" case, not an error. [F0902-54]
//
// This is the single shared coercion point for all four TypeTask-accept call
// sites (HTTP single/batch accept, MCP decode, A1-seam decode) — see GTD
// f457740e: previously each site inlined the same 3-branch coercion and
// silently discarded the "invalid" signal, so a rejected kind became
// unobservable to the caller.
func ResolveTaskKind(kind string) (resolved string, warning string) {
	if kind == "" {
		return KindGeneral, ""
	}
	if IsValidKind(kind) {
		return kind, ""
	}
	return KindGeneral, fmt.Sprintf(
		"kind %q is not a valid task kind; falling back to general", kindForWarning(kind),
	)
}

// [GTD a2466b37 / c761ba5c] kindForWarning bounds AND neutralises the caller's
// kind before it is embedded in warning text. suggested_kind is
// attacker-influenceable LLM tool input with no length cap upstream, and the
// warning travels to four different readers — the MCP response, two HTTP
// responses, and the A1-seam decode error — none of which neutralise it
// downstream. Doing it at the single producer is what makes all four sites
// correct at once; the two findings that reported this each proposed fixing
// their own consumer, which would have left the other three open.
//
// Both findings recorded the fix as blocked on an architecture decision,
// because the neutraliser used to be package-private to internal/mcp and this
// package cannot import that. #174 moved it to internal/safetext, which
// imports only "strings" — so the blocker those findings describe no longer
// exists.
//
// clip → neutralise → clip mirrors internal/mcp's clipSafe, and for the same
// two reasons: the first clip keeps the replacement scan off an unbounded
// input, and the second is what makes maxKindWarningRunes a hard bound, since
// a placeholder can be longer than the marker it replaces. The truncation
// suffix is appended last so it cannot itself be clipped away.
func kindForWarning(kind string) string {
	display := kind
	truncated := false
	if r := []rune(kind); len(r) > maxKindWarningRunes {
		display, truncated = string(r[:maxKindWarningRunes]), true
	}
	display = safetext.NeutralizeBoundaryMarkers(display)
	if r := []rune(display); len(r) > maxKindWarningRunes {
		display = string(r[:maxKindWarningRunes])
		truncated = true
	}
	if truncated {
		display += "…(truncated)"
	}
	return display
}

// kindRequiredSections is the per-kind list of sections a description must
// declare, in the order the warnings are emitted — task_input_test.go asserts
// that ValidateTaskInput's output order matches CheckKindFields', so the order
// here is part of the contract. "general" and "chore" require nothing.
//
// A table rather than one switch arm per kind: seven labels checked by seven
// hand-written ifs is how the same judgement key ended up written seven times,
// which is what made [GTD 7fc84288] a seven-place fix instead of a one-place
// one.
var kindRequiredSections = map[string][]string{
	"fix-pr":   {"branch", "acceptance"},
	"feature":  {"acceptance", "risk"},
	"refactor": {"scope", "non-goals"},
	"research": {"question", "success-criteria"},
}

// CheckKindFields verifies that description declares the per-kind required
// sections. Returns a slice of warning strings; empty means no issues.
// All checks are plain string work — no backtracking regex.
func CheckKindFields(kind, description string) []string {
	lower := strings.ToLower(description)

	var w []string
	for _, label := range kindRequiredSections[kind] {
		if !hasKindSection(lower, label) {
			w = append(w, fmt.Sprintf(
				"%s task: description should contain %q or a %q heading",
				kind, label+":", "## "+label))
		}
	}
	if kind == "fix-pr" && !hasFileLineRef(description) {
		w = append(w, "fix-pr task: description should contain at least one file:line reference")
	}
	return w
}

// hasKindSection reports whether a lower-cased description declares a section
// for label, in either form used in practice: the inline "label:" and the
// Markdown heading "## label" (any heading level, colon optional).
//
// [GTD 7fc84288] The judgement key used to be strings.Contains(lower, label+":")
// alone. Every ticket in this repo writes its sections as Markdown headings
// without the colon, so a description with a complete, correctly-written
// acceptance section produced exactly the same warning as one with no
// acceptance criteria at all — measured on a real ticket whose description
// listed five acceptance conditions under "## acceptance". A warning that fires
// on every input stops carrying information, and nothing reports that it has
// stopped. Widening the key is the ruled-on direction; rewriting every
// description to satisfy the checker is not.
//
// A bare mention must still warn, or the signal dies in the other direction
// instead: "this acceptance can wait" has neither a colon nor a heading, so it
// declares nothing. That is the case worth guarding, because it is the one a
// widened matcher is most likely to swallow.
func hasKindSection(lowerDescription, label string) bool {
	if strings.Contains(lowerDescription, label+":") {
		return true
	}
	for _, line := range strings.Split(lowerDescription, "\n") {
		head := strings.TrimSpace(line)
		if !strings.HasPrefix(head, "#") {
			continue
		}
		head = strings.TrimSpace(strings.TrimLeft(head, "#"))
		if head == label {
			return true
		}
		rest, ok := strings.CutPrefix(head, label)
		if !ok || rest == "" {
			continue
		}
		// "## acceptance criteria" declares the section. "## acceptances" does
		// not — the label has to end at a boundary, or a longer word that
		// merely starts with it would count.
		switch rest[0] {
		case ' ', '\t', ':', '-', '(', '/':
			return true
		}
	}
	return false
}
