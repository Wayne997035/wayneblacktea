package worksession_test

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

var testPgPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	// postgres:16-alpine is intentional — worksession uses minimal DDL,
	// not the full migration stack (no pgvector extension needed).
	c, err := tcpostgres.Run(
		ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	// Apply the work_sessions and work_session_tasks DDL directly.
	// We skip the full migration stack to keep test startup fast; only the
	// tables under test are needed.
	if err := applyWorkSessionSchema(ctx, pool); err != nil {
		log.Printf("apply schema: %v", err)
		return 1
	}

	testPgPool = pool
	return m.Run()
}

// openTestPgPool returns the package-level singleton pool initialised in TestMain.
// Skip with -short flag: testcontainers requires Docker and adds ~5-10 s.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// applyWorkSessionSchema creates the minimal schema required by worksession.Store.
// It mirrors 000021_work_sessions.up.sql + 000022_work_session_tasks.up.sql +
// 000065_work_sessions_evidence.up.sql + 000066_work_session_evidence.up.sql
// plus the tasks table (needed for batchMarkTasksInProgress / batchMarkTasksCompleted).
func applyWorkSessionSchema(ctx context.Context, pool *pgxpool.Pool) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS tasks (
	    id           UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
	    workspace_id UUID,
	    project_id   UUID,
	    title        TEXT    NOT NULL,
	    status       TEXT    NOT NULL DEFAULT 'pending'
	                         CHECK (status IN ('pending','in_progress','completed','cancelled')),
	    priority     INTEGER NOT NULL DEFAULT 3,
	    assignee     TEXT,
	    artifact     TEXT,
	    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS work_sessions (
	    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    workspace_id        UUID        NOT NULL,
	    repo_name           TEXT        NOT NULL,
	    project_id          UUID,
	    title               TEXT        NOT NULL,
	    goal                TEXT        NOT NULL,
	    status              TEXT        NOT NULL CHECK (status IN (
	                            'planned','in_progress','checkpointed',
	                            'completed','cancelled','archived')),
	    source              TEXT        NOT NULL,
	    confirmed_plan_id   UUID,
	    current_task_id     UUID,
	    final_summary       TEXT,
	    started_at          TIMESTAMPTZ,
	    last_checkpoint_at  TIMESTAMPTZ,
	    completed_at        TIMESTAMPTZ,
	    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    context_pack_id              UUID NULL,
	    verification_status          TEXT NULL CHECK (verification_status IN ('not_run','passed','failed','unknown')),
	    verification_command         TEXT NULL,
	    verification_output_excerpt  TEXT NULL,
	    outcome_id                   UUID NULL,
	    final_result                 TEXT NULL CHECK (final_result IN ('success','failure','partial','unknown','regressed')),
	    branch_name                  TEXT NULL
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_one_active
	    ON work_sessions(workspace_id, repo_name)
	    WHERE status = 'in_progress';

	CREATE TABLE IF NOT EXISTS work_session_tasks (
	    session_id  UUID        NOT NULL,
	    task_id     UUID        NOT NULL,
	    role        TEXT        NOT NULL DEFAULT 'primary',
	    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    PRIMARY KEY (session_id, task_id)
	);

	CREATE TABLE IF NOT EXISTS work_session_evidence (
	    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
	    workspace_id   UUID        NULL,
	    session_id     UUID        NOT NULL,
	    evidence_type  TEXT        NOT NULL CHECK (evidence_type IN ('command','pr','ci','railway','manual_note')),
	    status         TEXT        NOT NULL CHECK (status IN ('passed','failed','unknown')),
	    command        TEXT        NULL,
	    artifact       TEXT        NULL,
	    output_excerpt TEXT        NULL,
	    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("apply work session schema: %w", err)
	}
	return nil
}

// insertPgTestTask inserts a minimal tasks row (no assignee) for PG
// integration tests.
func insertPgTestTask(t *testing.T, pool *pgxpool.Pool, wsID uuid.UUID, taskID uuid.UUID) {
	t.Helper()
	const q = `INSERT INTO tasks (id, workspace_id, title, status, priority)
		VALUES ($1,$2,'test task','pending',3)`
	if _, err := pool.Exec(context.Background(), q, taskID, wsID); err != nil {
		t.Fatalf("insertPgTestTask %s: %v", taskID, err)
	}
}

// insertPgTestTaskWithAssignee inserts a tasks row pre-seeded with assignee —
// the P6.8 sibling of insertPgTestTask, used to verify
// batchMarkTasksInProgress preserves an existing assignee instead of
// overwriting it with the session's.
func insertPgTestTaskWithAssignee(t *testing.T, pool *pgxpool.Pool, wsID uuid.UUID, taskID uuid.UUID, assignee string) {
	t.Helper()
	const q = `INSERT INTO tasks (id, workspace_id, title, status, priority, assignee)
		VALUES ($1,$2,'test task','pending',3,$3)`
	if _, err := pool.Exec(context.Background(), q, taskID, wsID, assignee); err != nil {
		t.Fatalf("insertPgTestTaskWithAssignee %s: %v", taskID, err)
	}
}

// queryPgTaskStatus reads the status of a task from Postgres.
func queryPgTaskStatus(t *testing.T, pool *pgxpool.Pool, taskID uuid.UUID) string {
	t.Helper()
	row := pool.QueryRow(context.Background(), `SELECT status FROM tasks WHERE id = $1`, taskID)
	var status string
	if err := row.Scan(&status); err != nil {
		t.Fatalf("queryPgTaskStatus %s: %v", taskID, err)
	}
	return status
}

// queryPgTaskAssignee reads the assignee of a task from Postgres. Returns ""
// for SQL NULL.
func queryPgTaskAssignee(t *testing.T, pool *pgxpool.Pool, taskID uuid.UUID) string {
	t.Helper()
	row := pool.QueryRow(context.Background(), `SELECT COALESCE(assignee, '') FROM tasks WHERE id = $1`, taskID)
	var assignee string
	if err := row.Scan(&assignee); err != nil {
		t.Fatalf("queryPgTaskAssignee %s: %v", taskID, err)
	}
	return assignee
}

// newPgStore returns a postgres-backed Store for the test pool.
// workspaceID nil = no workspace scoping (single-tenant test).
func newPgStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *worksession.Store {
	return worksession.NewStore(pool, workspaceID)
}

// ---- Postgres integration tests ----

func TestPgStore_Create(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "test-repo",
		Title:       "Pg create test",
		Goal:        "Verify Postgres Create",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ID == uuid.Nil {
		t.Error("session ID should be set")
	}
	if sess.Status != statusInProgress {
		t.Errorf("initial status: got %q, want in_progress", sess.Status)
	}
	if sess.RepoName != "test-repo" {
		t.Errorf("repo_name: got %q, want test-repo", sess.RepoName)
	}
}

func TestPgStore_GetActive_ReturnsNilWhenNone(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	result, err := store.GetActive(ctx, wsID, "no-session-repo")
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if result.Active {
		t.Error("expected active=false for empty repo")
	}
	if result.ImplementationAllowed {
		t.Error("expected implementation_allowed=false when no session")
	}
}

func TestPgStore_OneActiveConstraint(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	p := worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "one-active-repo",
		Title:       "First",
		Goal:        "First goal",
		Source:      "test",
	}

	_, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}

	_, err = store.Create(ctx, p)
	if err == nil {
		t.Fatal("expected ErrAlreadyActive, got nil")
	}
	if err != worksession.ErrAlreadyActive {
		t.Errorf("expected ErrAlreadyActive, got %v", err)
	}
}

