package worksession_test

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
)

// TestCheckControlChars_MatchesValidatorCheckCommandField is the parity test
// for PR #155 security round-2 m-2: worksession.CheckControlChars used to be
// a hand-duplicated 14-line copy of validator.CheckCommandField (byte-for-
// byte identical logic, including the U+2028/U+2029 Unicode line/paragraph
// separator checks) and now delegates to it directly. This test asserts the
// two functions return identical results across the same inputs, so any
// future edit to one that isn't mirrored in the other (there should be no
// "other" anymore, but a regression that reintroduces a local copy) is
// caught here rather than by two silently-diverging implementations.
func TestCheckControlChars_MatchesValidatorCheckCommandField(t *testing.T) {
	cases := []struct {
		name  string
		field string
		value string
	}{
		{"empty_value", "branch_name", ""},
		{"plain_ascii", "branch_name", "feature/action-lifecycle"},
		{"tab_allowed", "verification_command", "cd build\ttask check"},
		{"embedded_newline_rejected", "branch_name", "feature/foo\nrm -rf /"},
		{"embedded_carriage_return_rejected", "command", "cd build\rtask check"},
		{"null_byte_rejected", "artifact", "https://example.com/\x00pwn"},
		{"other_control_char_rejected", "branch_name", "feature/\x01bad"},
		{"unicode_line_separator_rejected", "command", "cd build\u2028task check"},
		{"unicode_paragraph_separator_rejected", "command", "cd build\u2029task check"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := worksession.CheckControlChars(tc.field, tc.value)
			want := validator.CheckCommandField(tc.field, tc.value)
			if got != want {
				t.Errorf("worksession.CheckControlChars(%q, %q) = %q, want (validator.CheckCommandField) %q",
					tc.field, tc.value, got, want)
			}
			// Both must independently agree on error/no-error for this input;
			// a divergence in the reason string alone (not just presence)
			// would still be a real drift the delegation is meant to prevent.
			if (got != "") != (want != "") {
				t.Fatalf("error-presence mismatch for %q: worksession=%q validator=%q", tc.value, got, want)
			}
		})
	}
}
