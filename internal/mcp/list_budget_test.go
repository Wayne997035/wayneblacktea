package mcp

import (
	"strings"
	"testing"
)

// TestListBudget_UnderBudget_NotTruncated is F0930-11's parity/regression
// check: a small fixture well under listRuneBudget must come back untouched
// — truncated=false and every row kept, matching pre-F0930-11 semantics for
// the common case.
func TestListBudget_UnderBudget_NotTruncated(t *testing.T) {
	t.Parallel()
	rows := []string{"a", "b", "c", "d", "e"}
	kept, truncated := truncateListByRuneBudget(rows, listRuneBudget)
	if truncated {
		t.Errorf("truncated = true for a %d-rune fixture well under the %d-rune budget", len(strings.Join(rows, "")), listRuneBudget)
	}
	if len(kept) != len(rows) {
		t.Errorf("kept %d rows, want all %d", len(kept), len(rows))
	}
}

// TestListBudget_StopsBeforeOverBudgetRow pins the "only whole trailing rows
// dropped, never a partial field" contract directly against the generic
// helper, independent of any of the four MCP handlers built on top of it.
func TestListBudget_StopsBeforeOverBudgetRow(t *testing.T) {
	t.Parallel()
	// Each row marshals to `"xxxx...xxxx"` (100 runes of quoted content +
	// quotes); a maxRunes small enough to fit 3 rows but not a 4th pins the
	// exact boundary, matching appendNextActionsWithinByteBudget's own
	// "only whole trailing rows dropped" test style (resources_test.go).
	row := strings.Repeat("x", 100)
	rows := []string{row, row, row, row, row}
	// "[" + "]" = 2, each row = 102 runes (quotes), +1 comma between rows
	// after the first: budget for exactly 3 rows = 2 + 102 + 103 + 103 = 310.
	kept, truncated := truncateListByRuneBudget(rows, 310)
	if !truncated {
		t.Fatal("truncated = false, want true — the fixture has 5 rows and the budget fits 3")
	}
	if len(kept) != 3 {
		t.Errorf("kept %d rows, want exactly 3 (the budget must stop BEFORE the 4th, not include a partial row)", len(kept))
	}
	for i, r := range kept {
		if r != row {
			t.Errorf("kept[%d] = %q, want the original row unchanged (only whole trailing rows are ever dropped)", i, r)
		}
	}
}

// TestListBudget_SingleRowExceedsBudget is the zero-row edge case: even the
// very first row alone is larger than maxRunes. This must still be a
// successful, empty page — never an error — matching
// appendNextActionsWithinByteBudget's "only whole trailing rows dropped"
// contract taken to its limit.
func TestListBudget_SingleRowExceedsBudget(t *testing.T) {
	t.Parallel()
	rows := []string{strings.Repeat("x", 1000)}
	kept, truncated := truncateListByRuneBudget(rows, 50)
	if !truncated {
		t.Error("truncated = false, want true — the single row alone exceeds the budget")
	}
	if len(kept) != 0 {
		t.Errorf("kept %d rows, want 0 — a row larger than the whole budget must be dropped, not returned partially", len(kept))
	}
}

// TestListBudget_EmptyInput is the degenerate case: no rows at all is not
// truncated (there was nothing to drop).
func TestListBudget_EmptyInput(t *testing.T) {
	t.Parallel()
	kept, truncated := truncateListByRuneBudget([]string{}, listRuneBudget)
	if truncated {
		t.Error("truncated = true for an empty fixture — nothing was dropped, there was nothing to drop")
	}
	if len(kept) != 0 {
		t.Errorf("kept %d rows, want 0", len(kept))
	}
}