func TestPgStore_Checkpoint(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "chkpt-repo",
		Title:       "Checkpoint test",
		Goal:        "Test checkpoint",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	chk, err := store.Checkpoint(ctx, worksession.CheckpointParams{
		SessionID: sess.ID,
		Summary:   "halfway",
	})
	if err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
	if chk.Status != statusCheckpointed {
		t.Errorf("status: got %q, want checkpointed", chk.Status)
	}
	if chk.LastCheckpointAt == nil {
		t.Error("last_checkpoint_at should be set")
	}
}

func TestPgStore_Checkpoint_NilWorkspace(t *testing.T) {
	// R1 regression: when workspaceID is nil (no WORKSPACE_ID env),
	// Checkpoint must not use session UUID as workspace filter → 0 rows.
	pool := openTestPgPool(t)
	// Store with nil workspace = single-tenant mode, uuid.Nil as workspace.
	store := newPgStore(pool, nil)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: uuid.Nil,
		RepoName:    "nil-ws-repo",
		Title:       "Nil workspace test",
		Goal:        "R1 regression",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	chk, err := store.Checkpoint(ctx, worksession.CheckpointParams{
		SessionID: sess.ID,
		Summary:   "nil ws checkpoint",
	})
	if err != nil {
		t.Fatalf("Checkpoint with nil workspace: %v (R1 regression — was broken before fix)", err)
	}
	if chk.Status != statusCheckpointed {
		t.Errorf("status: got %q, want checkpointed", chk.Status)
	}
}

func TestPgStore_Finish(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "finish-repo",
		Title:       "Finish test",
		Goal:        "Test finish",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	done, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "all done",
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if done.Status != statusCompleted {
		t.Errorf("status: got %q, want completed", done.Status)
	}
	if done.FinalSummary == nil || *done.FinalSummary != "all done" {
		t.Errorf("final_summary: got %v, want 'all done'", done.FinalSummary)
	}

	// After finish, another Create on same repo should succeed (no active session).
	_, err = store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "finish-repo",
		Title:       "Second session",
		Goal:        "After finish",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create after Finish should succeed, got: %v", err)
	}
}

