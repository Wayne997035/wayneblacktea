package evals

import (
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
)

// ---------------------------------------------------------------------------
// TestOutcomeLearning_SuccessThreshold
//
// Proves stubOutcomeLearner actually discriminates on successThreshold (3),
// not just returning a constant. The "2 success -> false" case is the
// sensitivity check required by the P6.3 dispatch: if successThreshold were
// changed from 3 to 2 in outcome_learning_grader.go, this subtest would flip
// to Passed:false (2 successes would then satisfy the threshold), so the
// test has real discriminating power rather than being vacuously true.
// ---------------------------------------------------------------------------

func TestOutcomeLearning_SuccessThreshold(t *testing.T) {
	learner := stubOutcomeLearner{}

	t.Run("3 success same entity type -> true", func(t *testing.T) {
		outcomes := []outcome.Outcome{
			{EntityType: "task", Result: "success"},
			{EntityType: "task", Result: "success"},
			{EntityType: "task", Result: "success"},
		}
		if got := learner.ShouldProposeSkill(outcomes); !got {
			t.Errorf("ShouldProposeSkill() = %v, want true for 3 successes on the same entity type", got)
		}
	})

	t.Run("2 success same entity type -> false", func(t *testing.T) {
		outcomes := []outcome.Outcome{
			{EntityType: "task", Result: "success"},
			{EntityType: "task", Result: "success"},
		}
		// Sensitivity: successThreshold-- would make this assertion fail.
		if got := learner.ShouldProposeSkill(outcomes); got {
			t.Errorf("ShouldProposeSkill() = %v, want false for only 2 successes (threshold is %d)", got, successThreshold)
		}
	})

	// auto-seed non-contamination (backend-security-design.md §3.2 data
	// minimisation posture applies analogously here: don't let an
	// operational side effect masquerade as evidence). complete_task
	// auto-seeds a Result: "unknown" outcome per completed task
	// (internal/mcp/tools_gtd.go seedDraftOutcome). ShouldProposeSkill only
	// increments its counter on Result == "success" (outcome_learning_grader.go),
	// so piling up "unknown" rows alongside 2 real successes must NOT push
	// the count to the threshold.
	t.Run("2 success plus auto-seeded unknown rows -> still false", func(t *testing.T) {
		outcomes := []outcome.Outcome{
			{EntityType: "task", Result: "success"},
			{EntityType: "task", Result: "unknown"},
			{EntityType: "task", Result: "unknown"},
			{EntityType: "task", Result: "success"},
			{EntityType: "task", Result: "unknown"},
		}
		if got := learner.ShouldProposeSkill(outcomes); got {
			t.Errorf("ShouldProposeSkill() = %v, want false: auto-seeded \"unknown\" outcomes must not count toward successThreshold", got)
		}
	})

	t.Run("3 success across different entity types -> false", func(t *testing.T) {
		outcomes := []outcome.Outcome{
			{EntityType: "task", Result: "success"},
			{EntityType: "decision", Result: "success"},
			{EntityType: "sprint", Result: "success"},
		}
		if got := learner.ShouldProposeSkill(outcomes); got {
			t.Errorf("ShouldProposeSkill() = %v, want false: successes split across entity types must not sum together", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TestOutcomeLearning_FailedAppears
// ---------------------------------------------------------------------------

func TestOutcomeLearning_FailedAppears(t *testing.T) {
	outcomes := []outcome.Outcome{
		{EntityType: "task", Result: "unknown", Notes: ""},
		{EntityType: "task", Result: "failure", Notes: "sqlite migration runner double-applied 000067, retention index missing"},
	}

	t.Run("failure notes contain query -> surfaced", func(t *testing.T) {
		if got := FailedOutcomeSurfaced(outcomes, "retention index"); !got {
			t.Errorf("FailedOutcomeSurfaced() = %v, want true: query substring is present in the failure's Notes", got)
		}
	})

	t.Run("query not present in any failure -> not surfaced", func(t *testing.T) {
		if got := FailedOutcomeSurfaced(outcomes, "nonexistent-topic"); got {
			t.Errorf("FailedOutcomeSurfaced() = %v, want false: no failure Notes mention this query", got)
		}
	})

	// auto-seed non-contamination: an "unknown" outcome whose Notes happens
	// to be empty must not be mistaken for a failure match, and must not
	// suppress a real failure elsewhere in the slice either.
	t.Run("unknown outcome ignored, real failure still surfaces", func(t *testing.T) {
		if got := FailedOutcomeSurfaced(outcomes, "migration runner"); !got {
			t.Errorf("FailedOutcomeSurfaced() = %v, want true: the failure entry must still surface regardless of preceding unknown rows", got)
		}
	})
}

// ---------------------------------------------------------------------------
// TestOutcomeLearning_Regression
//
// Guards stubOutcomeLearner (or a future real OutcomeLearner) in BOTH
// directions against every fixture case: expect_candidate:false must never
// come back true (never over-propose), and expect_candidate:true must come
// back true (never under-propose). Skipping either direction would let a
// learner that always returns false pass this test vacuously.
//
// 2026-07-20: learn-001 originally carried only 2 success entries while
// asserting expect_candidate:true, contradicting the 3-success threshold.
// P6.3 flagged it rather than silently papering over it, and the fixture was
// corrected to 3 successes (user ruling: the threshold is right, the fixture
// was wrong), which is what allows the true-direction assertion below.
// ---------------------------------------------------------------------------

// outcomeLearningFixtureEntry mirrors one element of the "outcomes" array in
// testdata/outcome_learning_cases.json. Declared locally (distinct from
// evals_test.go's outcomeLearningCaseFixture/outcomeFixtureEntry) because a
// fixture-shaped type used only by test code must live in a _test.go file;
// outcome_learning_grader.go is a production file compiled by `go build
// ./internal/evals/...`, which excludes _test.go files, so it cannot
// reference a type declared there.
type outcomeLearningFixtureEntry struct {
	Status    string   `json:"status"`
	Repo      string   `json:"repo"`
	Files     []string `json:"files"`
	Procedure string   `json:"procedure"`
}

type outcomeLearningFixtureCase struct {
	ID              string                        `json:"id"`
	Outcomes        []outcomeLearningFixtureEntry `json:"outcomes"`
	SkillName       string                        `json:"skill_name"`
	ExpectCandidate bool                          `json:"expect_candidate"`
}

// toDomainOutcomes converts the fixture's JSON vocabulary (status/repo/
// files/procedure) into the real internal/outcome domain type OutcomeLearner
// operates on. procedure becomes EntityType — the dimension the fixture
// cases actually group repeated evidence by; repo/files have no home on
// outcome.Outcome and are folded into Notes for traceability only.
func toDomainOutcomes(entries []outcomeLearningFixtureEntry) []outcome.Outcome {
	out := make([]outcome.Outcome, 0, len(entries))
	for _, e := range entries {
		out = append(out, outcome.Outcome{
			EntityType: e.Procedure,
			Result:     e.Status,
			Notes:      strings.Join(e.Files, ","),
		})
	}
	return out
}

func TestOutcomeLearning_Regression(t *testing.T) {
	fixtures := LoadFixtures[outcomeLearningFixtureCase](t, "outcome_learning_cases.json")
	learner := stubOutcomeLearner{}

	sawExpectTrue := false
	for _, fc := range fixtures {
		got := learner.ShouldProposeSkill(toDomainOutcomes(fc.Outcomes))
		if got != fc.ExpectCandidate {
			t.Errorf("case %s (skill %q): ShouldProposeSkill = %v, want %v per fixture expect_candidate",
				fc.ID, fc.SkillName, got, fc.ExpectCandidate)
		}
		if fc.ExpectCandidate {
			sawExpectTrue = true
		}
	}

	// Non-vacuity guard: without at least one expect_candidate:true case, a
	// learner hard-coded to return false would satisfy every assertion above.
	if !sawExpectTrue {
		t.Fatal("fixture has no expect_candidate:true case — the true direction is untested and this test is vacuous")
	}
}

// ---------------------------------------------------------------------------
// TestOutcomeLearningGrader_Grade proves the Grade(ctx) EvalResult contract
// (evals.go's Grader interface) is actually wired up and runnable through
// the shared Run entrypoint, using hand-built cases that avoid the
// learn-001 fixture/threshold mismatch documented above.
// ---------------------------------------------------------------------------

func TestOutcomeLearningGrader_Grade(t *testing.T) {
	grader := OutcomeLearningGrader{
		Cases: []OutcomeLearningCase{
			{
				CaseID:    "grader-001-three-successes",
				SkillName: "add-eval-harness-scaffold",
				Outcomes: []outcome.Outcome{
					{EntityType: "task", Result: "success"},
					{EntityType: "task", Result: "success"},
					{EntityType: "task", Result: "success"},
				},
				ExpectCandidate: true,
			},
			{
				CaseID:    "grader-002-single-success",
				SkillName: "apply-behavior-rule-outcome",
				Outcomes: []outcome.Outcome{
					{EntityType: "task", Result: "success"},
				},
				ExpectCandidate: false,
			},
		},
	}

	Run(t, []Grader{grader})
}
