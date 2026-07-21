package gtd_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// timeAgoHours returns now - h hours; negative h returns a future time.
// Used by integration tests to build since-window arguments.
func timeAgoHours(h float64) time.Time {
	return time.Now().Add(-time.Duration(h * float64(time.Hour)))
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
	if _, err := pool.Exec(
		ctx, `
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
	if _, err := pool.Exec(
		ctx, `
		INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		VALUES ($1, $2, 'primary', NOW())`,
		sessionID, task.ID,
	); err != nil {
		t.Fatalf("insert work_session_tasks: %v", err)
	}

	// Sanity check: link row exists, current_task_id is set.
	var preLinks int
	if err := pool.QueryRow(
		ctx,
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
	if err := pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM work_session_tasks WHERE task_id = $1`, task.ID,
	).Scan(&postLinks); err != nil {
		t.Fatalf("post-count links: %v", err)
	}
	if postLinks != 0 {
		t.Errorf("expected work_session_tasks rows to be deleted, got %d", postLinks)
	}

	// Assertion 2: current_task_id is now NULL (was ON DELETE SET NULL).
	var currentTaskID *uuid.UUID
	if err := pool.QueryRow(
		ctx,
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
	if _, err := pool.Exec(
		ctx, `
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
	if _, err := pool.Exec(
		ctx, `
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
	if err := pool.QueryRow(
		ctx,
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
	if err := pool.QueryRow(
		ctx,
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
	if err := pool.QueryRow(
		ctx,
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

// TestStore_TopPendingTask verifies ordering and nil return on Postgres.
func TestStore_TopPendingTask(t *testing.T) {
	t.Run("multiple pending tasks → returns lowest priority then importance", func(t *testing.T) {
		pool := openTestPgPool(t)
		wsID := uuid.New()
		store := newPgGTDStore(pool, &wsID)
		ctx := context.Background()

		imp2 := int16(2)
		imp1 := int16(1)
		_, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "low-prio", Priority: 5, Importance: &imp2})
		if err != nil {
			t.Fatalf("CreateTask low-prio: %v", err)
		}
		top, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "top-prio", Priority: 1, Importance: &imp1})
		if err != nil {
			t.Fatalf("CreateTask top-prio: %v", err)
		}
		_, err = store.CreateTask(ctx, gtd.CreateTaskParams{Title: "mid-prio", Priority: 3, Importance: &imp2})
		if err != nil {
			t.Fatalf("CreateTask mid-prio: %v", err)
		}

		got, err := store.TopPendingTask(ctx)
		if err != nil {
			t.Fatalf("TopPendingTask: %v", err)
		}
		if got == nil {
			t.Fatal("expected a task, got nil")
		}
		if got.ID != top.ID {
			t.Errorf("expected task ID %s (priority 1), got %s (title: %q)", top.ID, got.ID, got.Title)
		}
	})

	t.Run("no pending tasks → nil, nil", func(t *testing.T) {
		pool := openTestPgPool(t)
		wsID := uuid.New()
		store := newPgGTDStore(pool, &wsID)
		ctx := context.Background()

		// Create a task and complete it — pending set should be empty.
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "done", Priority: 1})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := store.CompleteTask(ctx, task.ID, nil); err != nil {
			t.Fatalf("CompleteTask: %v", err)
		}

		got, err := store.TopPendingTask(ctx)
		if err != nil {
			t.Fatalf("TopPendingTask: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for empty pending set, got: %+v", got)
		}
	})
}

// TestStore_TasksByProjectAllStatuses_ReturnsPendingAndCompleted exercises
// the new all-statuses query against real Postgres so the SQL parses, the
// COALESCE ordering is honoured by the Postgres planner, and the default
// Tasks path stays active-only. Counterpart to the SQLite test — required by
// backend-security-design.md §6.5 (dual-backend integration parity).
func TestStore_TasksByProjectAllStatuses_ReturnsPendingAndCompleted(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	project, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "demo-pg", Title: "Demo PG", Area: "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	open, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "still open", Priority: 3, ProjectID: &project.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask open: %v", err)
	}
	done, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "shipped", Priority: 3, ProjectID: &project.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask done: %v", err)
	}
	if _, err := store.CompleteTask(ctx, done.ID, nil); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	all, err := store.TasksByProjectAllStatuses(ctx, project.ID)
	if err != nil {
		t.Fatalf("TasksByProjectAllStatuses: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 tasks, got %d (%+v)", len(all), all)
	}
	statuses := map[string]bool{}
	for _, tk := range all {
		statuses[tk.Status] = true
	}
	if !statuses["pending"] || !statuses["completed"] {
		t.Errorf("expected both pending and completed in result, got %+v", statuses)
	}

	// Default Tasks(...) MUST still be active-only — same regression guard as
	// the SQLite test, but pinned for the Postgres planner / driver path.
	active, err := store.Tasks(ctx, &project.ID)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(active) != 1 || active[0].ID != open.ID {
		t.Errorf("Tasks default path leaked completed rows: %+v", active)
	}
}

// TestStore_TasksByProjectAllStatuses_WorkspaceMismatch confirms strict
// per-workspace scope on Postgres: a request from workspace B for workspace
// A's project_id MUST return 0 rows.
func TestStore_TasksByProjectAllStatuses_WorkspaceMismatch(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)
	ctx := context.Background()

	project, err := storeA.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "ws-a-proj-" + wsA.String()[:8], Title: "WS A", Area: "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := storeA.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "in scope", Priority: 3, ProjectID: &project.ID,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := storeB.TasksByProjectAllStatuses(ctx, project.ID)
	if err != nil {
		t.Fatalf("TasksByProjectAllStatuses (ws B): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 tasks for cross-workspace read, got %d", len(got))
	}
}

// TestStore_TasksByProjectAllStatuses_EmptyProject pins the empty-result path
// on Postgres: a project with no tasks returns an empty slice (not an error).
func TestStore_TasksByProjectAllStatuses_EmptyProject(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	project, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "empty-pg", Title: "Empty PG", Area: "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := store.TasksByProjectAllStatuses(ctx, project.ID)
	if err != nil {
		t.Fatalf("TasksByProjectAllStatuses: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %+v", got)
	}
}

