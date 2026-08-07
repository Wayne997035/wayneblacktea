package scheduler

// PG integration coverage for job 2 (stuck_task_detection) and job 4
// (knowledge_to_skill_candidate) dedup (GTD 80cf80b6, PR #152 2nd-army
// finding): both jobs previously had no dedup guard at all, so every run
// re-proposed the same still-qualifying row. Mirrors the dedup test shape
// already established for job 3 in decision_outcome_review_pg_test.go
// (TestDecisionOutcomeReview_Dedup_SecondRunCreatesZero).

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Job 2 — stuck_task_detection dedup
// ---------------------------------------------------------------------------

func seedStuckTask(t *testing.T, ctx context.Context, wsID, id uuid.UUID, title string, updatedAt time.Time) {
	t.Helper()
	_, err := testPgPool.Exec(ctx, `INSERT INTO tasks
		(id, workspace_id, title, status, priority, updated_at)
		VALUES ($1, $2, $3, 'in_progress', 3, $4)`,
		id, wsID, title, updatedAt)
	if err != nil {
		t.Fatalf("seed task %s: %v", title, err)
	}
	t.Cleanup(func() { _, _ = testPgPool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", id) })
}

func countStuckTaskProposals(t *testing.T, ctx context.Context, wsID uuid.UUID) int {
	t.Helper()
	var count int
	err := testPgPool.QueryRow(ctx, `SELECT COUNT(*) FROM pending_proposals
		WHERE workspace_id = $1 AND type = 'task'
		  AND proposed_by = 'scheduler:stuck_task' AND status = 'pending'`, wsID).Scan(&count)
	if err != nil {
		t.Fatalf("count stuck_task proposals: %v", err)
	}
	return count
}

func stuckTaskSourceEntityIDs(t *testing.T, ctx context.Context, wsID uuid.UUID) map[string]int {
	t.Helper()
	rows, err := testPgPool.Query(ctx, `SELECT payload FROM pending_proposals
		WHERE workspace_id = $1 AND type = 'task'
		  AND proposed_by = 'scheduler:stuck_task' AND status = 'pending'`, wsID)
	if err != nil {
		t.Fatalf("query stuck_task payloads: %v", err)
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

func cleanupStuckTaskProposals(t *testing.T, ctx context.Context, wsID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testPgPool.Exec(ctx, `DELETE FROM pending_proposals
			WHERE workspace_id = $1 AND proposed_by = 'scheduler:stuck_task'`, wsID)
	})
}

// TestStuckTaskDetection_Dedup_SecondRunCreatesZero is the primary
// regression test for job 2: running the job twice against the same
// still-in_progress task must NOT double the pending proposal count on the
// second run — the SQL-level NOT EXISTS guard against
// payload->>'source_entity_id' must suppress re-proposing tasks that already
// have a pending scheduler proposal.
func TestStuckTaskDetection_Dedup_SecondRunCreatesZero(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	cleanupStuckTaskProposals(t, ctx, wsID)

	old := time.Now().UTC().AddDate(0, 0, -10) // well past the 7-day window
	taskIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range taskIDs {
		seedStuckTask(t, ctx, wsID, id, "stuck task "+id.String(), old.Add(time.Duration(i)*time.Minute))
	}

	propStore := proposal.NewStore(pool, &wsID)
	sc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps:  &cognitiveDeps{proposal: propStore, workspaceID: &wsID},
	}

	sc.runStuckTaskDetection()
	firstCount := countStuckTaskProposals(t, ctx, wsID)
	if firstCount != len(taskIDs) {
		t.Fatalf("first run: expected %d proposals, got %d", len(taskIDs), firstCount)
	}

	// Second run against the SAME still-in_progress tasks: must create ZERO
	// new proposals.
	sc.runStuckTaskDetection()
	secondCount := countStuckTaskProposals(t, ctx, wsID)
	if secondCount != firstCount {
		t.Errorf("second run: expected proposal count to stay at %d (dedup), got %d", firstCount, secondCount)
	}

	counts := stuckTaskSourceEntityIDs(t, ctx, wsID)
	for _, id := range taskIDs {
		if got := counts[id.String()]; got != 1 {
			t.Errorf("task %s: expected exactly 1 pending proposal, got %d", id, got)
		}
	}
}

// TestStuckTaskDetection_PayloadCarriesSourceEntityID verifies the build
// closure fix: job 2's TaskPayload.SourceEntityID must be set (previously
// always empty, which is what made the dedup predicate unable to match
// anything before this fix).
func TestStuckTaskDetection_PayloadCarriesSourceEntityID(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	cleanupStuckTaskProposals(t, ctx, wsID)

	taskID := uuid.New()
	seedStuckTask(t, ctx, wsID, taskID, "stuck task", time.Now().UTC().AddDate(0, 0, -10))

	propStore := proposal.NewStore(pool, &wsID)
	sc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps:  &cognitiveDeps{proposal: propStore, workspaceID: &wsID},
	}
	sc.runStuckTaskDetection()

	counts := stuckTaskSourceEntityIDs(t, ctx, wsID)
	if counts[taskID.String()] != 1 {
		t.Errorf("expected proposal payload.source_entity_id == %q, got counts=%v", taskID, counts)
	}
}

