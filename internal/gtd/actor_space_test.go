package gtd

import (
	"sort"
	"testing"
	"unicode"
)

// TestAssigneeSpaceChars_EqualsUnicodeIsSpace pins AssigneeSpaceChars to the
// exact set unicode.IsSpace treats as whitespace. The guarded-UPDATE SQL
// binds AssigneeSpaceChars as a trim charset parameter and MUST reject
// exactly the same assignee values RequireAssigneeForInProgress's
// strings.TrimSpace rejects — any drift between the two sets reopens the
// TOCTOU window SEC-196-02 closes for a subset of whitespace runes.
func TestAssigneeSpaceChars_EqualsUnicodeIsSpace(t *testing.T) {
	var want []rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if unicode.IsSpace(r) {
			want = append(want, r)
		}
	}
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })

	got := []rune(AssigneeSpaceChars)
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })

	if len(got) != len(want) {
		t.Fatalf("AssigneeSpaceChars has %d runes, unicode.IsSpace set has %d\ngot:  %U\nwant: %U", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AssigneeSpaceChars diverges from unicode.IsSpace at index %d: got %U, want %U\nfull got:  %U\nfull want: %U",
				i, got[i], want[i], got, want)
		}
	}
}