// TestStore_RecentCompletedTasks_PG verifies recently-completed task ordering
// + workspace scoping on the actual Postgres backend (testcontainers, no mock).
// Per backend-security-design.md §6.5, dual-backend stores require BOTH the
// SQLite test (in storage/sqlite/gtd_test.go) AND this PG testcontainers test.
func TestStore_RecentCompletedTasks_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "rct-pg", Title: "RCT PG", Area: "engineering"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	t1, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "t1", ProjectID: &proj.ID, Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask t1: %v", err)
	}
	t2, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "t2", ProjectID: &proj.ID, Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask t2: %v", err)
	}
	if _, err := store.CompleteTask(ctx, t1.ID, nil); err != nil {
		t.Fatalf("CompleteTask t1: %v", err)
	}
	if _, err := store.CompleteTask(ctx, t2.ID, nil); err != nil {
		t.Fatalf("CompleteTask t2: %v", err)
	}

	got, err := store.RecentCompletedTasks(ctx, proj.ID, 50)
	if err != nil {
		t.Fatalf("RecentCompletedTasks: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 completed tasks, got %d", len(got))
	}

	// Cross-workspace caller must see zero rows.
	otherWS := uuid.New()
	otherStore := newPgGTDStore(pool, &otherWS)
	otherGot, err := otherStore.RecentCompletedTasks(ctx, proj.ID, 50)
	if err != nil {
		t.Fatalf("RecentCompletedTasks (other workspace): %v", err)
	}
	if len(otherGot) != 0 {
		t.Errorf("expected 0 tasks for other workspace, got %d", len(otherGot))
	}

	// Limit caps result.
	limited, err := store.RecentCompletedTasks(ctx, proj.ID, 1)
	if err != nil {
		t.Fatalf("RecentCompletedTasks limit=1: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("expected 1 task with limit=1, got %d", len(limited))
	}
}

// TestStore_RecentActivityByProject_PG verifies project + since-window
// filtering and workspace scoping on Postgres.
func TestStore_RecentActivityByProject_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "rabp-pg", Title: "RABP PG", Area: "engineering"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	if err := store.LogActivity(ctx, "tester", "log_decision", &proj.ID, "first"); err != nil {
		t.Fatalf("LogActivity 1: %v", err)
	}
	if err := store.LogActivity(ctx, "tester", "complete_task", &proj.ID, "second"); err != nil {
		t.Fatalf("LogActivity 2: %v", err)
	}

	since := timeAgoHours(1)
	got, err := store.RecentActivityByProject(ctx, proj.ID, since, 50)
	if err != nil {
		t.Fatalf("RecentActivityByProject: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 activity rows, got %d", len(got))
	}

	// Future since → 0 rows.
	future := timeAgoHours(-1)
	gotFuture, err := store.RecentActivityByProject(ctx, proj.ID, future, 50)
	if err != nil {
		t.Fatalf("RecentActivityByProject future: %v", err)
	}
	if len(gotFuture) != 0 {
		t.Errorf("expected 0 rows for future since, got %d", len(gotFuture))
	}

	// Cross-workspace caller must see zero rows.
	otherWS := uuid.New()
	otherStore := newPgGTDStore(pool, &otherWS)
	otherGot, err := otherStore.RecentActivityByProject(ctx, proj.ID, since, 50)
	if err != nil {
		t.Fatalf("RecentActivityByProject other workspace: %v", err)
	}
	if len(otherGot) != 0 {
		t.Errorf("expected 0 rows for other workspace, got %d", len(otherGot))
	}
}

// pgMustCreateTask is a tiny helper that keeps the TasksByDueDateRange
// subtests focused on assertions and lets the outer function fit under the
// gocyclo budget.
func pgMustCreateTask(t *testing.T, store *gtd.Store, title string, due *time.Time) {
	t.Helper()
	_, err := store.CreateTask(context.Background(), gtd.CreateTaskParams{
		Title: title, Priority: 3, DueDate: due,
	})
	if err != nil {
		t.Fatalf("CreateTask %s: %v", title, err)
	}
}

func pgMustQueryDueRange(t *testing.T, store *gtd.Store, from, to time.Time) []db.Task {
	t.Helper()
	got, err := store.TasksByDueDateRange(context.Background(), from, to)
	if err != nil {
		t.Fatalf("TasksByDueDateRange: %v", err)
	}
	return got
}

