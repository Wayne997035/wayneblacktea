package runtime

import (
	"testing"
	"time"
)

type budgetCase struct {
	name       string
	budget     Budget
	elapsed    Elapsed
	wantExceed bool
	wantReason FinishReason
}

func runBudgetCases(t *testing.T, cases []budgetCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotEx, gotReason := tc.budget.CheckExceeded(tc.elapsed)
			if gotEx != tc.wantExceed || gotReason != tc.wantReason {
				t.Errorf("CheckExceeded(%+v) = (%v, %q), want (%v, %q)",
					tc.elapsed, gotEx, gotReason, tc.wantExceed, tc.wantReason)
			}
		})
	}
}

// F1123-31
func TestBudget_CheckExceeded_MaxTurns(t *testing.T) {
	runBudgetCases(t, []budgetCase{
		{"over", Budget{MaxTurns: 12}, Elapsed{Turns: 13}, true, FinishReasonMaxTurns},
		{"under", Budget{MaxTurns: 12}, Elapsed{Turns: 11}, false, ""},
		{"negative elapsed", Budget{MaxTurns: 12}, Elapsed{Turns: -1}, false, ""},
	})
}

// F1123-31
func TestBudget_CheckExceeded_MaxDuration(t *testing.T) {
	b := Budget{MaxDuration: 120 * time.Second}
	runBudgetCases(t, []budgetCase{
		{"over", b, Elapsed{Duration: 121 * time.Second}, true, FinishReasonMaxDuration},
		{"under", b, Elapsed{Duration: 119 * time.Second}, false, ""},
		{"zero elapsed", b, Elapsed{}, false, ""},
	})
}

// F1123-31
func TestBudget_CheckExceeded_MaxCost(t *testing.T) {
	b := Budget{MaxCostMicroUSD: 500_000}
	runBudgetCases(t, []budgetCase{
		{"over", b, Elapsed{CostMicroUSD: 500_001}, true, FinishReasonMaxCost},
		{"under", b, Elapsed{CostMicroUSD: 499_999}, false, ""},
		{"zero elapsed", b, Elapsed{}, false, ""},
	})
}

// F1123-31: duration -> cost -> turns.
func TestBudget_CheckExceeded_Order(t *testing.T) {
	b := Budget{MaxTurns: 12, MaxDuration: 120 * time.Second, MaxCostMicroUSD: 500_000}
	runBudgetCases(t, []budgetCase{
		{"all three", b, Elapsed{Turns: 13, Duration: 121 * time.Second, CostMicroUSD: 500_001}, true, FinishReasonMaxDuration},
		{"cost and turns", b, Elapsed{Turns: 13, Duration: 10 * time.Second, CostMicroUSD: 500_001}, true, FinishReasonMaxCost},
		{"duration and turns", b, Elapsed{Turns: 13, Duration: 121 * time.Second}, true, FinishReasonMaxDuration},
		{"turns only", b, Elapsed{Turns: 13}, true, FinishReasonMaxTurns},
	})
}

// F1123-31: exactly equal to the limit is not exceeded.
func TestBudget_CheckExceeded_BoundaryEqualNotExceeded(t *testing.T) {
	b := Budget{MaxTurns: 12, MaxDuration: 120 * time.Second, MaxCostMicroUSD: 500_000}
	runBudgetCases(t, []budgetCase{
		{"turns equal", b, Elapsed{Turns: 12}, false, ""},
		{"duration equal", b, Elapsed{Duration: 120 * time.Second}, false, ""},
		{"cost equal", b, Elapsed{CostMicroUSD: 500_000}, false, ""},
	})
}

// F1123-32
func TestBudget_CheckExceeded_WithinLimits(t *testing.T) {
	b := Budget{MaxTurns: 12, MaxDuration: 120 * time.Second, MaxCostMicroUSD: 500_000}
	runBudgetCases(t, []budgetCase{
		{"within", b, Elapsed{Turns: 5, Duration: 30 * time.Second, CostMicroUSD: 100_000}, false, ""},
		{"zero elapsed", b, Elapsed{}, false, ""},
		{"tool calls not judged", Budget{MaxToolCalls: 1}, Elapsed{Turns: 100}, false, ""},
	})
}

// F1123-32: a limit <= 0 disables that dimension.
func TestBudget_CheckExceeded_ZeroLimitMeansUnlimited(t *testing.T) {
	huge := Elapsed{Turns: 1000, Duration: time.Hour, CostMicroUSD: 1_000_000}
	runBudgetCases(t, []budgetCase{
		{"all zero", Budget{}, huge, false, ""},
		{"negative limits", Budget{MaxTurns: -1, MaxDuration: -time.Second, MaxCostMicroUSD: -1}, huge, false, ""},
		{"only cost unset", Budget{MaxTurns: 12}, Elapsed{CostMicroUSD: 1}, false, ""},
	})
}

// F1123-32
func TestBudget_DefaultInteractiveConstants(t *testing.T) {
	tests := []struct {
		name string
		got  any
		want any
	}{
		{"max turns", DefaultInteractiveMaxTurns, 12},
		{"max tool calls", DefaultInteractiveMaxToolCalls, 20},
		{"max duration", DefaultInteractiveMaxDuration, 120 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

// F1123-31: reason string values are part of the contract.
func TestBudget_FinishReasonValues(t *testing.T) {
	tests := []struct {
		got  FinishReason
		want string
	}{
		{FinishReasonMaxTurns, "max_turns_exceeded"},
		{FinishReasonMaxDuration, "max_duration_exceeded"},
		{FinishReasonMaxCost, "max_cost_exceeded"},
	}
	for _, tc := range tests {
		if string(tc.got) != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
}
