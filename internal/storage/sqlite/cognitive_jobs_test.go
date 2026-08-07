package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/scheduler"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// Compile-time guarantee that *sqlite.CognitiveJobsStore satisfies
// scheduler.CognitiveSQLiteStore. This lives in an external (`_test`)
// package specifically so it can import both packages without triggering
// the scheduler -> notion -> storage -> storage/sqlite import cycle that a
// same-package assertion in cognitive_jobs.go would hit (see that file's
// header comment).
var _ scheduler.CognitiveSQLiteStore = (*sqlite.CognitiveJobsStore)(nil)

// openCognitiveJobsDB opens a fresh in-memory SQLite DB scoped to wsID and
// returns both the raw *DB (for fixture seeding via ExecContext) and the
// CognitiveJobsStore under test.
func openCognitiveJobsDB(t *testing.T, wsID string) (*sqlite.DB, *sqlite.CognitiveJobsStore) {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", wsID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, sqlite.NewCognitiveJobsStore(d)
}

func rfc3339Millis(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func seedCJTask(t *testing.T, d *sqlite.DB, wsID uuid.UUID, id uuid.UUID, title, status string, updatedAt time.Time) {
	t.Helper()
	err := d.ExecContext(context.Background(), `INSERT INTO tasks
		(id, workspace_id, title, status, priority, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, 3, ?5, ?5)`,
		id.String(), wsID.String(), title, status, rfc3339Millis(updatedAt))
	if err != nil {
		t.Fatalf("seed task %s: %v", title, err)
	}
}

func seedCJDecision(t *testing.T, d *sqlite.DB, wsID uuid.UUID, id uuid.UUID, title string, createdAt time.Time) {
	t.Helper()
	err := d.ExecContext(context.Background(), `INSERT INTO decisions
		(id, workspace_id, title, context, decision, rationale, created_at)
		VALUES (?1, ?2, ?3, 'ctx', 'dec', 'rationale', ?4)`,
		id.String(), wsID.String(), title, rfc3339Millis(createdAt))
	if err != nil {
		t.Fatalf("seed decision %s: %v", title, err)
	}
}

func seedCJOutcome(t *testing.T, d *sqlite.DB, wsID, entityID uuid.UUID) {
	t.Helper()
	err := d.ExecContext(context.Background(), `INSERT INTO outcomes
		(id, workspace_id, entity_type, entity_id, result)
		VALUES (?1, ?2, 'decision', ?3, 'success')`,
		uuid.New().String(), wsID.String(), entityID.String())
	if err != nil {
		t.Fatalf("seed outcome for %s: %v", entityID, err)
	}
}

// seedCJPendingTaskProposal seeds a pending_proposals row of type='task' with
// a TaskPayload-shaped JSON payload carrying source_entity_id, mirroring what
// decision_outcome_review itself would have written on a prior run. Used to
// exercise the dedup NOT EXISTS guard.
func seedCJPendingTaskProposal(t *testing.T, d *sqlite.DB, wsID uuid.UUID, proposedBy, sourceEntityID string) {
	t.Helper()
	payload := `{"title":"t","source_tool":"` + proposedBy + `","source_entity_id":"` + sourceEntityID + `"}`
	err := d.ExecContext(context.Background(), `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, status, proposed_by, created_at)
		VALUES (?1, ?2, 'task', ?3, 'pending', ?4, ?5)`,
		uuid.New().String(), wsID.String(), payload, proposedBy, rfc3339Millis(time.Now()))
	if err != nil {
		t.Fatalf("seed pending task proposal: %v", err)
	}
}

// seedCJPendingKnowledgeProposal seeds a pending_proposals row of
// type='knowledge' with a KnowledgePayload-shaped JSON payload carrying
// source_entity_id, mirroring what knowledge_to_skill_candidate itself would
// have written on a prior run. Used to exercise HighRecallKnowledgeItems's
// dedup NOT EXISTS guard (job 4's twin of seedCJPendingTaskProposal above).
func seedCJPendingKnowledgeProposal(t *testing.T, d *sqlite.DB, wsID uuid.UUID, proposedBy, sourceEntityID string) {
	t.Helper()
	payload := `{"title":"t","content":"c","source_entity_id":"` + sourceEntityID + `"}`
	err := d.ExecContext(context.Background(), `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, status, proposed_by, created_at)
		VALUES (?1, ?2, 'knowledge', ?3, 'pending', ?4, ?5)`,
		uuid.New().String(), wsID.String(), payload, proposedBy, rfc3339Millis(time.Now()))
	if err != nil {
		t.Fatalf("seed pending knowledge proposal: %v", err)
	}
}

func seedCJKnowledgeItem(t *testing.T, d *sqlite.DB, wsID uuid.UUID, id uuid.UUID, title string, recallCount int, archived bool) {
	t.Helper()
	var archivedAt any
	if archived {
		archivedAt = rfc3339Millis(time.Now())
	}
	err := d.ExecContext(context.Background(), `INSERT INTO knowledge_items
		(id, workspace_id, type, title, content, recall_count, archived_at, created_at, updated_at)
		VALUES (?1, ?2, 'article', ?3, 'body', ?4, ?5, ?6, ?6)`,
		id.String(), wsID.String(), title, recallCount, archivedAt, rfc3339Millis(time.Now()))
	if err != nil {
		t.Fatalf("seed knowledge item %s: %v", title, err)
	}
}

func seedCJPendingProposal(t *testing.T, d *sqlite.DB, wsID uuid.UUID, id uuid.UUID, proposedBy string, createdAt time.Time) {
	t.Helper()
	err := d.ExecContext(context.Background(), `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, status, proposed_by, created_at)
		VALUES (?1, ?2, 'knowledge', '{"title":"t","content":"c"}', 'pending', ?3, ?4)`,
		id.String(), wsID.String(), proposedBy, rfc3339Millis(createdAt))
	if err != nil {
		t.Fatalf("seed pending proposal %s: %v", proposedBy, err)
	}
}

func pendingProposalStatus(t *testing.T, d *sqlite.DB, id uuid.UUID) (status string, reason *string) {
	t.Helper()
	row := d.QueryRowContext(context.Background(), `SELECT status, reason FROM pending_proposals WHERE id = ?1`, id.String())
	if err := row.Scan(&status, &reason); err != nil {
		t.Fatalf("query proposal %s status: %v", id, err)
	}
	return status, reason
}

// ---------------------------------------------------------------------------
// StuckTasks (job 2)
// ---------------------------------------------------------------------------

func TestCognitiveJobsStore_StuckTasks(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())
	now := time.Now().UTC()

	stuckID := uuid.New()
	seedCJTask(t, d, wsID, stuckID, "stuck task", "in_progress", now.AddDate(0, 0, -10))

	freshID := uuid.New()
	seedCJTask(t, d, wsID, freshID, "fresh in-progress task", "in_progress", now.AddDate(0, 0, -1))

	pendingOldID := uuid.New()
	seedCJTask(t, d, wsID, pendingOldID, "old pending task", taskStatusPending, now.AddDate(0, 0, -10))

	otherWsID := uuid.New()
	otherWsTaskID := uuid.New()
	seedCJTask(t, d, otherWsID, otherWsTaskID, "other workspace stuck task", "in_progress", now.AddDate(0, 0, -20))

	got, err := store.StuckTasks(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("StuckTasks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 stuck task, got %d: %+v", len(got), got)
	}
	if got[0].ID != stuckID || got[0].Title != "stuck task" {
		t.Errorf("unexpected stuck task returned: %+v", got[0])
	}
}