// TestStore_TasksByDueDateRange exercises the calendar-planning query on
// real Postgres (testcontainers). Mirrors the SQLite-side
// TestGTDStore_TasksByDueDateRange — keeping both in sync per
// backend-security-design.md §6.5 (dual-backend integration parity).
func TestStore_TasksByDueDateRange(t *testing.T) {
	pool := openTestPgPool(t)
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	weekEnd := now.Add(7 * 24 * time.Hour)

	t.Run("returns pending tasks with due_date inside range, ordered by due_date ASC", func(t *testing.T) {
		wsID := uuid.New()
		store := newPgGTDStore(pool, &wsID)
		dueLater := now.Add(5 * 24 * time.Hour)
		dueSooner := now.Add(2 * 24 * time.Hour)
		pgMustCreateTask(t, store, "later", &dueLater)
		pgMustCreateTask(t, store, "sooner", &dueSooner)

		got := pgMustQueryDueRange(t, store, now, weekEnd)
		if len(got) != 2 {
			t.Fatalf("want 2, got %d: %+v", len(got), got)
		}
		if got[0].Title != "sooner" || got[1].Title != "later" {
			t.Errorf("want order [sooner, later], got [%s, %s]", got[0].Title, got[1].Title)
		}
	})

	t.Run("excludes due_date outside range and tasks with NULL due_date", func(t *testing.T) {
		wsID := uuid.New()
		store := newPgGTDStore(pool, &wsID)
		insideDue := now.Add(2 * 24 * time.Hour)
		outsideAfter := now.Add(30 * 24 * time.Hour)
		pgMustCreateTask(t, store, "inside", &insideDue)
		pgMustCreateTask(t, store, "after", &outsideAfter)
		pgMustCreateTask(t, store, "no-due", nil)

		got := pgMustQueryDueRange(t, store, now, weekEnd)
		if len(got) != 1 || got[0].Title != "inside" {
			t.Errorf("want [inside], got %+v", got)
		}
	})

	t.Run("excludes completed tasks even when due_date is in range", func(t *testing.T) {
		wsID := uuid.New()
		store := newPgGTDStore(pool, &wsID)
		ctx := context.Background()
		due := now.Add(2 * 24 * time.Hour)
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "to-complete", Priority: 3, DueDate: &due})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := store.CompleteTask(ctx, task.ID, nil); err != nil {
			t.Fatalf("CompleteTask: %v", err)
		}
		got := pgMustQueryDueRange(t, store, now, weekEnd)
		if len(got) != 0 {
			t.Errorf("want 0 (completed excluded), got %d", len(got))
		}
	})

	t.Run("workspace isolation in shared DB", func(t *testing.T) {
		// Both stores share the SAME pgxpool / DB schema (real cross-tenant
		// scenario), so workspace scoping is the ONLY thing keeping them
		// apart. SQLite tests use separate :memory: DBs and so cannot exercise
		// this code path the same way.
		wsA := uuid.New()
		wsB := uuid.New()
		storeA := newPgGTDStore(pool, &wsA)
		storeB := newPgGTDStore(pool, &wsB)
		due := now.Add(2 * 24 * time.Hour)
		pgMustCreateTask(t, storeA, "isolated-A", &due)

		gotA := pgMustQueryDueRange(t, storeA, now, weekEnd)
		// May contain tasks from earlier subtests in this PG session; just
		// verify "isolated-A" is in there.
		found := false
		for _, tk := range gotA {
			if tk.Title == "isolated-A" {
				found = true
			}
		}
		if !found {
			t.Errorf("want 'isolated-A' in workspace A results, got %+v", gotA)
		}

		gotB := pgMustQueryDueRange(t, storeB, now, weekEnd)
		for _, tk := range gotB {
			if tk.Title == "isolated-A" {
				t.Errorf("workspace B leaked task from workspace A: %+v", tk)
			}
		}
	})
}

// TestStore_ProjectsByRepoName_PG pins the new repo↔project lookup against
// real Postgres (testcontainers). Required by backend-security-design §6.5:
// any new dialect-specific store query MUST have a PG testcontainers test
// in addition to the handler-level fake-store test in
// workspace_overview_handler_test.go. Sprint #92 (PR #92) added this query
// without a PG test; this case closes the gap.
func TestStore_ProjectsByRepoName_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	// Empty input → nil fast-path (early-return guard).
	got, err := store.ProjectsByRepoName(ctx, "")
	if err != nil {
		t.Fatalf("ProjectsByRepoName empty: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty repo name, got %+v", got)
	}

	// Two projects bound to the same repo, distinct priorities. repo_name
	// is set via raw UPDATE because CreateProjectParams does not yet
	// expose the field — matches migration 000037 comment ("populated by
	// hand-rolled UPDATE post-migration").
	p1, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "alpha", Title: "Alpha", Priority: 2,
	})
	if err != nil {
		t.Fatalf("CreateProject p1: %v", err)
	}
	p2, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "beta", Title: "Beta", Priority: 1,
	})
	if err != nil {
		t.Fatalf("CreateProject p2: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE projects SET repo_name = $1 WHERE id IN ($2, $3)`,
		"wayneblacktea", p1.ID, p2.ID,
	); err != nil {
		t.Fatalf("UPDATE repo_name: %v", err)
	}

	got, err = store.ProjectsByRepoName(ctx, "wayneblacktea")
	if err != nil {
		t.Fatalf("ProjectsByRepoName matched: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 projects, got %d (%+v)", len(got), got)
	}
	// ORDER BY priority ASC — beta (1) before alpha (2).
	if got[0].ID != p2.ID || got[1].ID != p1.ID {
		t.Errorf("ordering wrong: got [%s, %s], want [%s, %s]",
			got[0].ID, got[1].ID, p2.ID, p1.ID)
	}

	// Unmatched repo → empty slice, no error.
	got, err = store.ProjectsByRepoName(ctx, "no-such-repo")
	if err != nil {
		t.Fatalf("ProjectsByRepoName unmatched: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 for unmatched repo, got %d", len(got))
	}
}

