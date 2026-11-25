package gtd_test

import (
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestIsReservedAuditAction_BidiControl is the [F1124-01] guard: an action
// containing any of the 12 Unicode bidi control code points must be rejected
// by IsReservedAuditAction, because the control character (category Cf) is
// dropped for the comparison but kept in the stored value, so a reversed
// spelling would render as project_deleted. Pure unit test, no DB. Source
// stays pure ASCII: code points are written as string(rune(0x...)).
func TestIsReservedAuditAction_BidiControl(t *testing.T) {
	bidi := []rune{
		0x200E, 0x200F,
		0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
		0x2066, 0x2067, 0x2068, 0x2069,
		0x061C,
	}
	if len(bidi) != 12 {
		t.Fatalf("test enumerates %d code points, want 12", len(bidi))
	}

	cases := []struct {
		name   string
		action string
		want   bool
	}{
		{"reversed_with_202E", string(rune(0x202E)) + "deteled_tcejorp", true},
		{"reversed_without_202E", "deteled_tcejorp", false},
		{"2067_wrapping_unreserved", string(rune(0x2067)) + "task_updated", true},
		{"2067_wrapping_unreserved_negative", "task_updated", false},
		{"200F_suffix", "atom_consolidation" + string(rune(0x200F)), true},
		{"200F_suffix_negative", "atom_consolidation", false},
		{"arabic_plain_allowed", "تسجيل", false},
		{"arabic_with_061C", "تسجيل" + string(rune(0x061C)), true},
		{"plain_log_entry", "log_entry", false},
	}
	for _, r := range bidi {
		cases = append(cases, struct {
			name   string
			action string
			want   bool
		}{fmt.Sprintf("each_code_point/%04X", r), "log_" + string(r) + "entry", true})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gtd.IsReservedAuditAction(tc.action); got != tc.want {
				t.Errorf("IsReservedAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}
