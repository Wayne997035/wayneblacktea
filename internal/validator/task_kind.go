package validator

import (
	"fmt"
	"strings"
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
// stop a hostile/malformed suggested_kind (no length cap anywhere upstream,
// see backend-security-design.md §2.1/§3.1 — proposal payloads are
// attacker-influenceable LLM tool input) from inflating the warning text
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
	display := kind
	if r := []rune(kind); len(r) > maxKindWarningRunes {
		display = string(r[:maxKindWarningRunes]) + "…(truncated)"
	}
	return KindGeneral, fmt.Sprintf("kind %q is not a valid task kind; falling back to general", display)
}

// CheckKindFields verifies that description contains the per-kind required
// markers. Returns a slice of warning strings; empty means no issues.
// All checks use plain strings.Contains — no backtracking regex.
func CheckKindFields(kind, description string) []string {
	lower := strings.ToLower(description)

	switch kind {
	case "fix-pr":
		var w []string
		if !strings.Contains(lower, "branch:") {
			w = append(w, "fix-pr task: description should contain \"branch:\"")
		}
		if !strings.Contains(lower, "acceptance:") {
			w = append(w, "fix-pr task: description should contain \"acceptance:\"")
		}
		if !hasFileLineRef(description) {
			w = append(w, "fix-pr task: description should contain at least one file:line reference")
		}
		return w

	case "feature":
		var w []string
		if !strings.Contains(lower, "acceptance:") {
			w = append(w, "feature task: description should contain \"acceptance:\"")
		}
		if !strings.Contains(lower, "risk:") {
			w = append(w, "feature task: description should contain \"risk:\"")
		}
		return w

	case "refactor":
		var w []string
		if !strings.Contains(lower, "scope:") {
			w = append(w, "refactor task: description should contain \"scope:\"")
		}
		if !strings.Contains(lower, "non-goals:") {
			w = append(w, "refactor task: description should contain \"non-goals:\"")
		}
		return w

	case "research":
		var w []string
		if !strings.Contains(lower, "question:") {
			w = append(w, "research task: description should contain \"question:\"")
		}
		if !strings.Contains(lower, "success-criteria:") {
			w = append(w, "research task: description should contain \"success-criteria:\"")
		}
		return w

	default:
		// "general", "chore", and any future unknown kinds: no required fields.
		return nil
	}
}