func TestPgStore_Finish_NilWorkspace(t *testing.T) {
	// R1 regression: Finish with nil workspace must not silently return ErrNotFound.
	pool := openTestPgPool(t)
	store := newPgStore(pool, nil)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: uuid.Nil,
		RepoName:    "nil-ws-finish-repo",
		Title:       "Nil ws finish",
		Goal:        "R1 regression",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	done, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "nil ws done",
	})
	if err != nil {
		t.Fatalf("Finish with nil workspace: %v (R1 regression)", err)
	}
	if done.Status != statusCompleted {
		t.Errorf("status: got %q, want completed", done.Status)
	}
}

func TestPgStore_GetByID(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "getbyid-repo",
		Title:       "GetByID test",
		Goal:        "Test GetByID",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Happy path: correct workspace.
	got, err := store.GetByID(ctx, wsID, sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("ID mismatch: got %s, want %s", got.ID, sess.ID)
	}

	// Non-existent session ID must return ErrNotFound.
	_, err = store.GetByID(ctx, wsID, uuid.New())
	if err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound for unknown session, got %v", err)
	}

	// Cross-workspace isolation: a store scoped to wsB must not see wsA's session.
	wsB := uuid.New()
	storeB := newPgStore(pool, &wsB)
	_, err = storeB.GetByID(ctx, wsB, sess.ID)
	if err != worksession.ErrNotFound {
		t.Errorf("workspace B must not see workspace A's session, got: %v", err)
	}
}

func TestPgStore_LinkTask_And_LinkedTasks(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "link-task-repo",
		Title:       "LinkTask test",
		Goal:        "Test LinkTask",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	taskA := uuid.New()
	taskB := uuid.New()

	if err := store.LinkTask(ctx, sess.ID, taskA, "primary"); err != nil {
		t.Fatalf("LinkTask A: %v", err)
	}
	if err := store.LinkTask(ctx, sess.ID, taskB, "secondary"); err != nil {
		t.Fatalf("LinkTask B: %v", err)
	}

	// Idempotent: linking the same task twice must not error.
	if err := store.LinkTask(ctx, sess.ID, taskA, "primary"); err != nil {
		t.Fatalf("LinkTask A duplicate should be no-op: %v", err)
	}

	tasks, err := store.LinkedTasks(ctx, sess.ID)
	if err != nil {
		t.Fatalf("LinkedTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 linked tasks, got %d", len(tasks))
	}
}

func TestPgStore_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgStore(pool, &wsA)
	storeB := newPgStore(pool, &wsB)
	ctx := context.Background()

	_, err := storeA.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsA,
		RepoName:    "shared-repo",
		Title:       "WS A session",
		Goal:        "A goal",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create WS A: %v", err)
	}

	result, err := storeB.GetActive(ctx, wsB, "shared-repo")
	if err != nil {
		t.Fatalf("GetActive WS B: %v", err)
	}
	if result.Active {
		t.Error("workspace B must not see workspace A's session")
	}

	resultA, err := storeA.GetActive(ctx, wsA, "shared-repo")
	if err != nil {
		t.Fatalf("GetActive WS A: %v", err)
	}
	if !resultA.Active {
		t.Error("workspace A must see its own session")
	}
}

func TestPgStore_Checkpoint_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	_, err := store.Checkpoint(ctx, worksession.CheckpointParams{
		SessionID: uuid.New(),
		Summary:   "ghost",
	})
	if err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestPgStore_Finish_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	_, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: uuid.New(),
		Summary:   "ghost",
	})
	if err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---- task auto-status PG tests ----

// TestPgStartWork_AutoMarksTasksInProgress verifies that Create transitions
// linked tasks from pending → in_progress in Postgres. Assignee is supplied
// (P6.8 gate) so the transition is allowed to happen — the no-assignee reject
// path is covered separately by TestPgStartWork_NoAssigneeAnywhere_TaskStaysPending.
func TestPgStartWork_AutoMarksTasksInProgress(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)
	insertPgTestTask(t, pool, wsID, taskB)

	_, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-auto-in-progress-repo",
		Title:       "PG start work",
		Goal:        "verify in_progress auto-mark",
		Source:      "test",
		TaskIDs:     []uuid.UUID{taskA, taskB},
		Assignee:    assigneeClaude,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusInProgress {
		t.Errorf("taskA status: got %q, want in_progress", got)
	}
	if got := queryPgTaskStatus(t, pool, taskB); got != statusInProgress {
		t.Errorf("taskB status: got %q, want in_progress", got)
	}
}

// ---------------------------------------------------------------------------
// P6.8 — assignee gate on the worksession-start in_progress transition (PG)
// ---------------------------------------------------------------------------