// TestStore_ProjectsByRepoName_WorkspaceMismatch_PG confirms the disjoint
// `($2::uuid IS NULL OR workspace_id = $2)` clause enforces strict
// per-workspace scoping on Postgres: workspace B asking for a repo bound
// to workspace A's project MUST receive 0 rows. Mirrors the
// TasksByProjectAllStatuses_WorkspaceMismatch pattern.
func TestStore_ProjectsByRepoName_WorkspaceMismatch_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)
	ctx := context.Background()

	pA, err := storeA.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "ws-a-proj-" + wsA.String()[:8], Title: "WS A",
	})
	if err != nil {
		t.Fatalf("CreateProject pA: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE projects SET repo_name = $1 WHERE id = $2`,
		"shared-repo", pA.ID,
	); err != nil {
		t.Fatalf("UPDATE repo_name: %v", err)
	}

	gotA, err := storeA.ProjectsByRepoName(ctx, "shared-repo")
	if err != nil || len(gotA) != 1 {
		t.Fatalf("storeA ProjectsByRepoName: got %d projects, err %v", len(gotA), err)
	}

	gotB, err := storeB.ProjectsByRepoName(ctx, "shared-repo")
	if err != nil {
		t.Fatalf("storeB ProjectsByRepoName: %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("workspace B leaked workspace A projects: %+v", gotB)
	}
}

// readRepoName fetches projects.repo_name for id; t.Fatal on query error.
// Returns nil pointer when the column is NULL.
func readRepoName(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, label string) *string {
	t.Helper()
	var repoName *string
	if err := pool.QueryRow(
		ctx,
		`SELECT repo_name FROM projects WHERE id = $1`, id,
	).Scan(&repoName); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	return repoName
}

// execMigrationFile reads name from the embedded migrations FS and runs it
// against pool. Wraps both the read and the exec in t.Fatal for brevity.
func execMigrationFile(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) {
	t.Helper()
	body, err := migrationfs.FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

// TestMigration000039_BackfillProjectsRepoName_PG verifies migration 000039
// (backfill projects.repo_name = 'wayneblacktea' WHERE name = 'wbt-core-mvp')
// behaves as the production single-tenant deploy needs:
//
//   - happy path: a wbt-core-mvp project gets repo_name='wayneblacktea' bound
//     after re-running the migration, even though the migration was already
//     applied against an empty table during openTestPgPool's full-stack apply
//   - idempotency: re-running it a second time leaves the value unchanged
//     (UPDATE ... = same value is a safe no-op)
//   - scoping: other projects (different name) are untouched and remain NULL,
//     so the workspace_overview_handler.go fallback to ProjectByName still
//     applies for non-wbt-core-mvp projects
//
// applyAllUpMigrations has already executed 000039 against the freshly-built
// container before this test starts, so we re-execute the same .up.sql body
// via raw pool.Exec to assert the behaviour against seeded data. This is
// safe because the migration is documented (and proven below) to be idempotent.
func TestMigration000039_BackfillProjectsRepoName_PG(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	// Seed the canonical project mirroring production's row plus one unrelated
	// project to assert scoping (must remain NULL repo_name).
	canonical, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "wbt-core-mvp", Title: "Wayneblacktea Core MVP", Priority: 1,
	})
	if err != nil {
		t.Fatalf("CreateProject wbt-core-mvp: %v", err)
	}
	other, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "unrelated-project", Title: "Unrelated", Priority: 3,
	})
	if err != nil {
		t.Fatalf("CreateProject unrelated: %v", err)
	}

	// Pre-condition: both projects start with NULL repo_name. CreateProject
	// does not set the column so this should always hold; if it doesn't, the
	// test below is meaningless.
	for _, id := range []uuid.UUID{canonical.ID, other.ID} {
		if got := readRepoName(t, ctx, pool, id, "pre-check"); got != nil {
			t.Fatalf("pre-check: project %s already has repo_name=%q, expected NULL", id, *got)
		}
	}

	// Apply 000039 from the embedded FS verbatim so the test asserts on the
	// real migration source, not a paraphrase. Then verify post-conditions.
	execMigrationFile(t, ctx, pool, "000039_backfill_projects_repo_name.up.sql")

	// Assertion 1: canonical project bound to 'wayneblacktea'.
	if got := readRepoName(t, ctx, pool, canonical.ID, "post-check canonical"); got == nil || *got != "wayneblacktea" {
		t.Errorf("canonical repo_name = %v, want \"wayneblacktea\"", got)
	}

	// Assertion 2: unrelated project untouched (still NULL).
	if got := readRepoName(t, ctx, pool, other.ID, "post-check other"); got != nil {
		t.Errorf("unrelated project repo_name = %q, want NULL", *got)
	}

	// Assertion 3: idempotency — second apply leaves the value unchanged.
	execMigrationFile(t, ctx, pool, "000039_backfill_projects_repo_name.up.sql")
	if got := readRepoName(t, ctx, pool, canonical.ID, "idempotency post-check"); got == nil || *got != "wayneblacktea" {
		t.Errorf("after re-apply canonical repo_name = %v, want \"wayneblacktea\"", got)
	}

	// Assertion 4: down-migration restores NULL only for the bound row.
	execMigrationFile(t, ctx, pool, "000039_backfill_projects_repo_name.down.sql")
	if got := readRepoName(t, ctx, pool, canonical.ID, "post-down-check"); got != nil {
		t.Errorf("after down migration canonical repo_name = %q, want NULL", *got)
	}
}

// assertContainsTask asserts that taskID appears in got; used by
// TestStore_TasksForTimeline subtests to keep each case under the gocyclo limit.
func assertContainsTask(t *testing.T, got []db.Task, taskID uuid.UUID, label string) {
	t.Helper()
	for _, tk := range got {
		if tk.ID == taskID {
			return
		}
	}
	t.Errorf("%s: task %s not found among %d rows", label, taskID, len(got))
}

// assertNotContainsTask asserts that taskID does NOT appear in got.
func assertNotContainsTask(t *testing.T, got []db.Task, taskID uuid.UUID, label string) {
	t.Helper()
	for _, tk := range got {
		if tk.ID == taskID {
			t.Errorf("%s: task %s should be excluded but was found", label, taskID)
			return
		}
	}
}

// pgTimelineRange is a now-relative window [now-30d, now+1d] used by all
// TestStore_TasksForTimeline* subtests. It must be relative (not a fixed
// month) so tasks stamped with time.Now() by CreateTask/CompleteTask always
// fall inside the range regardless of wall-clock date; the april2026 back-date
// stays before the window start.
var pgTimelineRange = func() [2]time.Time {
	now := time.Now().UTC()
	return [2]time.Time{
		now.AddDate(0, 0, -30),
		now.AddDate(0, 0, 1),
	}
}()

// april2026 is a back-date outside the May range used by AC §2 and §4.
var april2026 = time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

// TestStore_TasksForTimeline_AC1_CreatedAndCompletedInRange verifies acceptance
// criterion §1: task created AND completed inside [from,to] → row returned
// (aggregator emits task_created + task_completed from it).
func TestStore_TasksForTimeline_AC1_CreatedAndCompletedInRange(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()
	may1, may31 := pgTimelineRange[0], pgTimelineRange[1]

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg-ac1", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.CompleteTask(ctx, task.ID, nil); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	got, err := store.TasksForTimeline(ctx, may1, may31)
	if err != nil {
		t.Fatalf("TasksForTimeline: %v", err)
	}
	assertContainsTask(t, got, task.ID, "AC1")
}