// TestCognitiveJobsStore_StuckTasks_Dedup is the mutation candidate for job
// 2's dedup NOT EXISTS guard (GTD 80cf80b6, PR #152 2nd-army finding): a
// stuck task that already has a pending scheduler:stuck_task proposal
// referencing it via payload.source_entity_id must NOT be returned again.
// Mirrors TestCognitiveJobsStore_DecisionsPendingOutcomeReview_Dedup (job 3)
// above.
func TestCognitiveJobsStore_StuckTasks_Dedup(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())
	old := time.Now().UTC().AddDate(0, 0, -10)

	qualifyingID := uuid.New()
	seedCJTask(t, d, wsID, qualifyingID, "qualifying stuck task", "in_progress", old)

	alreadyProposedID := uuid.New()
	seedCJTask(t, d, wsID, alreadyProposedID, "already proposed stuck task", "in_progress", old)
	seedCJPendingTaskProposal(t, d, wsID, "scheduler:stuck_task", alreadyProposedID.String())

	got, err := store.StuckTasks(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("StuckTasks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 stuck task (dedup excludes the already-proposed one), got %d: %+v", len(got), got)
	}
	if got[0].ID != qualifyingID {
		t.Errorf("expected qualifying task %s, got %s", qualifyingID, got[0].ID)
	}
}

