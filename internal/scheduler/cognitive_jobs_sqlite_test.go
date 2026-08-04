package scheduler

// Cross-backend "same fixture" tests for the 4 cognitive jobs with SQLite
// parity (GTD decision G4, 6ea0b014): stuck_task_detection,
// decision_outcome_review, knowledge_to_skill_candidate, proposal_cleanup.
// Each test seeds an equivalent logical fixture into BOTH a real Postgres
// testcontainer (testPgPool, wired by TestMain in
// pending_proposals_prune_pg_test.go) and a fresh SQLite :memory: DB, runs
// the job against each backend, and asserts the two produce equivalent
// results (same proposal count / payload shape — not byte-identical UUIDs,
// which are inherently backend-local).

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// openCrossBackendSQLiteDB opens a fresh in-memory SQLite DB scoped to wsID
// for the same-fixture cross-backend tests below.
func openCrossBackendSQLiteDB(t *testing.T, wsID uuid.UUID) *sqlite.DB {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", wsID.String())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// sqliteTimeArg formats t the way the SQLite schema's TEXT timestamp columns
// expect (RFC3339 with millisecond precision, UTC) — matches
// internal/storage/sqlite's sqliteMillisLayout, duplicated here since that
// constant is unexported and this file lives in a different package.
func sqliteTimeArg(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// ---------------------------------------------------------------------------
// Job 2 — stuck_task_detection
// ---------------------------------------------------------------------------

func TestStuckTaskDetection_CrossBackend_SameFixture(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	const title = "cross-backend stuck task"
	old := time.Now().UTC().AddDate(0, 0, -10)

	// --- Postgres side ---
	pgWsID := uuid.New()
	pgTaskID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO tasks (id, workspace_id, title, status, priority, updated_at)
		VALUES ($1,$2,$3,'in_progress',3,$4)`, pgTaskID, pgWsID, title, old); err != nil {
		t.Fatalf("seed PG task: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", pgTaskID) })

	pgProps := &stubProposalStore{}
	pgSc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps:  &cognitiveDeps{proposal: pgProps, workspaceID: &pgWsID},
	}
	pgSc.runStuckTaskDetection()

	// --- SQLite side ---
	sqWsID := uuid.New()
	sqDB := openCrossBackendSQLiteDB(t, sqWsID)
	sqTaskID := uuid.New()
	if err := sqDB.ExecContext(ctx, `INSERT INTO tasks
		(id, workspace_id, title, status, priority, created_at, updated_at)
		VALUES (?1,?2,?3,'in_progress',3,?4,?4)`,
		sqTaskID.String(), sqWsID.String(), title, sqliteTimeArg(old)); err != nil {
		t.Fatalf("seed SQLite task: %v", err)
	}

	sqProps := &stubProposalStore{}
	sqSc := &Scheduler{
		cognitiveDeps: &cognitiveDeps{
			proposal:    sqProps,
			workspaceID: &sqWsID,
			sqlite:      sqlite.NewCognitiveJobsStore(sqDB),
		},
	}
	sqSc.runStuckTaskDetection()

	if len(pgProps.created) != 1 || len(sqProps.created) != 1 {
		t.Fatalf("expected 1 proposal on each backend, got PG=%d SQLite=%d", len(pgProps.created), len(sqProps.created))
	}

	var pgPayload, sqPayload proposal.TaskPayload
	if err := json.Unmarshal(pgProps.created[0].Payload, &pgPayload); err != nil {
		t.Fatalf("unmarshal PG payload: %v", err)
	}
	if err := json.Unmarshal(sqProps.created[0].Payload, &sqPayload); err != nil {
		t.Fatalf("unmarshal SQLite payload: %v", err)
	}
	if pgPayload.Title != sqPayload.Title {
		t.Errorf("payload title mismatch: PG=%q SQLite=%q", pgPayload.Title, sqPayload.Title)
	}
	if pgPayload.SourceTool != sqPayload.SourceTool {
		t.Errorf("payload source_tool mismatch: PG=%q SQLite=%q", pgPayload.SourceTool, sqPayload.SourceTool)
	}
	if pgProps.proposedBys[0] != sqProps.proposedBys[0] {
		t.Errorf("proposed_by mismatch: PG=%q SQLite=%q", pgProps.proposedBys[0], sqProps.proposedBys[0])
	}
}

// ---------------------------------------------------------------------------
// Job 3 — decision_outcome_review
// ---------------------------------------------------------------------------

func TestDecisionOutcomeReview_CrossBackend_SameFixture(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	const title = "cross-backend decision"
	old := time.Now().UTC().AddDate(0, 0, -45)

	// --- Postgres side ---
	pgWsID := uuid.New()
	pgDecisionID := uuid.New()
	seedOldDecision(t, ctx, pgWsID, pgDecisionID, title, old)

	pgProps := &stubProposalStore{}
	pgSc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps:  &cognitiveDeps{proposal: pgProps, workspaceID: &pgWsID},
	}
	pgSc.runDecisionOutcomeReview()

	// --- SQLite side ---
	sqWsID := uuid.New()
	sqDB := openCrossBackendSQLiteDB(t, sqWsID)
	sqDecisionID := uuid.New()
	if err := sqDB.ExecContext(ctx, `INSERT INTO decisions
		(id, workspace_id, title, context, decision, rationale, created_at)
		VALUES (?1,?2,?3,'ctx','dec','rationale',?4)`,
		sqDecisionID.String(), sqWsID.String(), title, sqliteTimeArg(old)); err != nil {
		t.Fatalf("seed SQLite decision: %v", err)
	}

	sqProps := &stubProposalStore{}
	sqSc := &Scheduler{
		cognitiveDeps: &cognitiveDeps{
			proposal:    sqProps,
			workspaceID: &sqWsID,
			sqlite:      sqlite.NewCognitiveJobsStore(sqDB),
		},
	}
	sqSc.runDecisionOutcomeReview()

	if len(pgProps.created) != 1 || len(sqProps.created) != 1 {
		t.Fatalf("expected 1 proposal on each backend, got PG=%d SQLite=%d", len(pgProps.created), len(sqProps.created))
	}

	var pgPayload, sqPayload proposal.TaskPayload
	if err := json.Unmarshal(pgProps.created[0].Payload, &pgPayload); err != nil {
		t.Fatalf("unmarshal PG payload: %v", err)
	}
	if err := json.Unmarshal(sqProps.created[0].Payload, &sqPayload); err != nil {
		t.Fatalf("unmarshal SQLite payload: %v", err)
	}
	if pgPayload.Title != sqPayload.Title {
		t.Errorf("payload title mismatch: PG=%q SQLite=%q", pgPayload.Title, sqPayload.Title)
	}
	if pgPayload.SourceEntityID != pgDecisionID.String() {
		t.Errorf("PG source_entity_id = %q, want %q", pgPayload.SourceEntityID, pgDecisionID.String())
	}
	if sqPayload.SourceEntityID != sqDecisionID.String() {
		t.Errorf("SQLite source_entity_id = %q, want %q", sqPayload.SourceEntityID, sqDecisionID.String())
	}
}

// ---------------------------------------------------------------------------
// Job 4 — knowledge_to_skill_candidate
// ---------------------------------------------------------------------------

func TestKnowledgeToSkillCandidate_CrossBackend_SameFixture(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	const title = "cross-backend knowledge item"

	// --- Postgres side ---
	pgWsID := uuid.New()
	pgItemID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO knowledge_items
		(id, workspace_id, type, title, content, recall_count)
		VALUES ($1,$2,'article',$3,'body',5)`, pgItemID, pgWsID, title); err != nil {
		t.Fatalf("seed PG knowledge item: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM knowledge_items WHERE id = $1", pgItemID) })

	pgProps := &stubProposalStore{}
	pgSc := &Scheduler{
		disciplinePool: pool,
		cognitiveDeps:  &cognitiveDeps{proposal: pgProps, workspaceID: &pgWsID},
	}
	pgSc.runKnowledgeToSkillCandidate()

	// --- SQLite side ---
	sqWsID := uuid.New()
	sqDB := openCrossBackendSQLiteDB(t, sqWsID)
	sqItemID := uuid.New()
	now := sqliteTimeArg(time.Now())
	if err := sqDB.ExecContext(ctx, `INSERT INTO knowledge_items
		(id, workspace_id, type, title, content, recall_count, created_at, updated_at)
		VALUES (?1,?2,'article',?3,'body',5,?4,?4)`,
		sqItemID.String(), sqWsID.String(), title, now); err != nil {
		t.Fatalf("seed SQLite knowledge item: %v", err)
	}

	sqProps := &stubProposalStore{}
	sqSc := &Scheduler{
		cognitiveDeps: &cognitiveDeps{
			proposal:    sqProps,
			workspaceID: &sqWsID,
			sqlite:      sqlite.NewCognitiveJobsStore(sqDB),
		},
	}
	sqSc.runKnowledgeToSkillCandidate()

	if len(pgProps.created) != 1 || len(sqProps.created) != 1 {
		t.Fatalf("expected 1 proposal on each backend, got PG=%d SQLite=%d", len(pgProps.created), len(sqProps.created))
	}

	var pgPayload, sqPayload proposal.KnowledgePayload
	if err := json.Unmarshal(pgProps.created[0].Payload, &pgPayload); err != nil {
		t.Fatalf("unmarshal PG payload: %v", err)
	}
	if err := json.Unmarshal(sqProps.created[0].Payload, &sqPayload); err != nil {
		t.Fatalf("unmarshal SQLite payload: %v", err)
	}
	if pgPayload.Title != sqPayload.Title {
		t.Errorf("payload title mismatch: PG=%q SQLite=%q", pgPayload.Title, sqPayload.Title)
	}
	if pgProps.created[0].Type != string(proposal.TypeKnowledge) || sqProps.created[0].Type != string(proposal.TypeKnowledge) {
		t.Errorf("expected type=%q on both backends, got PG=%q SQLite=%q",
			proposal.TypeKnowledge, pgProps.created[0].Type, sqProps.created[0].Type)
	}
}