// TestStore_TasksForTimeline_AC2_CreatedBeforeCompletedInside verifies acceptance
// criterion §2: task created before [from], completed inside [from,to] → row
// returned via the updated_at branch.
func TestStore_TasksForTimeline_AC2_CreatedBeforeCompletedInside(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()
	may1, may31 := pgTimelineRange[0], pgTimelineRange[1]

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg-ac2", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE tasks SET created_at = $1 WHERE id = $2`, april2026, task.ID,
	); err != nil {
		t.Fatalf("back-date created_at: %v", err)
	}
	if _, err := store.CompleteTask(ctx, task.ID, nil); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	got, err := store.TasksForTimeline(ctx, may1, may31)
	if err != nil {
		t.Fatalf("TasksForTimeline: %v", err)
	}
	assertContainsTask(t, got, task.ID, "AC2 (completed inside range via updated_at)")
}

// TestStore_TasksForTimeline_AC3_PendingCreatedInside verifies acceptance
// criterion §3: pending task created inside [from,to] → row returned.
func TestStore_TasksForTimeline_AC3_PendingCreatedInside(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()
	may1, may31 := pgTimelineRange[0], pgTimelineRange[1]

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg-ac3-pending", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	got, err := store.TasksForTimeline(ctx, may1, may31)
	if err != nil {
		t.Fatalf("TasksForTimeline: %v", err)
	}
	if len(got) != 1 || got[0].ID != task.ID {
		t.Errorf("want 1 pending task %s, got %d rows: %+v", task.ID, len(got), got)
	}
}

// TestStore_TasksForTimeline_AC4_PendingCreatedBeforeRange verifies acceptance
// criterion §4: pending task created before [from] → excluded.
func TestStore_TasksForTimeline_AC4_PendingCreatedBeforeRange(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()
	may1, may31 := pgTimelineRange[0], pgTimelineRange[1]

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg-ac4-old-pending", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE tasks SET created_at = $1, updated_at = $1 WHERE id = $2`, april2026, task.ID,
	); err != nil {
		t.Fatalf("back-date: %v", err)
	}
	got, err := store.TasksForTimeline(ctx, may1, may31)
	if err != nil {
		t.Fatalf("TasksForTimeline: %v", err)
	}
	assertNotContainsTask(t, got, task.ID, "AC4 (old pending should be excluded)")
}

// assertPgTaskAllFields checks every mutable column of a PG-backed task in one
// place, keeping callers below the cyclomatic complexity threshold.
func assertPgTaskAllFields(t *testing.T, updated *db.Task,
	wantTitle, wantDesc string, wantPrio int32, wantImp int16,
	wantAssignee string, wantDue time.Time, wantCtx, wantStatus string,
) {
	t.Helper()
	if updated.Title != wantTitle {
		t.Errorf("title: got %q, want %q", updated.Title, wantTitle)
	}
	if !updated.Description.Valid || updated.Description.String != wantDesc {
		t.Errorf("description: got %+v", updated.Description)
	}
	if updated.Priority != wantPrio {
		t.Errorf("priority: got %d, want %d", updated.Priority, wantPrio)
	}
	if !updated.Importance.Valid || updated.Importance.Int16 != wantImp {
		t.Errorf("importance: got %+v", updated.Importance)
	}
	if !updated.Assignee.Valid || updated.Assignee.String != wantAssignee {
		t.Errorf("assignee: got %+v", updated.Assignee)
	}
	if !updated.DueDate.Valid || !updated.DueDate.Time.Equal(wantDue) {
		t.Errorf("due_date: got %+v, want %v", updated.DueDate, wantDue)
	}
	if !updated.Context.Valid || updated.Context.String != wantCtx {
		t.Errorf("context: got %+v", updated.Context)
	}
	if updated.Status != wantStatus {
		t.Errorf("status: got %q, want %q", updated.Status, wantStatus)
	}
}

// TestGTDStore_UpdateTask_PG_PartialPatch verifies that unspecified fields are preserved
// when only one field is patched.
func TestGTDStore_UpdateTask_PG_PartialPatch(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "original title", Priority: 2, Description: "original desc",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	newDesc := "updated description"
	updated, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Description: &newDesc})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if updated.Title != "original title" {
		t.Errorf("title should be preserved: got %q", updated.Title)
	}
	if !updated.Description.Valid || updated.Description.String != newDesc {
		t.Errorf("description should be updated: got %+v", updated.Description)
	}
	if updated.Priority != 2 {
		t.Errorf("priority should be preserved: got %d", updated.Priority)
	}
}

// TestGTDStore_UpdateTask_PG_AllFields verifies all mutable fields are written
// correctly in a single UpdateTask call. Required by backend-security-design.md §6.5.
func TestGTDStore_UpdateTask_PG_AllFields(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "old title", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	newTitle := "new title"
	newDesc := "new desc"
	newPrio := int32(1)
	newImp := int16(2)
	newAssignee := "human" // must be a canonical actor (gtd.NormalizeActor allowlist)
	due := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	newCtx := "discussion context"
	newStatus := string(gtd.TaskStatusInProgress)

	updated, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{
		Title: &newTitle, Description: &newDesc, Priority: &newPrio, Importance: &newImp,
		Assignee: &newAssignee, DueDate: &due, Context: &newCtx, Status: &newStatus,
	})
	if err != nil {
		t.Fatalf("UpdateTask all fields: %v", err)
	}
	assertPgTaskAllFields(t, updated, newTitle, newDesc, newPrio, newImp, newAssignee, due, newCtx, newStatus)
}

// TestGTDStore_UpdateTask_PG_CreatedAtImmutable verifies that created_at is not
// modified by UpdateTask.
func TestGTDStore_UpdateTask_PG_CreatedAtImmutable(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "immutable-created-at", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	originalCreatedAt := task.CreatedAt

	newTitle := "changed title"
	updated, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{Title: &newTitle})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	if !updated.CreatedAt.Valid {
		t.Fatal("updated.CreatedAt not valid")
	}
	if !updated.CreatedAt.Time.Equal(originalCreatedAt.Time) {
		t.Errorf("created_at changed: got %v, want %v", updated.CreatedAt.Time, originalCreatedAt.Time)
	}
}