// TestCognitiveJobsStore_StuckTasks_DedupIgnoresOtherJobsProposals verifies
// the dedup guard is scoped to proposed_by='scheduler:stuck_task' only — a
// pending proposal from a DIFFERENT job (even one that happens to reference
// the same source_entity_id, e.g. decision_outcome_review) must NOT suppress
// job 2's own proposal. Regression guard for the threat-surface question in
// GTD 80cf80b6: the dedup predicate is scoped by proposed_by AND type AND
// status, not by source_entity_id alone.
func TestCognitiveJobsStore_StuckTasks_DedupIgnoresOtherJobsProposals(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())
	old := time.Now().UTC().AddDate(0, 0, -10)

	taskID := uuid.New()
	seedCJTask(t, d, wsID, taskID, "stuck task", "in_progress", old)
	// A different job's proposal happens to carry the same source_entity_id —
	// must NOT suppress job 2's own proposal for this task.
	seedCJPendingTaskProposal(t, d, wsID, "scheduler:decision_outcome_review", taskID.String())

	got, err := store.StuckTasks(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("StuckTasks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 stuck task (a different job's proposal must not dedup-suppress this one), got %d: %+v", len(got), got)
	}
}

func TestCognitiveJobsStore_StuckTasks_EmptyNoPanic(t *testing.T) {
	wsID := uuid.New()
	_, store := openCognitiveJobsDB(t, wsID.String())
	got, err := store.StuckTasks(context.Background(), 7*24*time.Hour)
	if err != nil {
		t.Fatalf("StuckTasks on empty table: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 rows, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// DecisionsPendingOutcomeReview (job 3)
// ---------------------------------------------------------------------------

// TestCognitiveJobsStore_DecisionsPendingOutcomeReview_Dedup is the mutation
// candidate for the dedup NOT EXISTS guard: a decision that already has a
// pending scheduler:decision_outcome_review proposal referencing it via
// payload.source_entity_id must NOT be returned again.
func TestCognitiveJobsStore_DecisionsPendingOutcomeReview_Dedup(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())
	old := time.Now().UTC().AddDate(0, 0, -45)

	qualifyingID := uuid.New()
	seedCJDecision(t, d, wsID, qualifyingID, "qualifying decision", old)

	alreadyProposedID := uuid.New()
	seedCJDecision(t, d, wsID, alreadyProposedID, "already proposed decision", old.Add(time.Minute))
	seedCJPendingTaskProposal(t, d, wsID, "scheduler:decision_outcome_review", alreadyProposedID.String())

	got, err := store.DecisionsPendingOutcomeReview(context.Background(), 30*24*time.Hour, 200)
	if err != nil {
		t.Fatalf("DecisionsPendingOutcomeReview: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 decision (dedup excludes the already-proposed one), got %d: %+v", len(got), got)
	}
	if got[0].ID != qualifyingID {
		t.Errorf("expected qualifying decision %s, got %s", qualifyingID, got[0].ID)
	}
}

// TestCognitiveJobsStore_DecisionsPendingOutcomeReview_ExcludesWithOutcome
// mirrors TestDecisionOutcomeReview_Dedup_ExcludesDecisionsWithOutcome (PG,
// internal/scheduler/decision_outcome_review_pg_test.go) — same fixture
// shape, SQLite backend.
func TestCognitiveJobsStore_DecisionsPendingOutcomeReview_ExcludesWithOutcome(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())
	old := time.Now().UTC().AddDate(0, 0, -45)

	withOutcomeID := uuid.New()
	seedCJDecision(t, d, wsID, withOutcomeID, "has outcome", old)
	seedCJOutcome(t, d, wsID, withOutcomeID)

	withoutOutcomeID := uuid.New()
	seedCJDecision(t, d, wsID, withoutOutcomeID, "no outcome", old.Add(time.Minute))

	tooRecentID := uuid.New()
	seedCJDecision(t, d, wsID, tooRecentID, "too recent", time.Now().UTC().AddDate(0, 0, -5))

	got, err := store.DecisionsPendingOutcomeReview(context.Background(), 30*24*time.Hour, 200)
	if err != nil {
		t.Fatalf("DecisionsPendingOutcomeReview: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 decision, got %d: %+v", len(got), got)
	}
	if got[0].ID != withoutOutcomeID {
		t.Errorf("expected decision without outcome %s, got %s", withoutOutcomeID, got[0].ID)
	}
}

func TestCognitiveJobsStore_DecisionsPendingOutcomeReview_RespectsLimit(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())
	old := time.Now().UTC().AddDate(0, 0, -45)

	const total = 5
	ids := make([]uuid.UUID, total)
	for i := 0; i < total; i++ {
		ids[i] = uuid.New()
		seedCJDecision(t, d, wsID, ids[i], "cap-test", old.Add(time.Duration(i)*time.Hour))
	}

	const limit = 3
	got, err := store.DecisionsPendingOutcomeReview(context.Background(), 30*24*time.Hour, limit)
	if err != nil {
		t.Fatalf("DecisionsPendingOutcomeReview: %v", err)
	}
	if len(got) != limit {
		t.Fatalf("expected exactly %d decisions (limit), got %d", limit, len(got))
	}
	// Oldest-first: the first `limit` seeded IDs must be the ones returned.
	returned := map[uuid.UUID]bool{}
	for _, d := range got {
		returned[d.ID] = true
	}
	for i := 0; i < limit; i++ {
		if !returned[ids[i]] {
			t.Errorf("expected oldest decision[%d]=%s to be returned", i, ids[i])
		}
	}
}

