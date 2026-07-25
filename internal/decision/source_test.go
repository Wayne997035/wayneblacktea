package decision_test

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
)

// TestSource_Valid is a pure unit test (no DB) for decision.Source.Valid —
// comparison is exact and case-sensitive with no trimming: near-miss values
// (wrong case, surrounding whitespace) are invalid, not coerced (P3.0a
// Step 4).
func TestSource_Valid(t *testing.T) {
	cases := []struct {
		name string
		s    decision.Source
		want bool
	}{
		{"manual is valid", decision.SourceManual, true},
		{"auto is valid", decision.SourceAuto, true},
		{"empty is invalid", decision.Source(""), false},
		{"wrong case Manual is invalid", decision.Source("Manual"), false},
		{"wrong case AUTO is invalid", decision.Source("AUTO"), false},
		{"leading space is invalid", decision.Source(" manual"), false},
		{"trailing space is invalid", decision.Source("auto "), false},
		{"unrelated value is invalid", decision.Source("system"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Valid(); got != tc.want {
				t.Errorf("Source(%q).Valid() = %v, want %v", string(tc.s), got, tc.want)
			}
		})
	}
}