// ---------------------------------------------------------------------------
// Job 5 — proposal_cleanup
// ---------------------------------------------------------------------------

func TestProposalCleanup_CrossBackend_SameFixture(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	ctx := context.Background()
	old := time.Now().UTC().AddDate(0, 0, -35)

	// --- Postgres side ---
	pgWsID := uuid.New()
	pgSchedID := uuid.New()
	pgUserID := uuid.New()
	const insertPG = `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, status, proposed_by, created_at)
		VALUES ($1,$2,'knowledge','{"title":"t","content":"c"}','pending',$3,$4)`
	if _, err := pool.Exec(ctx, insertPG, pgSchedID, pgWsID, "scheduler:test-job", old); err != nil {
		t.Fatalf("seed PG scheduler proposal: %v", err)
	}
	if _, err := pool.Exec(ctx, insertPG, pgUserID, pgWsID, "user", old); err != nil {
		t.Fatalf("seed PG user proposal: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = ANY($1::uuid[])", []uuid.UUID{pgSchedID, pgUserID})
	})

	pgSc := &Scheduler{disciplinePool: pool}
	pgSc.runProposalCleanup()

	var pgSchedStatus, pgUserStatus string
	if err := pool.QueryRow(ctx, "SELECT status FROM pending_proposals WHERE id = $1", pgSchedID).Scan(&pgSchedStatus); err != nil {
		t.Fatalf("query PG scheduler proposal: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM pending_proposals WHERE id = $1", pgUserID).Scan(&pgUserStatus); err != nil {
		t.Fatalf("query PG user proposal: %v", err)
	}

	// --- SQLite side ---
	sqWsID := uuid.New()
	sqDB := openCrossBackendSQLiteDB(t, sqWsID)
	sqSchedID := uuid.New()
	sqUserID := uuid.New()
	const insertSQ = `INSERT INTO pending_proposals
		(id, workspace_id, type, payload, status, proposed_by, created_at)
		VALUES (?1,?2,'knowledge','{"title":"t","content":"c"}','pending',?3,?4)`
	if err := sqDB.ExecContext(ctx, insertSQ, sqSchedID.String(), sqWsID.String(), "scheduler:test-job", sqliteTimeArg(old)); err != nil {
		t.Fatalf("seed SQLite scheduler proposal: %v", err)
	}
	if err := sqDB.ExecContext(ctx, insertSQ, sqUserID.String(), sqWsID.String(), "user", sqliteTimeArg(old)); err != nil {
		t.Fatalf("seed SQLite user proposal: %v", err)
	}

	sqSc := &Scheduler{
		cognitiveDeps: &cognitiveDeps{sqlite: sqlite.NewCognitiveJobsStore(sqDB)},
	}
	sqSc.runProposalCleanup()

	var sqSchedStatus, sqUserStatus string
	if err := sqDB.QueryRowContext(ctx, "SELECT status FROM pending_proposals WHERE id = ?1", sqSchedID.String()).Scan(&sqSchedStatus); err != nil {
		t.Fatalf("query SQLite scheduler proposal: %v", err)
	}
	if err := sqDB.QueryRowContext(ctx, "SELECT status FROM pending_proposals WHERE id = ?1", sqUserID.String()).Scan(&sqUserStatus); err != nil {
		t.Fatalf("query SQLite user proposal: %v", err)
	}

	if pgSchedStatus != "rejected" || sqSchedStatus != "rejected" {
		t.Errorf("expected scheduler proposal rejected on both backends, got PG=%q SQLite=%q", pgSchedStatus, sqSchedStatus)
	}
	if pgUserStatus != "pending" || sqUserStatus != "pending" {
		t.Errorf("expected user proposal untouched on both backends, got PG=%q SQLite=%q", pgUserStatus, sqUserStatus)
	}
}