// TestGTDStore_UpdateTask_PG_NotFound verifies ErrNotFound is returned for unknown IDs.
func TestGTDStore_UpdateTask_PG_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	newTitle := "should not matter"
	_, err := store.UpdateTask(ctx, uuid.New(), gtd.UpdateTaskParams{Title: &newTitle})
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// TestStore_TasksForTimeline_WorkspaceIsolation verifies workspace B cannot
// see workspace A's rows.
func TestStore_TasksForTimeline_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	sA := newPgGTDStore(pool, &wsA)
	sB := newPgGTDStore(pool, &wsB)
	ctx := context.Background()
	may1, may31 := pgTimelineRange[0], pgTimelineRange[1]

	if _, err := sA.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg-ws-isolation", Priority: 3}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	gotA, err := sA.TasksForTimeline(ctx, may1, may31)
	if err != nil {
		t.Fatalf("TasksForTimeline (A): %v", err)
	}
	if len(gotA) != 1 {
		t.Errorf("want 1 row in workspace A, got %d", len(gotA))
	}
	gotB, err := sB.TasksForTimeline(ctx, may1, may31)
	if err != nil {
		t.Fatalf("TasksForTimeline (B): %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("want 0 rows in workspace B, got %d", len(gotB))
	}
}

// pgUpcomingCreateTask is a test helper used by TestGTDStore_UpcomingTasks_* functions.
func pgUpcomingCreateTask(
	t *testing.T, ctx context.Context, store *gtd.Store,
	title string, due *time.Time, importance *int16, status gtd.TaskStatus,
) *db.Task {
	t.Helper()
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:      title,
		Priority:   2,
		DueDate:    due,
		Importance: importance,
		// Assignee always set: some callers transition to in_progress below,
		// which requires an owner (P6.7 domain-layer gate); pending/completed/
		// cancelled callers are unaffected by the extra field.
		Assignee: "claude",
	})
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", title, err)
	}
	if status != "" && status != gtd.TaskStatusPending {
		task, err = store.UpdateTaskStatus(ctx, task.ID, status)
		if err != nil {
			t.Fatalf("UpdateTaskStatus(%q → %s): %v", title, status, err)
		}
	}
	return task
}

// TestGTDStore_UpcomingTasks_WithinWindow verifies that pending tasks inside
// the window and all no-due-date pending tasks are returned, while
// beyond-window and completed tasks are excluded.
func TestGTDStore_UpcomingTasks_WithinWindow(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	imp1 := int16(1)
	imp2 := int16(2)
	todayDue := now.Add(time.Hour)
	futureDue := now.AddDate(0, 0, 5)
	beyondDue := now.AddDate(0, 0, 30)

	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)

	pgUpcomingCreateTask(t, ctx, storeA, "today-task", &todayDue, nil, "")
	pgUpcomingCreateTask(t, ctx, storeA, "future-task", &futureDue, nil, "")
	pgUpcomingCreateTask(t, ctx, storeA, "beyond-window-task", &beyondDue, nil, "")                 // >7d → excluded
	pgUpcomingCreateTask(t, ctx, storeA, "unscheduled-imp1", nil, &imp1, "")                        // importance=1, no due → included
	pgUpcomingCreateTask(t, ctx, storeA, "unscheduled-imp2", nil, &imp2, "")                        // importance=2, no due → NOW included
	pgUpcomingCreateTask(t, ctx, storeA, "completed-task", &todayDue, nil, gtd.TaskStatusCompleted) // excluded
	pgUpcomingCreateTask(t, ctx, storeB, "ws-b-task", &todayDue, nil, "")                           // other workspace → excluded

	tasks, err := storeA.UpcomingTasks(ctx, now, 7, 50)
	if err != nil {
		t.Fatalf("UpcomingTasks: %v", err)
	}

	titleSet := make(map[string]bool, len(tasks))
	for _, tk := range tasks {
		titleSet[tk.Title] = true
	}
	// All no-due-date active tasks are now included regardless of importance.
	for _, want := range []string{"today-task", "future-task", "unscheduled-imp1", "unscheduled-imp2"} {
		if !titleSet[want] {
			t.Errorf("missing expected task %q; got titles: %v", want, titleSet)
		}
	}
	for _, notWant := range []string{"beyond-window-task", "completed-task", "ws-b-task"} {
		if titleSet[notWant] {
			t.Errorf("unexpected task %q in results", notWant)
		}
	}
}

// TestGTDStore_UpcomingTasks_NoDueDateAllImportances verifies that active tasks
// with NULL due_date are included in UpcomingTasks regardless of their importance
// value (the fix for the divergence between /dashboard upcoming and /gtd list).
func TestGTDStore_UpcomingTasks_NoDueDateAllImportances(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)

	for _, imp := range []int16{1, 2, 3} {
		v := imp
		pgUpcomingCreateTask(t, ctx, store, fmt.Sprintf("no-due-imp%d", imp), nil, &v, "")
	}

	tasks, err := store.UpcomingTasks(ctx, now, 7, 50)
	if err != nil {
		t.Fatalf("UpcomingTasks: %v", err)
	}

	titleSet := make(map[string]bool, len(tasks))
	for _, tk := range tasks {
		titleSet[tk.Title] = true
	}
	for _, want := range []string{"no-due-imp1", "no-due-imp2", "no-due-imp3"} {
		if !titleSet[want] {
			t.Errorf("missing expected no-due task %q; got: %v", want, titleSet)
		}
	}
}

// TestGTDStore_UpcomingTasks_WorkspaceScoping verifies that each store
// only sees tasks belonging to its own workspace.
func TestGTDStore_UpcomingTasks_WorkspaceScoping(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	todayDue := now.Add(time.Hour)

	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)

	pgUpcomingCreateTask(t, ctx, storeA, "ws-a-task", &todayDue, nil, "")
	pgUpcomingCreateTask(t, ctx, storeB, "ws-b-task", &todayDue, nil, "")

	tasksB, err := storeB.UpcomingTasks(ctx, now, 7, 50)
	if err != nil {
		t.Fatalf("UpcomingTasks (B): %v", err)
	}
	if len(tasksB) != 1 || tasksB[0].Title != "ws-b-task" {
		titles := make([]string, len(tasksB))
		for i, tk := range tasksB {
			titles[i] = tk.Title
		}
		t.Errorf("want [ws-b-task], got %v", titles)
	}
}

