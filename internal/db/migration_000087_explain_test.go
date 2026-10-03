//go:build integration

package db

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// [F1003-09] Lead's supplement 1 methodology applied to migration 000087.
//
// idx_pending_proposals_status_pending alone (no workspace_id column)
// loses to idx_pending_proposals_workspace_id + an explicit Sort at LIMIT
// 500, because production is not assumed single-workspace: the planner
// has to scan deep enough into the created_at-ordered index to collect
// LIMIT matching rows for one workspace out of however many share it.
// Migration 000087 therefore also creates a net-new workspace_id-leading
// composite, idx_pending_proposals_workspace_pending_sort, giving the
// per-workspace path an Index Cond on workspace_id plus the exact
// (created_at, id) trailing order; this file's hard requirement is that
// the custom plan uses THAT index, not idx_pending_proposals_status_pending.
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
// the production-shaped call must use
// idx_pending_proposals_workspace_pending_sort, with workspace_id in the
// Index Cond and no Sort node, at both LIMIT 50 and LIMIT 500 — see this
// file's header comment for why idx_pending_proposals_status_pending
// alone cannot satisfy this at LIMIT 500.
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
		if !strings.Contains(plan, "idx_pending_proposals_workspace_pending_sort") {
			t.Fatalf("STOP: custom plan (limit=%d) doesn't use idx_pending_proposals_workspace_pending_sort:\n%s", limit, plan)
		}
		if !strings.Contains(plan, "workspace_id") {
			t.Fatalf("STOP: custom plan (limit=%d) index usage doesn't condition on workspace_id:\n%s", limit, plan)
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
		genericUsesIndex := strings.Contains(genericPlan, "idx_pending_proposals_workspace_pending_sort") ||
			strings.Contains(genericPlan, "idx_pending_proposals_status_pending")
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
