package evals

import (
	"context"
	"fmt"
	"sort"
)

// BudgetItem is one retrievable context item competing for inclusion within
// a fixed byte budget. Priority is the ranking signal (higher = more
// important); SizeBytes is its cost against the budget.
type BudgetItem struct {
	ID        string
	SizeBytes int
	Priority  int
}

// BudgetEnforcer decides which items fit within budgetBytes and reports how
// many were omitted. Implementations MUST report every omission — silently
// dropping items without surfacing omittedCount would let a caller believe
// it received the complete result set (dispatch threat surface: unbounded
// retrieval / silent omission).
type BudgetEnforcer interface {
	Apply(items []BudgetItem, budgetBytes int) (included []BudgetItem, omittedCount int)
}

// stubBudgetEnforcer greedily includes items highest-priority-first, adding
// each item's SizeBytes to a running total. An item that would push the
// running total over budgetBytes is skipped (counted as omitted), and the
// loop continues to later, possibly-smaller items rather than stopping at
// the first miss.
type stubBudgetEnforcer struct{}

func (stubBudgetEnforcer) Apply(items []BudgetItem, budgetBytes int) ([]BudgetItem, int) {
	ordered := make([]BudgetItem, len(items))
	copy(ordered, items)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Priority > ordered[j].Priority
	})

	included := make([]BudgetItem, 0, len(items))
	omittedCount := 0
	runningTotal := 0
	for _, item := range ordered {
		if runningTotal+item.SizeBytes > budgetBytes {
			omittedCount++
			continue
		}
		included = append(included, item)
		runningTotal += item.SizeBytes
	}
	return included, omittedCount
}

// budgetFixtureItem/budgetFixtureCase mirror testdata/budget_cases.json's
// schema. Declared here (not reused from evals_test.go's budgetCaseFixture)
// because non-test files cannot reference types declared in _test.go files.
type budgetFixtureItem struct {
	ID        string `json:"id"`
	SizeBytes int    `json:"size_bytes"`
	Priority  int    `json:"priority"`
}

type budgetFixtureCase struct {
	ID                 string              `json:"id"`
	Items              []budgetFixtureItem `json:"items"`
	BudgetBytes        int                 `json:"budget_bytes"`
	ExpectIncluded     []string            `json:"expect_included"`
	ExpectOmittedCount int                 `json:"expect_omitted_count"`
}

// budgetGrader evaluates one budget_cases.json fixture case against a
// BudgetEnforcer implementation.
type budgetGrader struct {
	caseID             string
	items              []BudgetItem
	budgetBytes        int
	expectIncluded     []string
	expectOmittedCount int
	enforcer           BudgetEnforcer
}

// newBudgetGrader converts one fixture case into a budgetGrader, wiring in
// the BudgetEnforcer implementation under test.
func newBudgetGrader(c budgetFixtureCase, enforcer BudgetEnforcer) budgetGrader {
	items := make([]BudgetItem, len(c.Items))
	for i, fi := range c.Items {
		// budgetFixtureItem and BudgetItem share identical field names/types
		// (struct tags are ignored for conversion), so a direct conversion
		// is correct here, not a coincidental field-by-field copy.
		items[i] = BudgetItem(fi)
	}
	return budgetGrader{
		caseID:             c.ID,
		items:              items,
		budgetBytes:        c.BudgetBytes,
		expectIncluded:     c.ExpectIncluded,
		expectOmittedCount: c.ExpectOmittedCount,
		enforcer:           enforcer,
	}
}

// Grade implements the Grader contract (internal/evals/evals.go).
func (g budgetGrader) Grade(_ context.Context) EvalResult {
	included, omittedCount := g.enforcer.Apply(g.items, g.budgetBytes)

	// Conservation: every item is either included or counted as omitted —
	// never both, never neither. An off-by-one here (e.g. an implementation
	// that drops an item without incrementing omittedCount) fails this
	// check instead of silently passing.
	if len(included)+omittedCount != len(g.items) {
		return EvalResult{
			CaseID: g.caseID,
			Passed: false,
			Reason: fmt.Sprintf("conservation violated: included=%d + omitted=%d != total=%d",
				len(included), omittedCount, len(g.items)),
		}
	}

	if omittedCount != g.expectOmittedCount {
		return EvalResult{
			CaseID: g.caseID,
			Passed: false,
			Reason: fmt.Sprintf("omitted count = %d, want %d", omittedCount, g.expectOmittedCount),
		}
	}

	gotIDs := make([]string, len(included))
	for i, item := range included {
		gotIDs[i] = item.ID
	}
	if !sameIDSet(gotIDs, g.expectIncluded) {
		return EvalResult{
			CaseID: g.caseID,
			Passed: false,
			Reason: fmt.Sprintf("included IDs %v, want %v", gotIDs, g.expectIncluded),
		}
	}

	return EvalResult{CaseID: g.caseID, Passed: true}
}

// sameIDSet reports whether got and want contain the same IDs, ignoring
// order — a BudgetEnforcer may return included items in priority order
// rather than input order.
func sameIDSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	remaining := make(map[string]int, len(want))
	for _, id := range want {
		remaining[id]++
	}
	for _, id := range got {
		remaining[id]--
		if remaining[id] < 0 {
			return false
		}
	}
	return true
}