// TestGTDStore_UpcomingTasks_EmptyWorkspace verifies that a fresh workspace
// with no tasks returns an empty slice (not nil or error).
func TestGTDStore_UpcomingTasks_EmptyWorkspace(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	wsEmpty := uuid.New()
	storeEmpty := newPgGTDStore(pool, &wsEmpty)

	tasks, err := storeEmpty.UpcomingTasks(ctx, now, 7, 50)
	if err != nil {
		t.Fatalf("UpcomingTasks (empty): %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks in empty workspace, got %d", len(tasks))
	}
}

// TestGTDStore_UpcomingTasks_PastDue verifies that a past-due task is included
// in the result set and placed into the "today" bucket by GroupUpcomingTasks.
func TestGTDStore_UpcomingTasks_PastDue(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	wsPast := uuid.New()
	storePast := newPgGTDStore(pool, &wsPast)
	pastDue := now.AddDate(0, 0, -3)
	pgUpcomingCreateTask(t, ctx, storePast, "past-due-task", &pastDue, nil, "")

	tasks, err := storePast.UpcomingTasks(ctx, now, 7, 50)
	if err != nil {
		t.Fatalf("UpcomingTasks (past): %v", err)
	}
	if len(tasks) != 1 || tasks[0].Title != "past-due-task" {
		t.Errorf("expected past-due-task in results, got %v", tasks)
	}
	// Verify grouping places it in today bucket.
	groups := gtd.GroupUpcomingTasks(tasks, now, time.UTC, 7, 50)
	if len(groups.Today) != 1 {
		t.Errorf("expected past-due task in today bucket, got today=%d", len(groups.Today))
	}
}

// TestGTDStore_UpcomingTasks_InProgressIncluded verifies that in_progress tasks
// with a due date within the window are included in results alongside pending ones.
// Completed and cancelled tasks must remain excluded.
func TestGTDStore_UpcomingTasks_InProgressIncluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()

	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	dueSoon := now.Add(2 * 24 * time.Hour)

	pgUpcomingCreateTask(t, ctx, store, "pending-task", &dueSoon, nil, "")
	pgUpcomingCreateTask(t, ctx, store, "in-progress-task", &dueSoon, nil, gtd.TaskStatusInProgress)
	pgUpcomingCreateTask(t, ctx, store, "completed-task", &dueSoon, nil, gtd.TaskStatusCompleted)
	pgUpcomingCreateTask(t, ctx, store, "cancelled-task", &dueSoon, nil, gtd.TaskStatusCancelled)

	tasks, err := store.UpcomingTasks(ctx, now, 7, 50)
	if err != nil {
		t.Fatalf("UpcomingTasks: %v", err)
	}

	titleSet := make(map[string]bool, len(tasks))
	for _, tk := range tasks {
		titleSet[tk.Title] = true
	}
	for _, want := range []string{"pending-task", "in-progress-task"} {
		if !titleSet[want] {
			t.Errorf("expected %q in upcoming results; got %v", want, titleSet)
		}
	}
	for _, notWant := range []string{"completed-task", "cancelled-task"} {
		if titleSet[notWant] {
			t.Errorf("unexpected %q in upcoming results", notWant)
		}
	}
}

// TestStore_BeginTask_HappyPath verifies the PG-side happy path:
// pending → in_progress + activity_log row written.
func TestStore_BeginTask_HappyPath(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg begin task test", Priority: 2, Assignee: "claude"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	result, err := store.BeginTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("BeginTask: %v", err)
	}
	if result.Status != statusInProgress {
		t.Errorf("expected status in_progress, got %q", result.Status)
	}

	// Verify activity_log entry.
	logs, err := store.ListActivityLogsSince(ctx, task.CreatedAt.Time, 10)
	if err != nil {
		t.Fatalf("ListActivityLogsSince: %v", err)
	}
	var found bool
	for _, l := range logs {
		if l.Action == "work_session_started" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected work_session_started activity_log entry")
	}
}

// TestStore_BeginTask_Idempotent verifies that calling BeginTask on an already
// in_progress task returns the task without error and without a duplicate
// activity_log entry.
func TestStore_BeginTask_Idempotent(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "idempotent pg task", Priority: 1, Assignee: "claude"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if _, err := store.BeginTask(ctx, task.ID); err != nil {
		t.Fatalf("BeginTask first: %v", err)
	}
	logsBefore, err := store.ListActivityLogsSince(ctx, task.CreatedAt.Time, 100)
	if err != nil {
		t.Fatalf("ListActivityLogsSince before: %v", err)
	}
	countBefore := countAction(logsBefore, "work_session_started")

	if _, err := store.BeginTask(ctx, task.ID); err != nil {
		t.Fatalf("BeginTask second (idempotent): %v", err)
	}
	logsAfter, err := store.ListActivityLogsSince(ctx, task.CreatedAt.Time, 100)
	if err != nil {
		t.Fatalf("ListActivityLogsSince after: %v", err)
	}
	countAfter := countAction(logsAfter, "work_session_started")

	if countAfter != countBefore {
		t.Errorf("expected no duplicate log entry: before=%d after=%d", countBefore, countAfter)
	}
}

// TestStore_BeginTask_NotFound verifies ErrNotFound for a non-existent ID.
func TestStore_BeginTask_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	_, err := store.BeginTask(ctx, uuid.New())
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected gtd.ErrNotFound, got %v", err)
	}
}

