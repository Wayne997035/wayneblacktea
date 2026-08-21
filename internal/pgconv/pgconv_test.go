package pgconv

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// [F160-09] Recovered from an untracked worktree (pr160-lane-a) where it was
// written but never landed — ToTextPtr (pgconv.go:28) had zero test coverage
// on the integration branch before this file.

// TestF160_09_ToTextPtr pins ToTextPtr's presence semantics (Ω6,
// 2026-08-20-mcp-surface-spec.md): this is the conversion at the exact
// boundary where "caller omitted the field" and "caller explicitly passed
// an empty string" must NOT collapse into the same pgtype.Text — if they
// did, every Ω6 clobber-on-omit fix built on top of this function would
// come back, silently, because the bad code would still pass every test
// that only exercises the "non-empty value" case.
func TestF160_09_ToTextPtr(t *testing.T) {
	cases := []struct {
		name string
		in   *string
		want pgtype.Text
	}{
		{
			name: "nil pointer maps to NULL (preserve on omission)",
			in:   nil,
			want: pgtype.Text{Valid: false},
		},
		{
			name: "pointer to empty string maps to an explicit, non-NULL empty value",
			in:   strPtrLocal(""),
			want: pgtype.Text{String: "", Valid: true},
		},
		{
			name: "pointer to a non-empty string maps to that value",
			in:   strPtrLocal("go"),
			want: pgtype.Text{String: "go", Valid: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToTextPtr(tc.in)
			if got.Valid != tc.want.Valid {
				t.Errorf("ToTextPtr(%v).Valid = %v, want %v", derefForLog(tc.in), got.Valid, tc.want.Valid)
			}
			if got.String != tc.want.String {
				t.Errorf("ToTextPtr(%v).String = %q, want %q", derefForLog(tc.in), got.String, tc.want.String)
			}
		})
	}
}

// strPtrLocal returns a pointer to s, kept local to this test file (no
// existing pgconv test helpers to reuse — this is the package's first test
// file).
func strPtrLocal(s string) *string { return &s }

// derefForLog renders a *string for a t.Errorf message without a nil
// pointer dereference.
func derefForLog(v *string) string {
	if v == nil {
		return "<nil>"
	}
	return *v
}