// TestPgStartWork_StampsAssigneeOntoUnassignedTask verifies a session-level
// assignee is stamped onto a linked task that currently has none, at the
// moment it flips pending → in_progress.
func TestPgStartWork_StampsAssigneeOntoUnassignedTask(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)

	_, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-stamp-assignee-repo",
		Title:       "PG stamp assignee",
		Goal:        "verify assignee stamping",
		Source:      "test",
		TaskIDs:     []uuid.UUID{taskA},
		Assignee:    assigneeClaude,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusInProgress {
		t.Errorf("taskA status: got %q, want in_progress", got)
	}
	if got := queryPgTaskAssignee(t, pool, taskA); got != assigneeClaude {
		t.Errorf("taskA assignee: got %q, want stamped %q", got, assigneeClaude)
	}
}

// TestPgStartWork_PreservesExistingAssignee verifies a task that already has
// an assignee keeps it — the session's assignee must NOT overwrite an
// existing owner.
func TestPgStartWork_PreservesExistingAssignee(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	insertPgTestTaskWithAssignee(t, pool, wsID, taskA, assigneeHuman)

	_, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-preserve-assignee-repo",
		Title:       "PG preserve assignee",
		Goal:        "verify existing assignee preserved",
		Source:      "test",
		TaskIDs:     []uuid.UUID{taskA},
		Assignee:    assigneeClaude,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusInProgress {
		t.Errorf("taskA status: got %q, want in_progress", got)
	}
	if got := queryPgTaskAssignee(t, pool, taskA); got != assigneeHuman {
		t.Errorf("taskA assignee: got %q, want preserved %q (not overwritten by session assignee)", got, assigneeHuman)
	}
}

// TestPgStartWork_NoAssigneeAnywhere_TaskStaysPending verifies the reject
// half of the P6.8 gate: a task with no assignee, linked to a session that
// also supplies none, is excluded from the flip entirely and stays pending —
// rather than becoming an ownerless in_progress row.
func TestPgStartWork_NoAssigneeAnywhere_TaskStaysPending(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)

	_, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-no-assignee-anywhere-repo",
		Title:       "PG no assignee anywhere",
		Goal:        "verify reject-not-flip",
		Source:      "test",
		TaskIDs:     []uuid.UUID{taskA},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusPending {
		t.Errorf("taskA status: got %q, want pending (must NOT flip with no assignee anywhere)", got)
	}
	if got := queryPgTaskAssignee(t, pool, taskA); got != "" {
		t.Errorf("taskA assignee: got %q, want empty", got)
	}
}

// TestPgStore_Create_RejectsInvalidAssignee verifies the domain-layer
// defense-in-depth re-validation (mirrors gtd.Store.CreateTask/UpdateTask):
// an unrecognized Assignee value is rejected even though the MCP layer
// (tools_worksession.go/tools_plan.go) already validates on the way in.
func TestPgStore_Create_RejectsInvalidAssignee(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	_, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-invalid-assignee-repo",
		Title:       "PG invalid assignee",
		Goal:        "verify rejection",
		Source:      "test",
		Assignee:    "gemini",
	})
	if err == nil {
		t.Fatal("Create with unrecognized assignee must error")
	}
	if !errors.Is(err, gtd.ErrInvalidAssignee) {
		t.Errorf("expected gtd.ErrInvalidAssignee, got: %v", err)
	}
}

// TestPgStartWork_Idempotent verifies that tasks already in_progress are not
// downgraded when Create links them (WHERE status='pending' guard).
func TestPgStartWork_Idempotent(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)

	// Pre-set to in_progress before calling Create.
	if _, err := pool.Exec(
		ctx,
		`UPDATE tasks SET status = 'in_progress' WHERE id = $1`, taskA,
	); err != nil {
		t.Fatalf("pre-set in_progress: %v", err)
	}

	_, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-idempotent-repo",
		Title:       "PG idempotent",
		Goal:        "verify idempotency",
		Source:      "test",
		TaskIDs:     []uuid.UUID{taskA},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Task was already in_progress; must remain in_progress.
	if got := queryPgTaskStatus(t, pool, taskA); got != statusInProgress {
		t.Errorf("taskA status: got %q, want in_progress (must stay)", got)
	}
}

