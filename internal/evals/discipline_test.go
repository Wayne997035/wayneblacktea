package evals

import (
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
)

// ---------------------------------------------------------------------------
// DisciplineChecker: verified against the live discipline.MutatingTools /
// discipline.DeliberatelyExcludedTools allowlists, never a local copy.
// ---------------------------------------------------------------------------

// TestDiscipline_MutatingTools_AreRequired iterates the live
// discipline.MutatingTools map (not a snapshot) so that a tool silently
// removed from the allowlist would immediately start failing this test.
func TestDiscipline_MutatingTools_AreRequired(t *testing.T) {
	checker := stubDisciplineChecker{}
	for name := range discipline.MutatingTools {
		if !checker.RequiresVerification(name) {
			t.Errorf("mutating tool %q: RequiresVerification = false, want true", name)
		}
	}
}

// TestDiscipline_ReadonlyTools_NotRequired spot-checks representative
// read-only tools (get/list-style) that never call a Store write method.
func TestDiscipline_ReadonlyTools_NotRequired(t *testing.T) {
	checker := stubDisciplineChecker{}
	readonly := []string{"get_today_context", "list_tasks", "list_decisions"}
	for _, name := range readonly {
		if checker.RequiresVerification(name) {
			t.Errorf("readonly tool %q: RequiresVerification = true, want false", name)
		}
	}
}

// TestDiscipline_DeliberatelyExcludedTools_NotRequired iterates the live
// discipline.DeliberatelyExcludedTools allowlist — the explicit "this looks
// like it might mutate but is intentionally not drift-tracked" list — and
// asserts none of them require verification either. Together with
// TestDiscipline_MutatingTools_AreRequired above, this is how the grader
// handles BOTH allowlists: MutatingTools members must all read true,
// DeliberatelyExcludedTools members must all read false. Neither list is
// ever copied locally; both are read live so drift in either one is caught.
func TestDiscipline_DeliberatelyExcludedTools_NotRequired(t *testing.T) {
	checker := stubDisciplineChecker{}
	for name := range discipline.DeliberatelyExcludedTools {
		if checker.RequiresVerification(name) {
			t.Errorf("tool %q is in DeliberatelyExcludedTools but RequiresVerification = true", name)
		}
	}
}

// TestDiscipline_AllowlistsAreDisjoint guards the classification-gap
// scenario called out in the P6.4 dispatch: MutatingTools and
// DeliberatelyExcludedTools are meant to be mutually exclusive. A tool name
// present in both would make its classification ambiguous depending on
// iteration order — this test fails loudly instead of one list silently
// shadowing the other.
func TestDiscipline_AllowlistsAreDisjoint(t *testing.T) {
	for name := range discipline.MutatingTools {
		if discipline.DeliberatelyExcludedTools[name] {
			t.Errorf("tool %q is present in both MutatingTools and DeliberatelyExcludedTools", name)
		}
	}
}

// ---------------------------------------------------------------------------
// CloseoutChecker
// ---------------------------------------------------------------------------

// TestCloseout_IncompleteWhenOpenTasks covers the "all clear" happy path
// plus each of the three independent conditions (and all three at once)
// that must force IsComplete to false.
func TestCloseout_IncompleteWhenOpenTasks(t *testing.T) {
	checker := stubCloseoutChecker{}

	tests := []struct {
		name     string
		snapshot CloseoutSnapshot
		want     bool
	}{
		{
			name:     "all clear is complete",
			snapshot: CloseoutSnapshot{OpenTaskCount: 0, PendingProposals: 0, HandoffSet: true},
			want:     true,
		},
		{
			name:     "open tasks make it incomplete",
			snapshot: CloseoutSnapshot{OpenTaskCount: 1, PendingProposals: 0, HandoffSet: true},
			want:     false,
		},
		{
			name:     "pending proposals make it incomplete",
			snapshot: CloseoutSnapshot{OpenTaskCount: 0, PendingProposals: 3, HandoffSet: true},
			want:     false,
		},
		{
			name:     "missing handoff makes it incomplete",
			snapshot: CloseoutSnapshot{OpenTaskCount: 0, PendingProposals: 0, HandoffSet: false},
			want:     false,
		},
		{
			name:     "all three conditions at once is still just incomplete",
			snapshot: CloseoutSnapshot{OpenTaskCount: 2, PendingProposals: 1, HandoffSet: false},
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checker.IsComplete(tc.snapshot)
			if got != tc.want {
				t.Errorf("IsComplete(%+v) = %v, want %v", tc.snapshot, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// disciplineDriftGrader: fixture-backed end-to-end run
// ---------------------------------------------------------------------------

// TestDisciplineGrader_FixtureCases loads every case in
// testdata/discipline_cases.json and runs it through disciplineDriftGrader,
// which wraps computeDrift + stubDisciplineChecker into the Grader contract.
func TestDisciplineGrader_FixtureCases(t *testing.T) {
	cases := LoadFixtures[disciplineFixtureCase](t, "discipline_cases.json")
	checker := stubDisciplineChecker{}

	graders := make([]Grader, 0, len(cases))
	for _, c := range cases {
		graders = append(graders, disciplineDriftGrader{
			caseID:           c.ID,
			toolName:         c.ToolName,
			hasPriorDecision: c.HasPriorDecision,
			expectDrift:      c.ExpectDrift,
			checker:          checker,
		})
	}

	Run(t, graders)
}
