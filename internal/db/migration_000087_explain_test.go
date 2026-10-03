//go:build integration

package db

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// [F1003-09] Lead's supplement 1 methodology applied to migration 000087
// (idx_pending_proposals_status_pending's realigned created_at DESC, id
// DESC shape). Per Lead's supplement 2: 000087's index does NOT lead with
// workspace_id (workspace condition only ever lands in a Filter, never the
// Index Cond), so the risk being tested here is different from 000086's:
// whether the planner instead prefers idx_pending_proposals_workspace_id
// and re-sorts — same custom-plan hard requirement + generic-plan
// cost-threshold judgment either way.
//
// Query text source: the unexported sqlc-generated listPendingProposals
// constant (internal/db/proposal.sql.go), never hand-transcribed.
func seedPendingProposalsForCostThreshold(t *testing.T, ctx context.Context, wsID, otherWsID uuid.UUID) {
	t.Helper()
	for _, ws := range []uuid.UUID{wsID, otherWsID} {
		if _, err := testPgPool.Exec(ctx, `
			INSERT INTO pending_proposals (workspace_id, type, payload, status, proposed_by, created_at)
			SELECT $1, 'task', '{}'::jsonb, CASE WHEN i % 2 = 0 THEN 'pending' ELSE 'accepted' END,
			       'seed', NOW() - (i || ' seconds')::interval
			FROM generate_series(1, 2000) AS i`, ws); err != nil {
			t.Fatalf("seed 2000 pending_proposals for workspace %s: %v", ws, err)
		}
	}
	if _, err := testPgPool.Exec(ctx, `ANALYZE pending_proposals`); err != nil {
		t.Fatalf("ANALYZE pending_proposals: %v", err)
	}
}

// TestMigration000087_CustomPlanUsesIndexNoSort is the hard-judgment half:
// the production-shaped call must use idx_pending_proposals_status_pending
// with no Sort node, at both LIMIT 50 and LIMIT 500.
func TestMigration000087_CustomPlanUsesIndexNoSort(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	wsID := uuid.New()
	otherWsID := uuid.New()
	seedPendingProposalsForCostThreshold(t, ctx, wsID, otherWsID)

	for _, limit := range []int32{50, 500} {
		plan := explainCustomPlan(t, ctx, testPgPool, listPendingProposals, wsUUID(wsID), int32(0), limit)
		t.Logf("[000087] custom plan, limit=%d:\n%s", limit, plan)
		if !strings.Contains(plan, "idx_pending_proposals_status_pending") {
			t.Fatalf("STOP: custom plan (limit=%d) doesn't use idx_pending_proposals_status_pending:\n%s", limit, plan)
		}
		if strings.Contains(plan, "Sort") {
			t.Fatalf("STOP: custom plan (limit=%d) has a Sort node:\n%s", limit, plan)
		}
	}
}

// TestMigration000087_GenericPlanCostThreshold is the cost-threshold half,
// mirroring TestMigration000086_GenericPlanCostThreshold.
func TestMigration000087_GenericPlanCostThreshold(t *testing.T) {
	if testing.Short() {
		t.Skip("requires Docker")
	}
	ctx := context.Background()
	wsID := uuid.New()
	otherWsID := uuid.New()
	seedPendingProposalsForCostThreshold(t, ctx, wsID, otherWsID)

	cpuCost := cpuOperatorCostOf(t, ctx, testPgPool)
	t.Logf("[000087] cpu_operator_cost = %v", cpuCost)

	for _, limit := range []int32{50, 500} {
		customPlan := explainCustomPlan(t, ctx, testPgPool, listPendingProposals, wsUUID(wsID), int32(0), limit)
		customY := explainTopCost(t, customPlan)

		genericQuery := literalizeLimitOffset(listPendingProposals, int(limit))
		genericPlan := explainGenericPlan(t, ctx, testPgPool, genericQuery)
		t.Logf("[000087] limit=%d generic plan:\n%s", limit, genericPlan)

		if !strings.Contains(genericPlan, "$1") {
			t.Fatalf("STOP: generic-plan EXPLAIN (limit=%d) does not contain \"$1\" — measurement method is broken:\n%s", limit, genericPlan)
		}
		genericY := explainTopCost(t, genericPlan)
		genericUsesIndex := strings.Contains(genericPlan, "idx_pending_proposals_status_pending")
		genericHasSort := strings.Contains(genericPlan, "Sort")

		result := judgeGenericPlanGate(customY, genericY, cpuCost, 1, genericUsesIndex, genericHasSort)
		t.Logf("[000087] limit=%d: custom_Y=%v generic_Y=%v threshold=%v willSwitch=%v pass=%v (%s)",
			limit, customY, genericY, result.threshold, result.willSwitch, result.pass, result.reason)

		if !result.pass {
			t.Fatalf("STOP: limit=%d generic-plan cost-threshold judgment failed: %s (custom_Y=%v generic_Y=%v threshold=%v)",
				limit, result.reason, customY, genericY, result.threshold)
		}
	}
}
