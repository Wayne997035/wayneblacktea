package gtd_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are .up.sql files that MUST NOT be applied by the test
// runner. They contain psql metacommands (`\set`) that pgx (and golang-migrate
// when fed plain SQL) cannot parse. Production handles them via manual
// `psql -f` after substitution. Documented as "NOT AUTO-RUN" in the migration
// file header.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true, // psql `\set` metacommand
}

// openTestPgPool starts a throwaway Postgres container, applies the FULL
// migration stack (including 000026), and returns a pgxpool. The container,
// pool, and applied migrations are torn down via t.Cleanup.
//
// Skip with -short flag: testcontainers requires Docker and adds ~5-10 s.
//
// Critically, this test exercises migration 000026 end-to-end (drop FK
// constraints) — without it the original ON DELETE CASCADE / SET NULL would
// silently mask the cleanup logic in gtd.Store.DeleteTask, producing a green
// test that says nothing about whether the code-side cascade works.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}

	ctx := context.Background()
	// pgvector/pgvector:pg16 is the upstream image with the `vector` extension
	// pre-installed. Migration 000005_knowledge.up.sql does
	// `CREATE EXTENSION vector` so the vanilla `postgres:16-alpine` image
	// fails with `extension "vector" is not available`.
	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
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

	// Apply migrations with a custom runner that skips the documented
	// "NOT AUTO-RUN" psql-metacommand files (see skipMigrations comment).
	// This still applies migration 000026 end-to-end — the whole point of
	// the test is to verify GTDStore.DeleteTask performs the cascade in
	// code, not by leaning on a leftover FK.
	applied := applyAllUpMigrations(t, ctx, pool)
	if !applied["000026_drop_fk_constraints.up.sql"] {
		t.Fatal("migration 000026 was not applied — test would not exercise FK-drop cascade")
	}
	t.Logf("applyAllUpMigrations: applied %d migrations including 000026_drop_fk_constraints.up.sql", len(applied))

	return pool
}

// applyAllUpMigrations executes every *.up.sql file in the embedded
// migrations FS in numeric (filename-sorted) order against pool, skipping the
// known-incompatible files in skipMigrations. Returns the set of applied
// filenames so callers can assert specific migrations ran.
func applyAllUpMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)

	applied := make(map[string]bool, len(ups))
	for _, name := range ups {
		if skipMigrations[name] {
			t.Logf("applyAllUpMigrations: skipping %s (psql-metacommand-only file)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		applied[name] = true
	}
	return applied
}

// newPgGTDStore returns a Postgres-backed gtd.Store scoped to wsID. nil
// workspaceID = unscoped (single-tenant) mode.
func newPgGTDStore(pool *pgxpool.Pool, wsID *uuid.UUID) *gtd.Store {
	return gtd.NewStore(pool, wsID)
}

// TestStore_DeleteTask verifies the basic happy path on Postgres: create →
// delete → no longer listed.
func TestStore_DeleteTask(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "doomed", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := store.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	tasks, err := store.Tasks(ctx, nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("task should be deleted, still listed: %+v", tk)
		}
	}
}

