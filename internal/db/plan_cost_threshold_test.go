package db

import "testing"

// [F1003-08][F1003-09] Lead's supplement 1 cost-threshold methodology
// (sprint-1003 dispatch record), extracted into a pure function so it can
// be exercised by a synthetic table test independent of any real EXPLAIN
// output (this file carries no build tag — it runs under plain `go test`,
// no Docker). The real EXPLAIN-driven call sites are
// migration_000086_explain_test.go / migration_000087_explain_test.go
// (build tag integration).
//
// Source for the formula: PostgreSQL 16's plancache.c choose_custom_plan
// ("if (plansource->generic_cost < avg_custom_cost) return false;" — false
// meaning "don't use a custom plan", i.e. switch to generic) and
// cached_plan_cost ("result += 1000.0 * cpu_operator_cost * (nrelations +
// 1);", the per-execution replanning overhead added to the custom plan's
// own cost before the comparison):
// https://github.com/postgres/postgres/blob/REL_16_STABLE/src/backend/utils/cache/plancache.c

// genericPlanGateResult is the outcome of comparing one (custom, generic)
// EXPLAIN top-level cost pair against the threshold PostgreSQL's plancache
// would use to decide whether to keep using a custom plan.
type genericPlanGateResult struct {
	// threshold is customTopCost + 1000 * cpuOperatorCost * (nrelations+1).
	threshold float64
	// willSwitch reports whether genericTopCost < threshold — i.e. whether
	// PostgreSQL would switch from custom to generic plans in production
	// after the 5th execution of this prepared statement.
	willSwitch bool
	// pass is false only when willSwitch is true AND the generic plan
	// itself doesn't use the index cleanly (no index, or a Sort node) —
	// the one combination that means a production query could silently
	// degrade to a worse plan than what the custom-plan EXPLAIN tested.
	pass bool
	// reason explains pass in prose, for test failure messages / logs.
	reason string
}

// judgeGenericPlanGate applies the threshold formula and the gating rule:
// when PG wouldn't switch to generic in production (willSwitch == false),
// the generic plan's index/Sort shape is informational only — logged, not
// gated. When PG would switch (willSwitch == true), the generic plan MUST
// use the index with no Sort node, or pass is false.
func judgeGenericPlanGate(customTopCost, genericTopCost, cpuOperatorCost float64, nrelations int, genericUsesIndex, genericHasSort bool) genericPlanGateResult {
	threshold := customTopCost + 1000.0*cpuOperatorCost*float64(nrelations+1)
	willSwitch := genericTopCost < threshold
	if !willSwitch {
		return genericPlanGateResult{
			threshold:  threshold,
			willSwitch: false,
			pass:       true,
			reason:     "generic_Y >= threshold: PG keeps the custom plan in production; generic-plan index usage is informational only",
		}
	}
	if !genericUsesIndex || genericHasSort {
		return genericPlanGateResult{
			threshold:  threshold,
			willSwitch: true,
			pass:       false,
			reason:     "generic_Y < threshold: PG would switch to the generic plan in production, but it doesn't use the index with no Sort node",
		}
	}
	return genericPlanGateResult{
		threshold:  threshold,
		willSwitch: true,
		pass:       true,
		reason:     "generic_Y < threshold: PG would switch to the generic plan in production, and it still uses the index with no Sort node",
	}
}

// TestJudgeGenericPlanGate_SyntheticBothDirections is the "驗收層級自檢"
// positive control for the threshold judgment itself (dispatch record,
// 補強1 self-check): two synthetic cost pairs, one on each side of the
// threshold, proving the judgment function calls both directions
// correctly without depending on which side a real EXPLAIN happens to
// land on.
func TestJudgeGenericPlanGate_SyntheticBothDirections(t *testing.T) {
	const (
		customTopCost   = 10.0
		cpuOperatorCost = 0.0025
		nrelations      = 1
	)
	// threshold = 10 + 1000*0.0025*(1+1) = 10 + 5 = 15.
	wantThreshold := 15.0

	t.Run("generic_Y above threshold: index requirement not gated even if absent", func(t *testing.T) {
		got := judgeGenericPlanGate(customTopCost, 20.0, cpuOperatorCost, nrelations, false, true)
		if got.threshold != wantThreshold {
			t.Fatalf("threshold = %v, want %v", got.threshold, wantThreshold)
		}
		if got.willSwitch {
			t.Error("willSwitch = true, want false (generic_Y=20 >= threshold=15)")
		}
		if !got.pass {
			t.Errorf("pass = false, want true (not gated when PG keeps the custom plan): %s", got.reason)
		}
	})

	t.Run("generic_Y below threshold and generic plan has no index: judgment fails", func(t *testing.T) {
		got := judgeGenericPlanGate(customTopCost, 5.0, cpuOperatorCost, nrelations, false, false)
		if got.threshold != wantThreshold {
			t.Fatalf("threshold = %v, want %v", got.threshold, wantThreshold)
		}
		if !got.willSwitch {
			t.Error("willSwitch = false, want true (generic_Y=5 < threshold=15)")
		}
		if got.pass {
			t.Errorf("pass = true, want false (PG would switch to a generic plan with no index): %s", got.reason)
		}
	})

	t.Run("generic_Y below threshold but generic plan still uses the index with no Sort: passes", func(t *testing.T) {
		got := judgeGenericPlanGate(customTopCost, 5.0, cpuOperatorCost, nrelations, true, false)
		if !got.willSwitch {
			t.Error("willSwitch = false, want true (generic_Y=5 < threshold=15)")
		}
		if !got.pass {
			t.Errorf("pass = false, want true (PG switches but the generic plan is still safe): %s", got.reason)
		}
	})
}
