package validator

import "unicode"

// MaxBranchNameLen is the maximum rune count allowed for a task's
// branch_name field. Not a git limit (git itself allows much longer names) —
// a UX/DB-hygiene cap shared by the MCP and HTTP entry points.
const MaxBranchNameLen = 255

// ValidateBranchName checks the invariants shared by the MCP (tools_gtd.go)
// and HTTP (gtd_handler.go) layers for a task's branch_name field:
//
//   - Length is counted in runes, not bytes. A 255-character CJK branch name
//     is well within git's own limits but would trip a byte-length check
//     because each character is 2-3 bytes in UTF-8 — counting bytes here
//     previously caused the MCP and HTTP paths to disagree on the same input
//     (sprint 8-7 gap E).
//   - ASCII control characters (< 0x20), DEL (0x7F), and Unicode category C
//     ("Other": Cc control, Cf format like U+200B zero-width space / U+FEFF
//     BOM, Co private-use, Cs surrogate) are rejected outright.
//
// Returns a non-empty, user-facing error message on violation, or an empty
// string when s is acceptable — including the empty string itself; callers
// decide separately whether branch_name is required.
func ValidateBranchName(s string) string {
	if len([]rune(s)) > MaxBranchNameLen {
		return "branch_name must not exceed 255 characters"
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7F || unicode.Is(unicode.C, r) {
			return "branch_name must not contain control characters"
		}
	}
	return ""
}