// ---------------------------------------------------------------------------
// HighRecallKnowledgeItems (job 4)
// ---------------------------------------------------------------------------

func TestCognitiveJobsStore_HighRecallKnowledgeItems(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())

	highRecallID := uuid.New()
	seedCJKnowledgeItem(t, d, wsID, highRecallID, "high recall item", 5, false)

	lowRecallID := uuid.New()
	seedCJKnowledgeItem(t, d, wsID, lowRecallID, "low recall item", 2, false)

	archivedID := uuid.New()
	seedCJKnowledgeItem(t, d, wsID, archivedID, "archived high recall item", 10, true)

	got, err := store.HighRecallKnowledgeItems(context.Background(), 3)
	if err != nil {
		t.Fatalf("HighRecallKnowledgeItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 high-recall item, got %d: %+v", len(got), got)
	}
	if got[0].ID != highRecallID {
		t.Errorf("expected %s, got %s", highRecallID, got[0].ID)
	}
}

// TestCognitiveJobsStore_HighRecallKnowledgeItems_Dedup is the mutation
// candidate for job 4's dedup NOT EXISTS guard (GTD 80cf80b6, PR #152
// 2nd-army finding): a high-recall item that already has a pending
// scheduler:knowledge_to_skill proposal referencing it via
// payload.source_entity_id must NOT be returned again.
func TestCognitiveJobsStore_HighRecallKnowledgeItems_Dedup(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())

	qualifyingID := uuid.New()
	seedCJKnowledgeItem(t, d, wsID, qualifyingID, "qualifying high recall item", 5, false)

	alreadyProposedID := uuid.New()
	seedCJKnowledgeItem(t, d, wsID, alreadyProposedID, "already proposed high recall item", 8, false)
	seedCJPendingKnowledgeProposal(t, d, wsID, "scheduler:knowledge_to_skill", alreadyProposedID.String())

	got, err := store.HighRecallKnowledgeItems(context.Background(), 3)
	if err != nil {
		t.Fatalf("HighRecallKnowledgeItems: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 item (dedup excludes the already-proposed one), got %d: %+v", len(got), got)
	}
	if got[0].ID != qualifyingID {
		t.Errorf("expected qualifying item %s, got %s", qualifyingID, got[0].ID)
	}
}

