package worksession_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// openTestPgPool starts a throwaway Postgres container and returns a
// pgxpool connected to it. The container and pool are cleaned up via t.Cleanup.
//
// Skip with -short flag: testcontainers requires Docker and adds ~5-10 s.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if tErr := container.Terminate(ctx); tErr != nil {
			t.Logf("terminate container: %v", tErr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	// Apply the work_sessions and work_session_tasks DDL directly.
	// We skip the full migration stack to keep test startup fast; only the
	// tables under test are needed.
	if err := applyWorkSessionSchema(ctx, pool); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	return pool
}

// applyWorkSessionSchema creates the minimal schema required by worksession.Store.
// It mirrors 000021_work_sessions.up.sql + 000022_work_session_tasks.up.sql plus
// the tasks table (needed for batchMarkTasksInProgress / batchMarkTasksCompleted).
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
	    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_work_sessions_one_active
	    ON work_sessions(workspace_id, repo_name)
	    WHERE status = 'in_progress';

	CREATE TABLE IF NOT EXISTS work_session_tasks (
	    session_id  UUID        NOT NULL REFERENCES work_sessions(id) ON DELETE CASCADE,
	    task_id     UUID        NOT NULL,
	    role        TEXT        NOT NULL DEFAULT 'primary',
	    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	    PRIMARY KEY (session_id, task_id)
	);
	`
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return fmt.Errorf("apply work session schema: %w", err)
	}
	return nil
}

// insertPgTestTask inserts a minimal tasks row for PG integration tests.
func insertPgTestTask(t *testing.T, pool *pgxpool.Pool, wsID uuid.UUID, taskID uuid.UUID) {
	t.Helper()
	const q = `INSERT INTO tasks (id, workspace_id, title, status, priority)
		VALUES ($1,$2,'test task','pending',3)`
	if _, err := pool.Exec(context.Background(), q, taskID, wsID); err != nil {
		t.Fatalf("insertPgTestTask %s: %v", taskID, err)
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

	done, err := store.Finish(ctx, worksession.FinishParams{
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

	done, err := store.Finish(ctx, worksession.FinishParams{
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

	_, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: uuid.New(),
		Summary:   "ghost",
	})
	if err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---- task auto-status PG tests ----

// TestPgStartWork_AutoMarksTasksInProgress verifies that Create transitions
// linked tasks from pending → in_progress in Postgres.
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
	if _, err := pool.Exec(ctx,
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

	_, err = store.Finish(ctx, worksession.FinishParams{
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

// TestPgFinishWork_NoTaskIDs verifies that Finish discovers linked tasks via
// work_session_tasks and marks them all completed when CompletedTaskIDs is empty.
func TestPgFinishWork_NoTaskIDs(t *testing.T) {
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

	// Finish with no CompletedTaskIDs — store discovers linked tasks automatically.
	_, err = store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "done without explicit ids",
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
