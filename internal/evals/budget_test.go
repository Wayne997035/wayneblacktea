package evals

import "testing"

// ---------------------------------------------------------------------------
// BudgetEnforcer: conservation, omission reporting, unbounded-retrieval guard
// ---------------------------------------------------------------------------

// TestBudget_Conservation asserts len(included) + omittedCount ==
// len(items) for every fixture case — no item may vanish without being
// counted. See TestBudget_ConservationCatchesOffByOne below for proof this
// assertion is not vacuous: an enforcer that undercounts omittedCount by
// one fails it.
func TestBudget_Conservation(t *testing.T) {
	cases := LoadFixtures[budgetFixtureCase](t, "budget_cases.json")
	enforcer := stubBudgetEnforcer{}

	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			g := newBudgetGrader(c, enforcer)
			included, omittedCount := enforcer.Apply(g.items, g.budgetBytes)
			if len(included)+omittedCount != len(g.items) {
				t.Fatalf("conservation violated: included=%d + omitted=%d != total=%d",
					len(included), omittedCount, len(g.items))
			}
		})
	}
}

// offByOneBudgetEnforcer wraps stubBudgetEnforcer but deliberately
// undercounts omittedCount by one whenever anything was omitted — it
// simulates an item silently vanishing instead of being reported.
type offByOneBudgetEnforcer struct{}

func (offByOneBudgetEnforcer) Apply(items []BudgetItem, budgetBytes int) ([]BudgetItem, int) {
	included, omittedCount := stubBudgetEnforcer{}.Apply(items, budgetBytes)
	if omittedCount > 0 {
		omittedCount--
	}
	return included, omittedCount
}

// TestBudget_ConservationCatchesOffByOne proves the conservation property
// asserted in TestBudget_Conservation actually detects a broken
// implementation: offByOneBudgetEnforcer undercounts omittedCount by one on
// any fixture case with at least one omission, which must break the
// included+omitted==total equality.
func TestBudget_ConservationCatchesOffByOne(t *testing.T) {
	cases := LoadFixtures[budgetFixtureCase](t, "budget_cases.json")
	broken := offByOneBudgetEnforcer{}

	sawViolation := false
	for _, c := range cases {
		g := newBudgetGrader(c, broken)
		included, omittedCount := broken.Apply(g.items, g.budgetBytes)
		if len(included)+omittedCount != len(g.items) {
			sawViolation = true
		}
	}
	if !sawViolation {
		t.Fatal("expected the off-by-one enforcer to violate conservation on at least one fixture case, but none did")
	}
}

// TestBudget_OmittedCountReported asserts the omitted count matches the
// fixture's expectation exactly — a silent-drop bug that zeroes out
// omittedCount while still shrinking `included` would fail this even though
// it might coincidentally satisfy conservation in isolation.
func TestBudget_OmittedCountReported(t *testing.T) {
	cases := LoadFixtures[budgetFixtureCase](t, "budget_cases.json")
	enforcer := stubBudgetEnforcer{}

	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			g := newBudgetGrader(c, enforcer)
			_, omittedCount := enforcer.Apply(g.items, g.budgetBytes)
			if omittedCount != c.ExpectOmittedCount {
				t.Errorf("omitted count = %d, want %d (omissions must be reported, not silently dropped)",
					omittedCount, c.ExpectOmittedCount)
			}
		})
	}
}

// TestBudget_UnboundedRetrievalGuard asserts len(included) <= len(items)
// always holds — the enforcer must never return more items than it was
// offered.
func TestBudget_UnboundedRetrievalGuard(t *testing.T) {
	cases := LoadFixtures[budgetFixtureCase](t, "budget_cases.json")
	enforcer := stubBudgetEnforcer{}

	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			g := newBudgetGrader(c, enforcer)
			included, _ := enforcer.Apply(g.items, g.budgetBytes)
			if len(included) > len(g.items) {
				t.Fatalf("included %d items but only %d were offered — retrieval is unbounded",
					len(included), len(g.items))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// budgetGrader: fixture-backed end-to-end run
// ---------------------------------------------------------------------------

// TestBudgetGrader_FixtureCases loads every case in
// testdata/budget_cases.json and runs it through budgetGrader, which wraps
// stubBudgetEnforcer's conservation, omitted-count, and included-ID checks
// into the Grader contract.
func TestBudgetGrader_FixtureCases(t *testing.T) {
	cases := LoadFixtures[budgetFixtureCase](t, "budget_cases.json")
	enforcer := stubBudgetEnforcer{}

	graders := make([]Grader, 0, len(cases))
	for _, c := range cases {
		graders = append(graders, newBudgetGrader(c, enforcer))
	}
	Run(t, graders)
}