// ---------------------------------------------------------------------------
// Job 4 — knowledge_to_skill_candidate dedup
// ---------------------------------------------------------------------------

func seedHighRecallKnowledgeItem(t *testing.T, ctx context.Context, wsID, id uuid.UUID, title string) {
	t.Helper()
	_, err := testPgPool.Exec(ctx, `INSERT INTO knowledge_items
		(id, workspace_id, type, title, content, recall_count)
		VALUES ($1, $2, 'article', $3, 'body', 5)`,
		id, wsID, title)
	if err != nil {
		t.Fatalf("seed knowledge item %s: %v", title, err)
	}
	t.Cleanup(func() { _, _ = testPgPool.Exec(ctx, "DELETE FROM knowledge_items WHERE id = $1", id) })
}

func countKnowledgeToSkillProposals(t *testing.T, ctx context.Context, wsID uuid.UUID) int {
	t.Helper()
	var count int
	err := testPgPool.QueryRow(ctx, `SELECT COUNT(*) FROM pending_proposals
		WHERE workspace_id = $1 AND type = 'knowledge'
		  AND proposed_by = 'scheduler:knowledge_to_skill' AND status = 'pending'`, wsID).Scan(&count)
	if err != nil {
		t.Fatalf("count knowledge_to_skill proposals: %v", err)
	}
	return count
}

func knowledgeToSkillSourceEntityIDs(t *testing.T, ctx context.Context, wsID uuid.UUID) map[string]int {
	t.Helper()
	rows, err := testPgPool.Query(ctx, `SELECT payload FROM pending_proposals
		WHERE workspace_id = $1 AND type = 'knowledge'
		  AND proposed_by = 'scheduler:knowledge_to_skill' AND status = 'pending'`, wsID)
	if err != nil {
		t.Fatalf("query knowledge_to_skill payloads: %v", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		var kp proposal.KnowledgePayload
		if err := json.Unmarshal(raw, &kp); err != nil {
			t.Fatalf("unmarshal KnowledgePayload: %v", err)
		}
		counts[kp.SourceEntityID]++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate payload rows: %v", err)
	}
	return counts
}

func cleanupKnowledgeToSkillProposals(t *testing.T, ctx context.Context, wsID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testPgPool.Exec(ctx, `DELETE FROM pending_proposals
			WHERE workspace_id = $1 AND proposed_by = 'scheduler:knowledge_to_skill'`, wsID)
	})
}

// TestKnowledgeToSkillCandidate_Dedup_SecondRunCreatesZero is the primary
// regression test for job 4: running the job twice against the same
// still-high-recall knowledge item must NOT double the pending proposal
// count on the second run.
func TestKnowledgeToSkillCandidate_Dedup_SecondRunCreatesZero(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	cleanupKnowledgeToSkillProposals(t, ctx, wsID)

	itemIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for _, id := range itemIDs {
		seedHighRecallKnowledgeItem(t, ctx, wsID, id, "high recall item "+id.String())
	}

	propStore := proposal.NewStore(pool, &wsID)
	sc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps:  &cognitiveDeps{proposal: propStore, workspaceID: &wsID},
	}

	sc.runKnowledgeToSkillCandidate()
	firstCount := countKnowledgeToSkillProposals(t, ctx, wsID)
	if firstCount != len(itemIDs) {
		t.Fatalf("first run: expected %d proposals, got %d", len(itemIDs), firstCount)
	}

	// Second run against the SAME still-high-recall items: must create ZERO
	// new proposals.
	sc.runKnowledgeToSkillCandidate()
	secondCount := countKnowledgeToSkillProposals(t, ctx, wsID)
	if secondCount != firstCount {
		t.Errorf("second run: expected proposal count to stay at %d (dedup), got %d", firstCount, secondCount)
	}

	counts := knowledgeToSkillSourceEntityIDs(t, ctx, wsID)
	for _, id := range itemIDs {
		if got := counts[id.String()]; got != 1 {
			t.Errorf("item %s: expected exactly 1 pending proposal, got %d", id, got)
		}
	}
}

// TestKnowledgeToSkillCandidate_PayloadCarriesSourceEntityID verifies the
// build closure fix: job 4's KnowledgePayload.SourceEntityID (a NEW field —
// KnowledgePayload had none before this fix) must be set.
func TestKnowledgeToSkillCandidate_PayloadCarriesSourceEntityID(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	cleanupKnowledgeToSkillProposals(t, ctx, wsID)

	itemID := uuid.New()
	seedHighRecallKnowledgeItem(t, ctx, wsID, itemID, "high recall item")

	propStore := proposal.NewStore(pool, &wsID)
	sc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps:  &cognitiveDeps{proposal: propStore, workspaceID: &wsID},
	}
	sc.runKnowledgeToSkillCandidate()

	counts := knowledgeToSkillSourceEntityIDs(t, ctx, wsID)
	if counts[itemID.String()] != 1 {
		t.Errorf("expected proposal payload.source_entity_id == %q, got counts=%v", itemID, counts)
	}
}