// ---------------------------------------------------------------------------
// ExpireStaleScheduledProposals (job 5)
// ---------------------------------------------------------------------------

// TestCognitiveJobsStore_ExpireStaleScheduledProposals_SchedulerOnlyGuard is
// the mutation candidate for the proposed_by LIKE 'scheduler:%' safety
// boundary — a user-submitted proposal older than the cutoff must NEVER be
// touched, even though it matches every other predicate.
func TestCognitiveJobsStore_ExpireStaleScheduledProposals_SchedulerOnlyGuard(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())
	old := time.Now().UTC().AddDate(0, 0, -35)

	schedulerID := uuid.New()
	seedCJPendingProposal(t, d, wsID, schedulerID, "scheduler:test-job", old)

	userID := uuid.New()
	seedCJPendingProposal(t, d, wsID, userID, "user", old)

	n, err := store.ExpireStaleScheduledProposals(context.Background(), 30*24*time.Hour, "expired by scheduler")
	if err != nil {
		t.Fatalf("ExpireStaleScheduledProposals: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 row affected (scheduler-owned only), got %d", n)
	}

	schedStatus, schedReason := pendingProposalStatus(t, d, schedulerID)
	if schedStatus != proposalStatusRejected {
		t.Errorf("scheduler proposal: status = %q, want %q", schedStatus, proposalStatusRejected)
	}
	if schedReason == nil || *schedReason != "expired by scheduler" {
		t.Errorf("scheduler proposal: reason = %v, want %q", schedReason, "expired by scheduler")
	}

	userStatus, userReason := pendingProposalStatus(t, d, userID)
	if userStatus != taskStatusPending {
		t.Errorf("user proposal: status = %q, want %q (must NOT be touched)", userStatus, taskStatusPending)
	}
	if userReason != nil {
		t.Errorf("user proposal: reason = %q, want nil (must NOT be touched)", *userReason)
	}
}

func TestCognitiveJobsStore_ExpireStaleScheduledProposals_RespectsRetentionWindow(t *testing.T) {
	wsID := uuid.New()
	d, store := openCognitiveJobsDB(t, wsID.String())

	staleID := uuid.New()
	seedCJPendingProposal(t, d, wsID, staleID, "scheduler:test-job", time.Now().UTC().AddDate(0, 0, -35))

	freshID := uuid.New()
	seedCJPendingProposal(t, d, wsID, freshID, "scheduler:test-job", time.Now().UTC().AddDate(0, 0, -5))

	n, err := store.ExpireStaleScheduledProposals(context.Background(), 30*24*time.Hour, "expired by scheduler")
	if err != nil {
		t.Fatalf("ExpireStaleScheduledProposals: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 row affected (only the stale one), got %d", n)
	}

	if status, _ := pendingProposalStatus(t, d, freshID); status != taskStatusPending {
		t.Errorf("fresh proposal: status = %q, want %q (inside retention window)", status, taskStatusPending)
	}
}

func TestCognitiveJobsStore_ExpireStaleScheduledProposals_EmptyTableNoPanic(t *testing.T) {
	wsID := uuid.New()
	_, store := openCognitiveJobsDB(t, wsID.String())
	n, err := store.ExpireStaleScheduledProposals(context.Background(), 30*24*time.Hour, "expired by scheduler")
	if err != nil {
		t.Fatalf("ExpireStaleScheduledProposals on empty table: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows affected, got %d", n)
	}
}