// TestPgFinishWork_AutoMarksTasksCompleted verifies that Finish marks the
// explicitly provided CompletedTaskIDs as completed in Postgres.
func TestPgFinishWork_AutoMarksTasksCompleted(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)
	insertPgTestTask(t, pool, wsID, taskB)

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-auto-completed-repo",
		Title:       "PG finish work",
		Goal:        "verify completed auto-mark",
		Source:      "test",
		TaskIDs:     []uuid.UUID{taskA, taskB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, _, err = store.Finish(ctx, worksession.FinishParams{
		SessionID:        sess.ID,
		Summary:          "done",
		CompletedTaskIDs: []uuid.UUID{taskA, taskB},
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryPgTaskStatus(t, pool, taskB); got != statusCompleted {
		t.Errorf("taskB status: got %q, want completed", got)
	}
}

// TestPgFinishWork_NoTaskIDs_CompleteAllLinkedTasksOptIn verifies that
// Finish discovers linked tasks via work_session_tasks and marks them all
// completed when CompletedTaskIDs is empty AND the caller opts in via
// CompleteAllLinkedTasks (Ω5 fix — see
// TestPgFinishWork_OmittedCompletedTaskIDsAffectsNone for the default-false,
// no-op case).
func TestPgFinishWork_NoTaskIDs_CompleteAllLinkedTasksOptIn(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)
	insertPgTestTask(t, pool, wsID, taskB)

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-no-task-ids-repo",
		Title:       "PG finish no ids",
		Goal:        "verify fallback completion",
		Source:      "test",
		TaskIDs:     []uuid.UUID{taskA, taskB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Finish with no CompletedTaskIDs but CompleteAllLinkedTasks=true —
	// store discovers linked tasks automatically.
	_, completedIDs, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:              sess.ID,
		Summary:                "done without explicit ids, opted into complete-all",
		CompleteAllLinkedTasks: true,
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if len(completedIDs) != 2 {
		t.Errorf("completedIDs: got %d ids, want 2", len(completedIDs))
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryPgTaskStatus(t, pool, taskB); got != statusCompleted {
		t.Errorf("taskB status: got %q, want completed", got)
	}
}

// TestPgFinishWork_OmittedCompletedTaskIDsAffectsNone is U7's Ω5 bad-case
// red test (Postgres backend): omitting completed_task_ids with
// CompleteAllLinkedTasks left at its false default must complete NEITHER
// linked task.
func TestPgFinishWork_OmittedCompletedTaskIDsAffectsNone(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)
	insertPgTestTask(t, pool, wsID, taskB)

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-omitted-completed-repo",
		Title:       "PG finish omitted ids",
		Goal:        "verify omission completes nothing",
		Source:      "test",
		TaskIDs:     []uuid.UUID{taskA, taskB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, completedIDs, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "done, completed_task_ids omitted, no opt-in",
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if len(completedIDs) != 0 {
		t.Errorf("completedIDs: got %v, want empty (bad case: omission must not silently complete linked tasks)", completedIDs)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got == statusCompleted {
		t.Errorf("taskA must NOT be completed when completed_task_ids is omitted, got %q", got)
	}
	if got := queryPgTaskStatus(t, pool, taskB); got == statusCompleted {
		t.Errorf("taskB must NOT be completed when completed_task_ids is omitted, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2.2 — evidence chain / verification / outcome linkage (Postgres)
// ---------------------------------------------------------------------------

// TestPgStore_Finish_WritesEvidenceColumns mirrors
// TestFinish_WritesEvidenceColumns (SQLite) against real Postgres.
func TestPgStore_Finish_WritesEvidenceColumns(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-evidence-cols-repo",
		Title:       "Evidence cols test",
		Goal:        "Verify Postgres evidence columns",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	outcomeID := uuid.New()
	verifStatus := "passed"
	verifCmd := "cd build && task check"
	verifExcerpt := "0 issues"
	finalResult := "success"
	done, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:                 sess.ID,
		Summary:                   "verified and shipped",
		VerificationStatus:        &verifStatus,
		VerificationCommand:       &verifCmd,
		VerificationOutputExcerpt: &verifExcerpt,
		FinalResult:               &finalResult,
		OutcomeID:                 &outcomeID,
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if done.VerificationStatus == nil || *done.VerificationStatus != "passed" {
		t.Errorf("VerificationStatus: got %v, want passed", done.VerificationStatus)
	}
	if done.FinalResult == nil || *done.FinalResult != "success" {
		t.Errorf("FinalResult: got %v, want success", done.FinalResult)
	}
	if done.OutcomeID == nil || *done.OutcomeID != outcomeID {
		t.Errorf("OutcomeID: got %v, want %s", done.OutcomeID, outcomeID)
	}
}

// TestPgStore_AddEvidence_TruncatesOutputExcerpt mirrors
// TestAddEvidence_TruncatesOutputExcerpt (SQLite) against real Postgres.
func TestPgStore_AddEvidence_TruncatesOutputExcerpt(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-truncate-repo",
		Title:       "Truncate test",
		Goal:        "Verify Postgres truncation",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	longExcerpt := strings.Repeat("a", 2500)
	got, err := store.AddEvidence(ctx, worksession.Evidence{
		SessionID:     sess.ID,
		EvidenceType:  "command",
		Status:        "passed",
		OutputExcerpt: &longExcerpt,
	})
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if got.OutputExcerpt == nil {
		t.Fatal("expected non-nil OutputExcerpt")
	}
	if n := len([]rune(*got.OutputExcerpt)); n != worksession.EvidenceOutputExcerptCap {
		t.Errorf("output_excerpt length: got %d runes, want %d", n, worksession.EvidenceOutputExcerptCap)
	}
}

// TestPgStore_AddEvidence_RedactBeforeCap_CredentialStraddlingBoundary
// mirrors the SQLite test of the same shape against real Postgres, proving
// redact-then-cap ordering holds for both backends.
func TestPgStore_AddEvidence_RedactBeforeCap_CredentialStraddlingBoundary(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    "pg-straddle-repo",
		Title:       "Straddle test",
		Goal:        "Verify Postgres redact-then-cap ordering",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const paddingLen = 1970
	const tokenSuffixLen = 40
	padding := strings.Repeat("x", paddingLen)
	token := "ghp_" + strings.Repeat("b", tokenSuffixLen)
	excerpt := padding + token // token spans runes [1970, 2014) — straddles rune index 2000

	got, err := store.AddEvidence(ctx, worksession.Evidence{
		SessionID:     sess.ID,
		EvidenceType:  "command",
		Status:        "failed",
		OutputExcerpt: &excerpt,
	})
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if got.OutputExcerpt == nil {
		t.Fatal("expected non-nil OutputExcerpt")
	}
	if strings.Contains(*got.OutputExcerpt, token) {
		t.Fatalf("raw token straddling the cap boundary must not survive on Postgres, got: %s", *got.OutputExcerpt)
	}
	if !strings.Contains(*got.OutputExcerpt, "[REDACTED:github-token]") {
		t.Errorf("expected full [REDACTED:github-token] placeholder, got: %s", *got.OutputExcerpt)
	}
}

// TestPgStore_ListRecent_DescOrder mirrors TestListRecent_DescOrder (SQLite)
// against real Postgres.
func TestPgStore_ListRecent_DescOrder(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sessOld, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-recent-old", Title: "Old", Goal: "old goal", Source: "test",
	})
	if err != nil {
		t.Fatalf("Create old: %v", err)
	}
	if _, _, err := store.Finish(ctx, worksession.FinishParams{SessionID: sessOld.ID, Summary: "done"}); err != nil {
		t.Fatalf("Finish old: %v", err)
	}
	sessNew, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-recent-new", Title: "New", Goal: "new goal", Source: "test",
	})
	if err != nil {
		t.Fatalf("Create new: %v", err)
	}
	if _, _, err := store.Finish(ctx, worksession.FinishParams{SessionID: sessNew.ID, Summary: "done"}); err != nil {
		t.Fatalf("Finish new: %v", err)
	}

	// Force deterministic timestamps so DESC ordering isn't flaky under fast
	// sequential inserts within the same clock tick.
	if _, err := pool.Exec(ctx, `UPDATE work_sessions SET created_at = '2020-01-01T00:00:00Z' WHERE id = $1`, sessOld.ID); err != nil {
		t.Fatalf("force old timestamp: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE work_sessions SET created_at = '2020-01-02T00:00:00Z' WHERE id = $1`, sessNew.ID); err != nil {
		t.Fatalf("force new timestamp: %v", err)
	}

	got, err := store.ListRecent(ctx, wsID, "", 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got))
	}
	if got[0].ID != sessNew.ID {
		t.Errorf("expected newest session first, got %s want %s", got[0].ID, sessNew.ID)
	}
	if got[1].ID != sessOld.ID {
		t.Errorf("expected oldest session last, got %s want %s", got[1].ID, sessOld.ID)
	}
}

// TestPgStore_SetOutcomeLink_NotFound mirrors TestSetOutcomeLink_NotFound
// (SQLite) against real Postgres.
func TestPgStore_SetOutcomeLink_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	if err := store.SetOutcomeLink(ctx, uuid.New(), uuid.New()); err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestPgStore_SetOutcomeLink_Success mirrors TestSetOutcomeLink_Success
// (SQLite) against real Postgres.
func TestPgStore_SetOutcomeLink_Success(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-link-outcome-repo", Title: "Link test", Goal: "goal", Source: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	outcomeID := uuid.New()
	if err := store.SetOutcomeLink(ctx, sess.ID, outcomeID); err != nil {
		t.Fatalf("SetOutcomeLink: %v", err)
	}

	got, err := store.GetByID(ctx, wsID, sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.OutcomeID == nil || *got.OutcomeID != outcomeID {
		t.Errorf("OutcomeID: got %v, want %s", got.OutcomeID, outcomeID)
	}
}

// TestPgStore_Create_RejectsControlCharsInBranchName mirrors
// TestCreate_RejectsControlCharsInBranchName (SQLite) against real Postgres.
func TestPgStore_Create_RejectsControlCharsInBranchName(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	branchName := "feature/x\ngit push --force"
	_, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-branch-repo", Title: "t", Goal: "g", Source: "test",
		BranchName: &branchName,
	})
	if err == nil {
		t.Error("expected error for branch_name containing newline, got nil")
	}
}

// TestPgStore_Finish_RejectsControlCharsInVerificationCommand mirrors
// TestFinish_RejectsControlCharsInVerificationCommand (SQLite) against real
// Postgres.
func TestPgStore_Finish_RejectsControlCharsInVerificationCommand(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-verify-cmd-repo", Title: "t", Goal: "g", Source: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	badCmd := "task check\nrm -rf /"
	_, _, err = store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID, Summary: "done", VerificationCommand: &badCmd,
	})
	if err == nil {
		t.Error("expected error for verification_command containing newline, got nil")
	}
}

