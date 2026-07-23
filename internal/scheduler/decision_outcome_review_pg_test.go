package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// seedOldDecision inserts a decision row older than decisionOutcomeInterval
// (30 days) with no matching outcomes row, so it qualifies for
// runDecisionOutcomeReview on every call unless a matching pending proposal
// already exists (dedup) or the row falls outside the daily cap window.
func seedOldDecision(t *testing.T, ctx context.Context, wsID, id uuid.UUID, title string, createdAt time.Time) {
	t.Helper()
	_, err := testPgPool.Exec(ctx, `INSERT INTO decisions
		(id, workspace_id, title, context, decision, rationale, created_at)
		VALUES ($1, $2, $3, 'ctx', 'dec', 'rationale', $4)`,
		id, wsID, title, createdAt)
	if err != nil {
		t.Fatalf("seed decision %s: %v", title, err)
	}
	t.Cleanup(func() {
		_, _ = testPgPool.Exec(ctx, "DELETE FROM decisions WHERE id = $1", id)
	})
}

// countDecisionOutcomeProposals returns how many pending_proposals rows exist
// for the given workspace that were created by decision_outcome_review and
// are still pending.
func countDecisionOutcomeProposals(t *testing.T, ctx context.Context, wsID uuid.UUID) int {
	t.Helper()
	var count int
	err := testPgPool.QueryRow(ctx, `SELECT COUNT(*) FROM pending_proposals
		WHERE workspace_id = $1
		  AND type = 'task'
		  AND proposed_by = 'scheduler:decision_outcome_review'
		  AND status = 'pending'`, wsID).Scan(&count)
	if err != nil {
		t.Fatalf("count decision_outcome_review proposals: %v", err)
	}
	return count
}

// sourceEntityIDsFor returns the set of payload.source_entity_id values for
// all pending decision_outcome_review proposals in the workspace. Used to
// assert dedup: each decision ID must appear at most once across repeated
// job runs, and to assert the daily-cap test picked the expected (oldest)
// decisions.
func sourceEntityIDsFor(t *testing.T, ctx context.Context, wsID uuid.UUID) map[string]int {
	t.Helper()
	rows, err := testPgPool.Query(ctx, `SELECT payload FROM pending_proposals
		WHERE workspace_id = $1
		  AND type = 'task'
		  AND proposed_by = 'scheduler:decision_outcome_review'
		  AND status = 'pending'`, wsID)
	if err != nil {
		t.Fatalf("query payloads: %v", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		var tp proposal.TaskPayload
		if err := json.Unmarshal(raw, &tp); err != nil {
			t.Fatalf("unmarshal TaskPayload: %v", err)
		}
		counts[tp.SourceEntityID]++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate payload rows: %v", err)
	}
	return counts
}

func cleanupDecisionOutcomeProposals(t *testing.T, ctx context.Context, wsID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testPgPool.Exec(ctx, `DELETE FROM pending_proposals
			WHERE workspace_id = $1 AND proposed_by = 'scheduler:decision_outcome_review'`, wsID)
	})
}

// TestDecisionOutcomeReview_Dedup_SecondRunCreatesZero is the primary
// regression test for the 2026-07-19 incident: running the job twice against
// the same backlog of no-outcome decisions must NOT double the pending
// proposal count on the second run — the SQL-level NOT EXISTS guard against
// payload->>'source_entity_id' must suppress re-proposing decisions that
// already have a pending scheduler proposal.
func TestDecisionOutcomeReview_Dedup_SecondRunCreatesZero(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	cleanupDecisionOutcomeProposals(t, ctx, wsID)

	old := time.Now().UTC().AddDate(0, 0, -45) // well past the 30-day window
	decisionIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for i, id := range decisionIDs {
		seedOldDecision(t, ctx, wsID, id, "decision "+id.String(), old.Add(time.Duration(i)*time.Minute))
	}

	propStore := proposal.NewStore(pool, &wsID)
	sc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps: &cognitiveDeps{
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}

	// First run: one proposal per decision.
	sc.runDecisionOutcomeReview()
	firstCount := countDecisionOutcomeProposals(t, ctx, wsID)
	if firstCount != len(decisionIDs) {
		t.Fatalf("first run: expected %d proposals, got %d", len(decisionIDs), firstCount)
	}

	// Second run against the SAME backlog (no outcome recorded, no proposal
	// resolved in between): must create ZERO new proposals.
	sc.runDecisionOutcomeReview()
	secondCount := countDecisionOutcomeProposals(t, ctx, wsID)
	if secondCount != firstCount {
		t.Errorf("second run: expected proposal count to stay at %d (dedup), got %d", firstCount, secondCount)
	}

	// Each decision must appear at most once across both runs.
	counts := sourceEntityIDsFor(t, ctx, wsID)
	for _, id := range decisionIDs {
		if got := counts[id.String()]; got != 1 {
			t.Errorf("decision %s: expected exactly 1 pending proposal, got %d", id, got)
		}
	}
}

