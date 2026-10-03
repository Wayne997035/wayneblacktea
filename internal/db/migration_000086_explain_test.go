//go:build integration

package db

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// [F1003-08] Lead's supplement 1 methodology applied to migration 000086
// (idx_goals_active_due_date): proves both (a) the custom-plan prod path
// uses the index with no Sort node (hard requirement — STOP condition if
// it fails) and (b) the generic-plan cost-threshold judgment at LIMIT 50
// and LIMIT 500.
//
// Query text source: the unexported sqlc-generated listActiveGoals
// constant (internal/db/gtd.sql.go), never hand-transcribed.
//
// Data volume: 2000 rows in the target workspace + 2000 in another
// workspace, half status='active' — the "假設" section's documented test
// scale (sprint-1003 dispatch record: "未驗證 — production row counts;
// this comparison only holds at this scale").
func seedGoalsForCostThreshold(t *testing.T, ctx context.Context, wsID, otherWsID uuid.UUID) {
	t.Helper()
	for _, ws := range []uuid.UUID{wsID, otherWsID} {
		if _, err := testPgPool.Exec(ctx, `
			INSERT INTO goals (workspace_id, title, status, due_date)
			SELECT $1, 'g' || i, CASE WHEN i % 2 = 0 THEN 'active' ELSE 'archived' END,
			       CASE WHEN i % 4 = 0 THEN NOW() - (i || ' minutes')::interval ELSE NULL END
			FROM generate_series(1, 2000) AS i`, ws); err != nil {
			t.Fatalf("seed 2000 goals for workspace %s: %v", ws, err)
		}
	}
	if _, err := testPgPool.Exec(ctx, `ANALYZE goals`); err != nil {
		t.Fatalf("ANALYZE goals: %v", err)
	}
}

// TestMigration000086_CustomPlanUsesIndexNoSort is the hard-judgment half
// of Lead's supplement 1 (custom plan is always a STOP-gated requirement,
// no threshold involved): the production-shaped call (workspace_id bound
// to a real UUID, offset 0) must use idx_goals_active_due_date with no
// Sort node, at both LIMIT 50 and LIMIT 500.
func TestMigration000086_CustomPlanUsesIndexNoSort(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	wsID := uuid.New()
	otherWsID := uuid.New()
	seedGoalsForCostThreshold(t, ctx, wsID, otherWsID)

	for _, limit := range []int32{50, 500} {
		plan := explainCustomPlan(t, ctx, testPgPool, listActiveGoals, wsUUID(wsID), int32(0), limit)
		t.Logf("[000086] custom plan, limit=%d:\n%s", limit, plan)
		if !strings.Contains(plan, "Index Scan using idx_goals_active_due_date") &&
			!strings.Contains(plan, "Index Only Scan using idx_goals_active_due_date") {
			t.Fatalf("STOP: custom plan (limit=%d) doesn't use idx_goals_active_due_date:\n%s", limit, plan)
		}
		if strings.Contains(plan, "Sort") {
			t.Fatalf("STOP: custom plan (limit=%d) has a Sort node:\n%s", limit, plan)
		}
	}
}

// TestMigration000086_GenericPlanCostThreshold is the cost-threshold half:
// measures the real custom_Y / generic_Y / cpu_operator_cost at LIMIT 50
// and 500, computes the threshold, and applies judgeGenericPlanGate.
// STOP conditions (per dispatch record): generic plan EXPLAIN doesn't
// contain "$1" (measurement method itself broken), or generic_Y < threshold
// while the generic plan lacks the index / has a Sort node.
func TestMigration000086_GenericPlanCostThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	wsID := uuid.New()
	otherWsID := uuid.New()
	seedGoalsForCostThreshold(t, ctx, wsID, otherWsID)

	cpuCost := cpuOperatorCostOf(t, ctx, testPgPool)
	t.Logf("[000086] cpu_operator_cost = %v", cpuCost)

	for _, limit := range []int32{50, 500} {
		customPlan := explainCustomPlan(t, ctx, testPgPool, listActiveGoals, wsUUID(wsID), int32(0), limit)
		customY := explainTopCost(t, customPlan)

		genericQuery := literalizeLimitOffset(listActiveGoals, int(limit))
		genericPlan := explainGenericPlan(t, ctx, testPgPool, genericQuery)
		t.Logf("[000086] limit=%d generic plan:\n%s", limit, genericPlan)

		// Self-proof (STOP condition): the generic plan must show $1
		// literally — proof it's a genuine generic plan, not accidentally
		// a custom one.
		if !strings.Contains(genericPlan, "$1") {
			t.Fatalf("STOP: generic-plan EXPLAIN (limit=%d) does not contain \"$1\" — measurement method is broken, "+
				"not a real generic plan:\n%s", limit, genericPlan)
		}
		genericY := explainTopCost(t, genericPlan)
		genericUsesIndex := strings.Contains(genericPlan, "idx_goals_active_due_date")
		genericHasSort := strings.Contains(genericPlan, "Sort")

		result := judgeGenericPlanGate(customY, genericY, cpuCost, 1, genericUsesIndex, genericHasSort)
		t.Logf("[000086] limit=%d: custom_Y=%v generic_Y=%v threshold=%v willSwitch=%v pass=%v (%s)",
			limit, customY, genericY, result.threshold, result.willSwitch, result.pass, result.reason)

		if !result.pass {
			t.Fatalf("STOP: limit=%d generic-plan cost-threshold judgment failed: %s (custom_Y=%v generic_Y=%v threshold=%v)",
				limit, result.reason, customY, genericY, result.threshold)
		}
	}
}