// TestPgStore_GetEvidence_EmptyReturnsEmptySlice mirrors
// TestGetEvidence_EmptyReturnsEmptySlice (SQLite) against real Postgres.
func TestPgStore_GetEvidence_EmptyReturnsEmptySlice(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-no-evidence-repo", Title: "t", Goal: "g", Source: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	if got == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 evidence rows, got %d", len(got))
	}
}

// TestPgStore_Finish_WithEvidence mirrors TestFinish_WithEvidence (SQLite)
// against real Postgres.
func TestPgStore_Finish_WithEvidence(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-finish-evidence-repo", Title: "t", Goal: "g", Source: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cmd := taskCheckCmd
	pr := "https://github.com/example/repo/pull/1"
	if _, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "done with evidence",
		Evidence: []worksession.EvidenceInput{
			{EvidenceType: "command", Status: "passed", Command: &cmd},
			{EvidenceType: "pr", Status: "passed", Artifact: &pr},
		},
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	evs, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 evidence rows, got %d", len(evs))
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2 review F2 — deferred_task_ids must never be marked completed (PG)
// ---------------------------------------------------------------------------

// TestPgFinishWork_DeferredOnly_ExcludesFromFallback mirrors
// TestFinishWork_DeferredOnly_ExcludesFromFallback (SQLite) against real
// Postgres.
func TestPgFinishWork_DeferredOnly_ExcludesFromFallback(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)
	insertPgTestTask(t, pool, wsID, taskB)

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-deferred-only-repo", Title: "t", Goal: "g", Source: "test",
		TaskIDs: []uuid.UUID{taskA, taskB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:              sess.ID,
		Summary:                "deferred taskB to next session",
		DeferredTaskIDs:        []uuid.UUID{taskB},
		CompleteAllLinkedTasks: true,
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusCompleted {
		t.Errorf("taskA (not deferred) status: got %q, want completed", got)
	}
	if got := queryPgTaskStatus(t, pool, taskB); got == statusCompleted {
		t.Errorf("taskB (deferred) must NOT be completed, got %q", got)
	}
}

// TestPgFinishWork_CompletedAndDeferred_NoOverlap mirrors
// TestFinishWork_CompletedAndDeferred_NoOverlap (SQLite) against real
// Postgres.
func TestPgFinishWork_CompletedAndDeferred_NoOverlap(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)
	insertPgTestTask(t, pool, wsID, taskB)

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-completed-deferred-repo", Title: "t", Goal: "g", Source: "test",
		TaskIDs: []uuid.UUID{taskA, taskB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:        sess.ID,
		Summary:          "taskA done, taskB deferred",
		CompletedTaskIDs: []uuid.UUID{taskA},
		DeferredTaskIDs:  []uuid.UUID{taskB},
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryPgTaskStatus(t, pool, taskB); got == statusCompleted {
		t.Errorf("taskB (deferred) must NOT be completed, got %q", got)
	}
}

// TestPgFinishWork_CompletedDeferredOverlap_DeferredWins mirrors
// TestFinishWork_CompletedDeferredOverlap_DeferredWins (SQLite) against real
// Postgres.
func TestPgFinishWork_CompletedDeferredOverlap_DeferredWins(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertPgTestTask(t, pool, wsID, taskA)
	insertPgTestTask(t, pool, wsID, taskB)

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-overlap-repo", Title: "t", Goal: "g", Source: "test",
		TaskIDs: []uuid.UUID{taskA, taskB},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// taskB is listed in BOTH completed and deferred — deferred must win.
	if _, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:        sess.ID,
		Summary:          "conflicting lists for taskB",
		CompletedTaskIDs: []uuid.UUID{taskA, taskB},
		DeferredTaskIDs:  []uuid.UUID{taskB},
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryPgTaskStatus(t, pool, taskA); got != statusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryPgTaskStatus(t, pool, taskB); got == statusCompleted {
		t.Errorf("taskB (in both completed and deferred) must NOT be completed — deferred must win, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2 review F5 — GetEvidence must be workspace-scoped (PG)
// ---------------------------------------------------------------------------

// TestPgStore_GetEvidence_WorkspaceIsolation mirrors
// TestGetEvidence_WorkspaceIsolation (SQLite) against real Postgres. Both
// stores share the same pool/table, so this genuinely exercises the new
// workspace_id predicate rather than relying on physically separate storage.
// TestPgStore_GetEvidence_NilWorkspace mirrors TestPgStore_Checkpoint_NilWorkspace:
// legacy mode (no WORKSPACE_ID env → nil workspaceID) stamps evidence writes
// with uuid.Nil and GetEvidence's read filter with the same value, so a
// legacy-mode write→read round trip must return the row unfiltered by any
// spurious cross-workspace mismatch. This is the regression-protection
// counterpart to the SQLite parity fix in the same PR (wbt-2.0 security
// backlog) — it proves Postgres's existing zero-UUID equality filter was
// already correct and self-consistent, which SQLite's degenerate
// `?1 IS NULL` predicate was not.
//
// Evidence is attached via Finish (worksession.FinishParams.Evidence), not a
// direct AddEvidence call, because that mirrors the only production call
// path: AddEvidence has exactly two call sites in the whole module and both
// are internal to Finish (Postgres store.go and the SQLite equivalent),
// which explicitly resolves and stamps a zero-value WorkspaceID pointer in
// legacy mode before calling AddEvidence. A direct AddEvidence call that
// omits WorkspaceID falls through to Postgres's ev.WorkspaceID fallback
// (nil), which writes SQL NULL instead of uuid.Nil — a pre-existing,
// out-of-scope edge case for callers that bypass Finish, not exercised by
// any production code path today.
func TestPgStore_GetEvidence_NilWorkspace(t *testing.T) {
	pool := openTestPgPool(t)
	store := newPgStore(pool, nil)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: uuid.Nil,
		RepoName:    "nil-ws-evidence-repo",
		Title:       "Nil workspace evidence test",
		Goal:        "Legacy mode round trip",
		Source:      "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cmd := taskCheckCmd
	if _, _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "nil ws done with evidence",
		Evidence: []worksession.EvidenceInput{
			{EvidenceType: "command", Status: "passed", Command: &cmd},
		},
	}); err != nil {
		t.Fatalf("Finish with nil workspace: %v", err)
	}

	got, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence with nil workspace: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 evidence row in legacy mode round trip, got %d", len(got))
	}
	if got[0].Command == nil || *got[0].Command != cmd {
		t.Errorf("command mismatch: got %v, want %s", got[0].Command, cmd)
	}
}

// TestPgStore_GetEvidence_HardCapsLimit mirrors TestGetEvidence_HardCapsLimit
// (SQLite) against real Postgres: GetEvidence returns at most
// worksession.MaxEvidenceListLimit (100) rows, preserving created_at ASC
// order and dropping the newest row when the cap is hit.
func TestPgStore_GetEvidence_HardCapsLimit(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgStore(pool, &wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID, RepoName: "pg-evidence-cap-repo", Title: "t", Goal: "g", Source: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const seedCount = worksession.MaxEvidenceListLimit + 1
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range seedCount {
		cmd := fmt.Sprintf("cmd-%03d", i)
		ev, err := store.AddEvidence(ctx, worksession.Evidence{
			SessionID:    sess.ID,
			EvidenceType: "command",
			Status:       "passed",
			Command:      &cmd,
		})
		if err != nil {
			t.Fatalf("AddEvidence[%d]: %v", i, err)
		}
		ts := base.Add(time.Duration(i) * time.Second)
		if _, err := pool.Exec(ctx, `UPDATE work_session_evidence SET created_at = $2 WHERE id = $1`, ev.ID, ts); err != nil {
			t.Fatalf("force timestamp[%d]: %v", i, err)
		}
	}

	got, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	if len(got) != worksession.MaxEvidenceListLimit {
		t.Fatalf("expected exactly %d evidence rows (hard cap), got %d", worksession.MaxEvidenceListLimit, len(got))
	}
	if got[0].Command == nil || *got[0].Command != "cmd-000" {
		t.Errorf("expected first row command=cmd-000 (oldest, ASC order), got %v", got[0].Command)
	}
	if got[len(got)-1].Command == nil || *got[len(got)-1].Command != "cmd-099" {
		t.Errorf("expected last row command=cmd-099 (cap drops newest row cmd-100), got %v", got[len(got)-1].Command)
	}
}

func TestPgStore_GetEvidence_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgStore(pool, &wsA)
	storeB := newPgStore(pool, &wsB)
	ctx := context.Background()

	sess, err := storeA.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsA, RepoName: "pg-iso-evidence-repo", Title: "t", Goal: "g", Source: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cmd := taskCheckCmd
	if _, err := storeA.AddEvidence(ctx, worksession.Evidence{
		SessionID:    sess.ID,
		EvidenceType: "command",
		Status:       "passed",
		Command:      &cmd,
	}); err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}

	got, err := storeB.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence (storeB): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("workspace B must not see workspace A's evidence, got %d rows", len(got))
	}

	gotA, err := storeA.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence (storeA): %v", err)
	}
	if len(gotA) != 1 {
		t.Errorf("workspace A should see its own evidence, got %d rows", len(gotA))
	}
}