// TestStore_BeginTask_WorkspaceIsolation verifies that a task in workspace A
// cannot be begun by a store scoped to workspace B.
func TestStore_BeginTask_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA, wsB := uuid.New(), uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)
	ctx := context.Background()

	task, err := storeA.CreateTask(ctx, gtd.CreateTaskParams{Title: "ws isolation task", Priority: 1})
	if err != nil {
		t.Fatalf("CreateTask in wsA: %v", err)
	}

	_, err = storeB.BeginTask(ctx, task.ID)
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound for cross-workspace begin, got %v", err)
	}
}

// countAction counts activity log entries with the given action string.
func countAction(logs []db.ActivityLog, action string) int {
	var n int
	for _, l := range logs {
		if l.Action == action {
			n++
		}
	}
	return n
}

// backdatePgActivityLog rewrites created_at for all activity_log rows with
// the given action, bypassing LogActivity (which always stamps NOW()).
func backdatePgActivityLog(t *testing.T, pool *pgxpool.Pool, action string, createdAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE activity_log SET created_at = $1 WHERE action = $2`, createdAt, action,
	); err != nil {
		t.Fatalf("backdate activity_log: %v", err)
	}
}

func countPgActivityLogRows(t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM activity_log WHERE action = $1`, action).Scan(&n); err != nil {
		t.Fatalf("count activity_log: %v", err)
	}
	return n
}

// TestStore_PruneOlderThan_ActivityLog_Expired verifies that an activity_log
// row older than the cutoff is deleted on Postgres.
func TestStore_PruneOlderThan_ActivityLog_Expired(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	if err := store.LogActivity(ctx, "actor", "pg_expired_action", nil, ""); err != nil {
		t.Fatalf("LogActivity: %v", err)
	}
	backdatePgActivityLog(t, pool, "pg_expired_action", time.Now().Add(-400*24*time.Hour))

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	n, err := store.PruneOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n < 1 {
		t.Errorf("rows deleted = %d, want >= 1", n)
	}
	if got := countPgActivityLogRows(t, pool, "pg_expired_action"); got != 0 {
		t.Errorf("expired row survived: count = %d, want 0", got)
	}
}

// TestStore_PruneOlderThan_ActivityLog_NotExpired verifies that a fresh
// activity_log row is NOT deleted on Postgres.
func TestStore_PruneOlderThan_ActivityLog_NotExpired(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	if err := store.LogActivity(ctx, "actor", "pg_fresh_action", nil, ""); err != nil {
		t.Fatalf("LogActivity: %v", err)
	}
	backdatePgActivityLog(t, pool, "pg_fresh_action", time.Now().Add(-10*24*time.Hour))

	cutoff := time.Now().Add(-365 * 24 * time.Hour)
	if _, err := store.PruneOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if got := countPgActivityLogRows(t, pool, "pg_fresh_action"); got != 1 {
		t.Errorf("fresh row was pruned: count = %d, want 1", got)
	}
}

// TestStore_PruneOlderThan_ActivityLog_Boundary verifies the cutoff
// comparison is strict-less-than on Postgres: a row exactly at the cutoff
// survives, a row 1 second before the cutoff is deleted.
func TestStore_PruneOlderThan_ActivityLog_Boundary(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	cutoff := time.Now().Add(-365 * 24 * time.Hour)

	if err := store.LogActivity(ctx, "actor", "pg_at_cutoff", nil, ""); err != nil {
		t.Fatalf("LogActivity (pg_at_cutoff): %v", err)
	}
	backdatePgActivityLog(t, pool, "pg_at_cutoff", cutoff)

	if err := store.LogActivity(ctx, "actor", "pg_just_before_cutoff", nil, ""); err != nil {
		t.Fatalf("LogActivity (pg_just_before_cutoff): %v", err)
	}
	backdatePgActivityLog(t, pool, "pg_just_before_cutoff", cutoff.Add(-1*time.Second))

	if _, err := store.PruneOlderThan(ctx, cutoff); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if got := countPgActivityLogRows(t, pool, "pg_at_cutoff"); got != 1 {
		t.Errorf("row exactly at cutoff was pruned: count = %d, want 1 (survive)", got)
	}
	if got := countPgActivityLogRows(t, pool, "pg_just_before_cutoff"); got != 0 {
		t.Errorf("row just before cutoff survived: count = %d, want 0 (pruned)", got)
	}
}

// TestGTDStore_PG_GetTaskByID exercises GetTaskByID on Postgres:
// found, not found (ErrNotFound), and wrong workspace (ErrNotFound).
// Uses testcontainers (Docker required); skipped with -short.
func TestGTDStore_PG_GetTaskByID(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		pool := openTestPgPool(t)
		wsID := uuid.New()
		store := newPgGTDStore(pool, &wsID)
		ctx := context.Background()

		created, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "pg fetch me", Priority: 2})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		got, err := store.GetTaskByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("GetTaskByID: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("ID mismatch: got %s, want %s", got.ID, created.ID)
		}
		if got.Title != "pg fetch me" {
			t.Errorf("Title mismatch: got %q, want %q", got.Title, "pg fetch me")
		}
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		pool := openTestPgPool(t)
		wsID := uuid.New()
		store := newPgGTDStore(pool, &wsID)

		_, err := store.GetTaskByID(context.Background(), uuid.New())
		if !errors.Is(err, gtd.ErrNotFound) {
			t.Errorf("expected gtd.ErrNotFound for unknown ID, got %v", err)
		}
	})

	t.Run("wrong workspace returns ErrNotFound", func(t *testing.T) {
		pool := openTestPgPool(t)
		wsA := uuid.New()
		wsB := uuid.New()
		storeA := newPgGTDStore(pool, &wsA)
		storeB := newPgGTDStore(pool, &wsB)
		ctx := context.Background()

		created, err := storeA.CreateTask(ctx, gtd.CreateTaskParams{Title: "ws-a task", Priority: 3})
		if err != nil {
			t.Fatalf("CreateTask in workspace A: %v", err)
		}

		// Workspace B must not see workspace A's task.
		_, err = storeB.GetTaskByID(ctx, created.ID)
		if !errors.Is(err, gtd.ErrNotFound) {
			t.Errorf("expected gtd.ErrNotFound from wrong workspace, got %v", err)
		}
	})
}