// TestDecisionOutcomeReview_Dedup_ExcludesDecisionsWithOutcome verifies the
// pre-existing NOT EXISTS(outcomes) guard still works in combination with the
// new dedup guard — a decision that already has a recorded outcome must never
// generate a proposal, even on the very first run.
func TestDecisionOutcomeReview_Dedup_ExcludesDecisionsWithOutcome(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	cleanupDecisionOutcomeProposals(t, ctx, wsID)

	old := time.Now().UTC().AddDate(0, 0, -45)
	withOutcomeID := uuid.New()
	withoutOutcomeID := uuid.New()
	seedOldDecision(t, ctx, wsID, withOutcomeID, "has outcome", old)
	seedOldDecision(t, ctx, wsID, withoutOutcomeID, "no outcome", old.Add(time.Minute))

	outcomeRowID := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO outcomes
		(id, workspace_id, entity_type, entity_id, result)
		VALUES ($1, $2, 'decision', $3, 'success')`,
		outcomeRowID, wsID, withOutcomeID)
	if err != nil {
		t.Fatalf("seed outcome: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM outcomes WHERE id = $1", outcomeRowID)
	})

	propStore := proposal.NewStore(pool, &wsID)
	sc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps: &cognitiveDeps{
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}
	sc.runDecisionOutcomeReview()

	counts := sourceEntityIDsFor(t, ctx, wsID)
	if counts[withOutcomeID.String()] != 0 {
		t.Errorf("decision with recorded outcome must not get a proposal, got count=%d", counts[withOutcomeID.String()])
	}
	if counts[withoutOutcomeID.String()] != 1 {
		t.Errorf("decision without outcome must get exactly 1 proposal, got count=%d", counts[withoutOutcomeID.String()])
	}
}

// TestDecisionOutcomeReview_DailyCap_LimitsCreatedCount is the regression test
// for the "613 backlog -> context deadline exceeded" half of the incident.
// decisionOutcomeReviewDailyCap is temporarily shrunk so the test doesn't need
// to seed hundreds of rows to exercise the LIMIT path. Asserts the job stops
// at the cap AND that it picked the oldest decisions first (ORDER BY
// created_at ASC), so the backlog drains forward on subsequent runs instead
// of being stuck reprocessing the same head-of-queue rows forever.
func TestDecisionOutcomeReview_DailyCap_LimitsCreatedCount(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	cleanupDecisionOutcomeProposals(t, ctx, wsID)

	prevCap := decisionOutcomeReviewDailyCap
	const testCap = 3
	decisionOutcomeReviewDailyCap = testCap
	t.Cleanup(func() { decisionOutcomeReviewDailyCap = prevCap })

	old := time.Now().UTC().AddDate(0, 0, -45)
	// Seed 5 qualifying decisions — more than testCap(3) — with strictly
	// increasing created_at so we can assert the oldest 3 were picked.
	const total = 5
	decisionIDs := make([]uuid.UUID, total)
	for i := 0; i < total; i++ {
		decisionIDs[i] = uuid.New()
		seedOldDecision(t, ctx, wsID, decisionIDs[i], "cap-test", old.Add(time.Duration(i)*time.Hour))
	}

	propStore := proposal.NewStore(pool, &wsID)
	sc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps: &cognitiveDeps{
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}
	sc.runDecisionOutcomeReview()

	got := countDecisionOutcomeProposals(t, ctx, wsID)
	if got != testCap {
		t.Fatalf("expected exactly %d proposals (daily cap), got %d", testCap, got)
	}

	counts := sourceEntityIDsFor(t, ctx, wsID)
	// The first testCap decisions (oldest) must have been proposed...
	for i := 0; i < testCap; i++ {
		got := counts[decisionIDs[i].String()]
		if got != 1 {
			t.Errorf("expected oldest decision[%d]=%s to have a proposal, got count=%d", i, decisionIDs[i], got)
		}
	}
	// ...and the remaining (newest) decisions must NOT have been proposed yet
	// — they roll forward into the next day's run.
	for i := testCap; i < total; i++ {
		got := counts[decisionIDs[i].String()]
		if got != 0 {
			t.Errorf("expected newest decision[%d]=%s to be skipped this run (cap), got count=%d", i, decisionIDs[i], got)
		}
	}
}

// TestDecisionOutcomeReview_EmptyBacklog_NoPanic verifies the job is safe to
// run against a workspace with zero qualifying decisions — production may go
// days without a new no-outcome decision.
func TestDecisionOutcomeReview_EmptyBacklog_NoPanic(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	wsID := uuid.New()
	propStore := proposal.NewStore(pool, &wsID)
	sc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps: &cognitiveDeps{
			proposal:    propStore,
			workspaceID: &wsID,
		},
	}
	// Must not panic on zero-row result.
	sc.runDecisionOutcomeReview()
}
