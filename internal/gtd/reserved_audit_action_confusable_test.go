package gtd_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"

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

// TestIsReservedAuditAction_SpacingCompat guards [F1125-01] and [F1125-02].
// [F1125-01]: compatibility characters such as U+2017 decompose under NFKD
// into U+0020 + a combining mark, and the directly-sent expanded form
// "space + combining mark" renders as a single visible glyph; both must be
// treated as one placeholder character in a reserved name.
// [F1125-02]: U+0385 expands to space + two combining marks, so the
// placeholder rule must not replace the first combining mark too (the
// "task\u0385deleted" row fails without the not-Mn/Me condition).
func TestIsReservedAuditAction_SpacingCompat(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   bool
	}{
		{"u2017_double_low_line", "task\u2017deleted", true},
		{"space_plus_u0332", "task \u0332deleted", true},
		{"space_plus_u0333", "task \u0333deleted", true},
		{"space_plus_u0331", "task \u0331deleted", true},
		{"project_space_u0305", "project \u0305deleted", true},
		{"project_space_u0332_restored", "project \u0332restored", true},
		{"u0385_space_two_marks", "task\u0385deleted", true},
		{"plain_space_negative", "task deleted", false},
		{"nbsp_negative", "task\u00a0deleted", false},
		{"u2017_updated_negative", "task\u2017updated", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gtd.IsReservedAuditAction(tc.action); got != tc.want {
				t.Errorf("IsReservedAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}

// TestIsReservedAuditAction_CombiningMarkEnumeration guards [F1125-01]
// exhaustively: every Mn/Me code point after a space, and every code point
// whose NFKD starts with U+0020 and is longer than one rune, must read as a
// single placeholder inside each reserved name.
func TestIsReservedAuditAction_CombiningMarkEnumeration(t *testing.T) {
	var missed []string
	templates := []string{"task%cdeleted", "project%cdeleted", "project%crestored"}
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue
		}
		if unicode.In(r, unicode.Mn, unicode.Me) && !gtd.IsReservedAuditAction("task "+string(r)+"deleted") {
			missed = append(missed, fmt.Sprintf("mark U+%04X", r))
		}
		if !spacingLedNFKD(r) {
			continue
		}
		for _, tpl := range templates {
			if a := fmt.Sprintf(tpl, r); !gtd.IsReservedAuditAction(a) {
				missed = append(missed, fmt.Sprintf("spacing U+%04X in %q", r, tpl))
			}
		}
	}
	if len(missed) > 0 {
		t.Errorf("%d code points not recognised as reserved: %s", len(missed), strings.Join(missed, "; "))
	}
}

// spacingLedNFKD reports whether r's NFKD form starts with U+0020 and is
// longer than one rune.
func spacingLedNFKD(r rune) bool {
	d := norm.NFKD.String(string(r))
	return strings.HasPrefix(d, " ") && len([]rune(d)) > 1
}

// TestIsReservedAuditAction_MultiRuneD guards [F1125-04]: a "d" may be drawn
// as "c" + "l" (or look-alikes of each), and the fold must not be so wide
// that a non-d position becomes reserved.
func TestIsReservedAuditAction_MultiRuneD(t *testing.T) {
	cases := []struct {
		action string
		want   bool
	}{
		{"task_cleleted", true},
		{"project_cleleted", true},
		{"task_\u0441leleted", true},
		{"task_c\u04cfeleted", true},
		{"project_restore\u0441l", true},
		{"project_\u0441\u04cfeleted", true},
		{"task_clean", false},
		{"project_closed", false},
	}
	for _, tc := range cases {
		if got := gtd.IsReservedAuditAction(tc.action); got != tc.want {
			t.Errorf("IsReservedAuditAction(%q) = %v, want %v", tc.action, got, tc.want)
		}
	}
	// Over-folding enumeration (R3-01): replace each position with a
	// two-rune spelling; only a "d" position may become reserved.
	for _, name := range []string{"task_deleted", "project_deleted", "project_restored"} {
		for i := 0; i < len(name); i++ {
			for _, sp := range []string{"cl", "ci", "c1", "c|"} {
				action := name[:i] + sp + name[i+1:]
				want := name[i] == 'd'
				if got := gtd.IsReservedAuditAction(action); got != want {
					t.Errorf("name %q pos %d spelling %q: IsReservedAuditAction(%q) = %v, want %v", name, i, sp, action, got, want)
				}
			}
		}
	}
}

// TestIsReservedAuditAction_NoFalsePositives guards [F1125-03] and
// [F1125-04] from the allow side. [F1125-03]: canonicalAuditAction and the
// placeholder pass share droppedByCanon; the row it protects is the existing
// U+2800 case in TestIsReservedAuditAction_Variants
// (reserved_audit_action_variants_test.go, unchanged). [F1125-04]: ordinary
// action names must never read as reserved.
func TestIsReservedAuditAction_NoFalsePositives(t *testing.T) {
	names := []string{
		"task:begin", "task:completed", "task:updated", "task:added", "decision:logged",
		"plan:confirmed", "session:handoff", "worksession:started", "worksession:finished",
		"worksession:checkpointed", "dashboard:reconciled", "atom_consolidation",
		"closeout_session_check", "task_completed", "task_closed", "task_cleared",
		"task_cleaned", "task_cloned", "task_declined", "task_delegated", "task_deferred",
		"task_reclaimed", "task_selected", "task_relabeled", "task_cancelled",
		"task_canceled", "task_clipped", "project_created", "project_closed",
		"project_cloned", "project_declined", "project_released", "project_restarted",
		"project_restore", "project_reclaimed", "project_archived", "project_cleared",
		"project_selected", "project_relocated", "project_restocked", "project_rescored",
		"project_restyled", "task deleted", "task-deleted", "Task Deleted", "taskdeleted",
		"task_delete", "task_deletes", "tasks_deleted",
	}
	for _, a := range names {
		if gtd.IsReservedAuditAction(a) {
			t.Errorf("IsReservedAuditAction(%q) = true, want false", a)
		}
	}
}
