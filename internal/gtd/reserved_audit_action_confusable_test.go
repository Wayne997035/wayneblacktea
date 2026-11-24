package gtd_test

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestIsReservedAuditAction_ASCIIConfusables is the [F1124-02] guard: common
// ASCII look-alikes (I/1/| for l, 0 for o) must be treated as the reserved
// name's letter, while near-misses outside the fold table stay allowed. Pure
// unit test, no DB.
func TestIsReservedAuditAction_ASCIIConfusables(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   bool
	}{
		{"capital_I_for_l", "task_deIeted", true},
		{"capital_I_for_l_negative", "task_updated", false},
		{"digit_1_for_l", "project_de1eted", true},
		{"digit_1_for_l_negative", "project_de2eted", false},
		{"zero_for_o", "project_rest0red", true},
		{"zero_for_o_negative", "project_restxred", false},
		{"case_regression", "PROJECT_DELETED", true},
		{"case_regression_negative", "project_deleter", false},
		{"pipe_for_l", "task_de|eted", true},
		{"pipe_for_l_negative", "task_de-eted", false},
		{"longer_not_matched", "task_deIeted_x", false},
		{"exact_name_still_true", "task_deleted", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gtd.IsReservedAuditAction(tc.action); got != tc.want {
				t.Errorf("IsReservedAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}
