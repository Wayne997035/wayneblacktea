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
//
// This is a SYNTHETIC FIXTURE ENFORCER, not the production budget path, and
// this grader does NOT prove production contextpack budget enforcement is
// correct. The real implementation, internal/contextpack.trimToBudget
// (scorer.go), differs from this stub in every dimension that matters:
//   - Unit: trimToBudget counts runes of Item.Summary (utf8.RuneCountInString);
//     this stub counts BudgetItem.SizeBytes, an opaque caller-supplied int.
//   - Stop condition: trimToBudget stops non-pinned inclusion outright at the
//     first item that would overflow the budget (everything after is
//     dropped); this stub skips the offending item and keeps trying
//     later, possibly-smaller items (skip-and-continue).
//   - Pinning: trimToBudget always keeps TypeTask/TypeProject items
//     (isPinned) regardless of score or budget; this stub has no pinning
//     concept — every item is subject to the same priority-ordered cutoff.
//
// budget_cases.json fixtures and this file only exercise the invented
// greedy-skip model above. Passing this grader is evidence the stub's own
// invented model is internally consistent (conservation + expected
// included/omitted match); it is NOT evidence that trimToBudget behaves
// correctly. A grader wired directly to internal/contextpack.trimToBudget
// (or an adapter satisfying BudgetEnforcer by calling it) would be required
// to make that claim, and is out of scope for this fix — see P6-F1 scope
// boundaries (backend-security-design.md §2: grader semantics are not
// touched by this change; this comment only documents an existing gap).
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
