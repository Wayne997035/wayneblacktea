package validator

import "strings"

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
