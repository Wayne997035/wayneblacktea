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

// TestIsReservedAuditAction_SpacingCompatAndMultiRune is the [SEC-PR208-SE-03]
// guard: a spacing-compat character for the underscore and "cl" (or "cI", "c1",
// "c|") for d are rejected; the negative rows show the folds miss ordinary
// actions and that a no-break space stays allowed. Pure unit test, no DB.
func TestIsReservedAuditAction_SpacingCompatAndMultiRune(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   bool
	}{
		{"double_low_line_for_underscore", "task‗deleted", true},
		{"overline_for_underscore", "project‾deleted", true},
		{"macron_for_underscore", "task¯deleted", true},
		{"cl_for_d", "task_cleleted", true},
		{"cl_for_d_project", "project_cleleted", true},
		{"capital_I_cl_for_d", "task_cIeleted", true},
		{"digit_1_cl_for_d", "task_c1eleted", true},
		{"pipe_cl_for_d", "project_c|eleted", true},
		{"double_low_line_other_word", "task‗updated", false},
		{"cl_in_ordinary_action", "task_clean", false},
		{"cl_in_ordinary_project_action", "project_closed", false},
		{"no_break_space_for_underscore", "task deleted", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gtd.IsReservedAuditAction(tc.action); got != tc.want {
				t.Errorf("IsReservedAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}
