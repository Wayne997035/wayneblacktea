package mcp

import (
	"strings"
	"testing"
)

// TestSanitizeAuditText verifies the audit-text sanitiser used in
// middleware_discipline.go. It must:
//   - strip ASCII control bytes (< 0x20) — including \r, \n, \x00, ANSI ESC
//   - preserve \t (tab is the documented exception)
//   - cap output at maxRunes (rune count, not byte count, for UTF-8 safety)
//   - pass plain ASCII through unchanged
//   - handle empty strings as a fast path
//
// Backed by backend-security-design.md §5.4 and CWE-117 (log/audit
// injection — control chars in stored audit text can break CLI rendering
// and forge new log lines).
func TestSanitizeAuditText(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		maxRunes int
		want     string
	}{
		{
			name:     "empty string passes through",
			in:       "",
			maxRunes: 128,
			want:     "",
		},
		{
			name:     "plain ASCII passes through unchanged",
			in:       "add_task",
			maxRunes: 128,
			want:     "add_task",
		},
		{
			name:     "plain UTF-8 with multi-byte runes passes through",
			in:       "中文_repo_名稱",
			maxRunes: 128,
			want:     "中文_repo_名稱",
		},
		{
			name:     "tab is preserved",
			in:       "tool\tname",
			maxRunes: 128,
			want:     "tool\tname",
		},
		{
			name:     "newline stripped",
			in:       "line1\nline2",
			maxRunes: 128,
			want:     "line1line2",
		},
		{
			name:     "carriage return stripped",
			in:       "line1\rline2",
			maxRunes: 128,
			want:     "line1line2",
		},
		{
			name:     "null byte stripped",
			in:       "before\x00after",
			maxRunes: 128,
			want:     "beforeafter",
		},
		{
			name:     "ANSI escape sequence stripped (ESC = 0x1b)",
			in:       "before\x1b[31mred\x1b[0mafter",
			maxRunes: 128,
			want:     "before[31mred[0mafter",
		},
		{
			name:     "all common controls stripped at once",
			in:       "a\x00b\x01c\x07d\x0be\x1bf",
			maxRunes: 128,
			want:     "abcdef",
		},
		{
			name:     "tool name >128 runes truncated to exactly 128",
			in:       strings.Repeat("a", 256),
			maxRunes: 128,
			want:     strings.Repeat("a", 128),
		},
		{
			name:     "exactly maxRunes is preserved",
			in:       strings.Repeat("a", 128),
			maxRunes: 128,
			want:     strings.Repeat("a", 128),
		},
		{
			name:     "multi-byte rune cap counts by rune, not byte",
			in:       strings.Repeat("漢", 200), // 600 bytes UTF-8
			maxRunes: 128,
			want:     strings.Repeat("漢", 128),
		},
		{
			name:     "control char + length cap together",
			in:       strings.Repeat("a", 200) + "\n" + strings.Repeat("b", 200),
			maxRunes: 128,
			want:     strings.Repeat("a", 128),
		},
		{
			name:     "repo_name 256-rune cap (control char + length)",
			in:       strings.Repeat("r", 300),
			maxRunes: 256,
			want:     strings.Repeat("r", 256),
		},
		{
			name:     "high-byte runes (≥0x80) preserved (not control chars)",
			in:       "café",
			maxRunes: 128,
			want:     "café",
		},
		{
			name:     "0x7f DEL is NOT stripped (only < 0x20)",
			in:       "before\x7fafter",
			maxRunes: 128,
			want:     "before\x7fafter",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeAuditText(tc.in, tc.maxRunes)
			if got != tc.want {
				t.Errorf("sanitizeAuditText(%q, %d):\n  got  %q\n  want %q", tc.in, tc.maxRunes, got, tc.want)
			}
		})
	}
}

// TestSanitizeAuditText_ConstantsMatchRequirements documents the
// caller-side constants used in middleware_discipline.go. If these change,
// audit log behaviour changes — callers below assert the tier so that any
// future tweak surfaces in the test diff.
func TestSanitizeAuditText_ConstantsMatchRequirements(t *testing.T) {
	if maxToolNameRunes != 128 {
		t.Errorf("maxToolNameRunes drift: want 128, got %d", maxToolNameRunes)
	}
	if maxRepoNameRunes != 256 {
		t.Errorf("maxRepoNameRunes drift: want 256, got %d", maxRepoNameRunes)
	}
}