// TestStore_DeleteTask_CascadesIntoWorkSessions verifies the code-level
// replacement for the FK cascades dropped in migration 000026 on Postgres:
//
//   - work_session_tasks.task_id (was ON DELETE CASCADE) → row removed
//   - work_sessions.current_task_id (was ON DELETE SET NULL) → column NULL'd
//
// Mirrors the SQLite-side TestGTDStore_DeleteTask_CascadesIntoWorkSessions.
func TestStore_DeleteTask_CascadesIntoWorkSessions(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "linked-task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Insert a work_session whose current_task_id points at this task, plus
	// a work_session_tasks join row. Hand-rolled INSERT to keep this test
	// focused on the GTD cascade behaviour (and to avoid coupling to the
	// worksession.Store API which has its own tests).
	sessionID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO work_sessions (
		    id, workspace_id, repo_name, project_id, title, goal, status, source,
		    confirmed_plan_id, current_task_id, started_at, created_at, updated_at
		) VALUES (
		    $1, $2, $3, NULL, $4, $5, 'in_progress', 'manual',
		    NULL, $6, NOW(), NOW(), NOW()
		)`,
		sessionID, wsID, "demo-repo", "linked-session", "test cascade", task.ID,
	); err != nil {
		t.Fatalf("insert work_session: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		VALUES ($1, $2, 'primary', NOW())`,
		sessionID, task.ID,
	); err != nil {
		t.Fatalf("insert work_session_tasks: %v", err)
	}

	// Sanity check: link row exists, current_task_id is set.
	var preLinks int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM work_session_tasks WHERE task_id = $1`, task.ID,
	).Scan(&preLinks); err != nil {
		t.Fatalf("pre-count: %v", err)
	}
	if preLinks != 1 {
		t.Fatalf("expected 1 link row before delete, got %d", preLinks)
	}

	// Delete — should cascade into work_session_tasks and NULL current_task_id.
	if err := store.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// Assertion 1: link row is gone (was ON DELETE CASCADE).
	var postLinks int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM work_session_tasks WHERE task_id = $1`, task.ID,
	).Scan(&postLinks); err != nil {
		t.Fatalf("post-count links: %v", err)
	}
	if postLinks != 0 {
		t.Errorf("expected work_session_tasks rows to be deleted, got %d", postLinks)
	}

	// Assertion 2: current_task_id is now NULL (was ON DELETE SET NULL).
	var currentTaskID *uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT current_task_id FROM work_sessions WHERE id = $1`, sessionID,
	).Scan(&currentTaskID); err != nil {
		t.Fatalf("post-read session: %v", err)
	}
	if currentTaskID != nil {
		t.Errorf("expected current_task_id to be NULL after task delete, got %s", currentTaskID)
	}

	// Assertion 3: the task itself is gone.
	tasks, err := store.Tasks(ctx, nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("task should be deleted, still listed: %+v", tk)
		}
	}
}

// TestStore_DeleteTask_NoLinkedRows verifies DeleteTask works when no
// work_session_tasks / work_sessions rows reference the task. The cleanup
// statements should be no-ops.
func TestStore_DeleteTask_NoLinkedRows(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "isolated", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := store.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask without linked rows: %v", err)
	}

	tasks, err := store.Tasks(ctx, nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("task should be deleted, still listed: %+v", tk)
		}
	}
}

// TestStore_DeleteTask_WorkspaceMismatch verifies the workspace pre-check
// inside DeleteTask: a workspace-B caller MUST NOT delete a workspace-A task
// AND MUST NOT touch workspace A's join rows / current_task_id pointer.
// Without the pre-check the cleanup statements (keyed only by task_id) would
// silently erase neighbouring data even though the parent DELETE 0-rowed.
func TestStore_DeleteTask_WorkspaceMismatch(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)
	ctx := context.Background()

	// Create the task in workspace A.
	task, err := storeA.CreateTask(ctx, gtd.CreateTaskParams{Title: "ws-A-task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask in workspace A: %v", err)
	}

	// Workspace-A work_session whose current_task_id points at the task,
	// plus a workspace-A join row. These belong to workspace A and a
	// workspace-B caller must not touch them.
	sessionID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO work_sessions (
		    id, workspace_id, repo_name, project_id, title, goal, status, source,
		    confirmed_plan_id, current_task_id, started_at, created_at, updated_at
		) VALUES (
		    $1, $2, $3, NULL, $4, $5, 'in_progress', 'manual',
		    NULL, $6, NOW(), NOW(), NOW()
		)`,
		sessionID, wsA, "ws-A-repo", "ws-A-session", "test ws-mismatch guard", task.ID,
	); err != nil {
		t.Fatalf("insert work_session: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		VALUES ($1, $2, 'primary', NOW())`,
		sessionID, task.ID,
	); err != nil {
		t.Fatalf("insert work_session_tasks: %v", err)
	}

	// Cross-workspace delete must be a silent no-op (matching the pre-fix
	// "0 rows affected" behaviour).
	if err := storeB.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask cross-workspace must be silent no-op, got: %v", err)
	}

	// Assertion 1: the parent task still exists when read from workspace A.
	var taskCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM tasks WHERE id = $1`, task.ID,
	).Scan(&taskCount); err != nil {
		t.Fatalf("count task: %v", err)
	}
	if taskCount != 1 {
		t.Errorf("expected task to survive cross-workspace delete, got count=%d", taskCount)
	}

	// Assertion 2: the workspace-A join-table row MUST still be present.
	// Without the pre-check the task_id-only cleanup DELETE would erase it.
	var linksRemaining int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM work_session_tasks WHERE task_id = $1`, task.ID,
	).Scan(&linksRemaining); err != nil {
		t.Fatalf("count surviving join rows: %v", err)
	}
	if linksRemaining != 1 {
		t.Errorf("expected workspace A's join row to survive cross-workspace delete, got %d remaining", linksRemaining)
	}

	// Assertion 3: workspace A's work_sessions.current_task_id MUST still
	// point at the original task. Without the pre-check the task_id-only
	// UPDATE would NULL it.
	var currentTaskID *uuid.UUID
	if err := pool.QueryRow(ctx,
		`SELECT current_task_id FROM work_sessions WHERE id = $1`, sessionID,
	).Scan(&currentTaskID); err != nil {
		t.Fatalf("read current_task_id: %v", err)
	}
	if currentTaskID == nil {
		t.Error("workspace A current_task_id was NULL'd by cross-workspace DeleteTask (pre-check missing or broken)")
	} else if *currentTaskID != task.ID {
		t.Errorf("workspace A current_task_id changed unexpectedly: got %s, want %s", currentTaskID, task.ID)
	}
}

// TestStore_DeleteTask_NonExistentID asserts ErrNotFound semantics: deleting
// a UUID that does not exist anywhere is treated as a silent no-op (matching
// the SQLite precedent and the pre-fix Postgres behaviour where the parent
// DELETE simply affected 0 rows).
func TestStore_DeleteTask_NonExistentID(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	err := store.DeleteTask(context.Background(), uuid.New())
	if err != nil && !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected nil or ErrNotFound for unknown task, got: %v", err)
	}
}
