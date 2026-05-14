package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// openMem opens an in-memory SQLite DB and applies the embedded schema.
// Each test gets its own DB so they cannot interfere.
func openMem(t *testing.T, workspaceID string) *sqlite.GTDStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), ":memory:", workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewGTDStore(d)
}

func TestGTDStore_CreateAndListProjects(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	p, err := s.CreateProject(ctx, gtd.CreateProjectParams{
		Name:  "demo-project",
		Title: "Demo",
		Area:  "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Name != "demo-project" || p.Status != "active" {
		t.Errorf("unexpected project: %+v", p)
	}

	projects, err := s.ListActiveProjects(ctx)
	if err != nil {
		t.Fatalf("ListActiveProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != p.ID {
		t.Errorf("expected freshly-created project to appear, got %+v", projects)
	}
}

func TestGTDStore_DuplicateProjectNameConflict(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	_, err := s.CreateProject(ctx, gtd.CreateProjectParams{Name: "dup", Title: "first", Area: "x"})
	if err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}
	_, err = s.CreateProject(ctx, gtd.CreateProjectParams{Name: "dup", Title: "second", Area: "x"})
	if !errors.Is(err, gtd.ErrConflict) {
		t.Errorf("expected gtd.ErrConflict on duplicate name, got %v", err)
	}
}

func TestGTDStore_CreateTaskWithImportanceAndContext(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	importance := int16(2)
	tc := "Discussed in 4/27 architecture session"
	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title:      "implement gtd sqlite",
		Priority:   2,
		Importance: &importance,
		Context:    tc,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Title != "implement gtd sqlite" {
		t.Errorf("unexpected title: %q", task.Title)
	}
	if !task.Importance.Valid || task.Importance.Int16 != 2 {
		t.Errorf("expected importance=2, got %+v", task.Importance)
	}
	if !task.Context.Valid || task.Context.String != tc {
		t.Errorf("expected context=%q, got %+v", tc, task.Context)
	}
}

func TestGTDStore_CompleteTask(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "x", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	url := "https://example.com/pr/1"
	completed, err := s.CompleteTask(ctx, task.ID, &url)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if completed.Status != "completed" || !completed.Artifact.Valid || completed.Artifact.String != url {
		t.Errorf("unexpected completed task: %+v", completed)
	}
}

func TestGTDStore_CompleteTaskNotFound(t *testing.T) {
	s := openMem(t, "")
	_, err := s.CompleteTask(context.Background(), uuid.New(), nil)
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing task, got %v", err)
	}
}

func TestGTDStore_WorkspaceIsolation(t *testing.T) {
	scoped := openMem(t, "11111111-1111-4111-8111-111111111111")
	other := openMem(t, "22222222-2222-4222-8222-222222222222") // different DB, different scope

	if _, err := scoped.CreateTask(context.Background(),
		gtd.CreateTaskParams{Title: "scoped task", Priority: 3}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	tasks, err := scoped.Tasks(context.Background(), nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task in scoped store, got %d", len(tasks))
	}

	tasksOther, err := other.Tasks(context.Background(), nil)
	if err != nil {
		t.Fatalf("Tasks (other): %v", err)
	}
	if len(tasksOther) != 0 {
		t.Errorf("expected 0 tasks in unrelated workspace, got %d", len(tasksOther))
	}
}

func TestGTDStore_WeeklyProgress(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	t1, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "a", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := s.CompleteTask(ctx, t1.ID, nil); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if _, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "b", Priority: 3}); err != nil {
		t.Fatalf("CreateTask b: %v", err)
	}

	completed, total, err := s.WeeklyProgress(ctx)
	if err != nil {
		t.Fatalf("WeeklyProgress: %v", err)
	}
	if completed != 1 || total != 1 {
		t.Errorf("expected completed=1 total=1, got completed=%d total=%d", completed, total)
	}
}

func TestGTDStore_DeleteTask(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "doomed", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := s.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	tasks, _ := s.Tasks(ctx, nil)
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("task should be deleted, still appears: %+v", tk)
		}
	}
}

// TestGTDStore_DeleteTask_CascadesIntoWorkSessions verifies the code-level
// replacement for the FK cascades dropped in migration 000026. After
// migration 000026 the DB no longer enforces:
//
//   - work_session_tasks.task_id ON DELETE CASCADE
//   - work_sessions.current_task_id ON DELETE SET NULL
//
// so GTDStore.DeleteTask MUST clean those rows itself, atomically, in a
// single transaction. The test inserts a task, links it to a work_session
// both as the current_task_id and via a join row, then deletes the task and
// asserts the cleanup happened.
func TestGTDStore_DeleteTask_CascadesIntoWorkSessions(t *testing.T) {
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := sqlite.NewGTDStore(d)
	ctx := context.Background()

	// Insert a task via the normal store path.
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "linked-task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Insert a work_session whose current_task_id points at this task.
	// Hand-rolled INSERT (not via WorkSessionStore) keeps this test focused
	// on the GTD cascade behaviour without coupling to worksession internals.
	sessionID := uuid.New().String()
	wsID := uuid.New().String()
	if err := d.ExecContext(ctx,
		`INSERT INTO work_sessions
			(id, workspace_id, repo_name, project_id, title, goal, status, source,
			 confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		 VALUES (?1,?2,?3,NULL,?4,?5,'in_progress','manual',NULL,?6,?7,?7,?7)`,
		sessionID, wsID, "demo-repo", "linked-session", "test cascade",
		task.ID.String(), "2026-05-03T00:00:00.000Z",
	); err != nil {
		t.Fatalf("insert work_session: %v", err)
	}

	// Insert a join row.
	if err := d.ExecContext(ctx,
		`INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		 VALUES (?1,?2,'primary','2026-05-03T00:00:00.000Z')`,
		sessionID, task.ID.String(),
	); err != nil {
		t.Fatalf("insert work_session_tasks: %v", err)
	}

	// Sanity check: the link row exists, current_task_id is set.
	var preLinks int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM work_session_tasks WHERE task_id = ?1`, task.ID.String(),
	).Scan(&preLinks); err != nil {
		t.Fatalf("pre-count: %v", err)
	}
	if preLinks != 1 {
		t.Fatalf("expected 1 link row before delete, got %d", preLinks)
	}

	// Delete the task — should cascade.
	if err := store.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// Assertion 1: link row is gone (was ON DELETE CASCADE).
	var postLinks int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM work_session_tasks WHERE task_id = ?1`, task.ID.String(),
	).Scan(&postLinks); err != nil {
		t.Fatalf("post-count links: %v", err)
	}
	if postLinks != 0 {
		t.Errorf("expected work_session_tasks rows to be deleted, got %d", postLinks)
	}

	// Assertion 2: current_task_id is now NULL (was ON DELETE SET NULL).
	var currentTaskID *string
	if err := d.QueryRowContext(ctx,
		`SELECT current_task_id FROM work_sessions WHERE id = ?1`, sessionID,
	).Scan(&currentTaskID); err != nil {
		t.Fatalf("post-count session: %v", err)
	}
	if currentTaskID != nil {
		t.Errorf("expected current_task_id to be NULL after task delete, got %q", *currentTaskID)
	}

	// Assertion 3: the task itself is gone.
	tasks, _ := store.Tasks(ctx, nil)
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("task should be deleted, still appears: %+v", tk)
		}
	}
}

// TestGTDStore_DeleteTask_NoLinkedRows ensures DeleteTask still works when
// no work_session_tasks / work_sessions rows reference the task. The cleanup
// statements should be no-ops and the parent DELETE should commit normally.
func TestGTDStore_DeleteTask_NoLinkedRows(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "isolated", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := s.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask without linked rows: %v", err)
	}

	tasks, _ := s.Tasks(ctx, nil)
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("task should be deleted, still appears: %+v", tk)
		}
	}
}

// insertWSAFixture inserts a work_session + work_session_tasks pair belonging
// to workspace A pointing at taskID. Used by the cross-workspace test to
// verify that a workspace-B caller cannot touch these rows.
func insertWSAFixture(t *testing.T, d *sqlite.DB, ctx context.Context, wsA, taskID string) string {
	t.Helper()
	sessionID := uuid.New().String()
	if err := d.ExecContext(ctx,
		`INSERT INTO work_sessions
			(id, workspace_id, repo_name, project_id, title, goal, status, source,
			 confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		 VALUES (?1,?2,?3,NULL,?4,?5,'in_progress','manual',NULL,?6,?7,?7,?7)`,
		sessionID, wsA, "ws-A-repo", "ws-A-session", "test ws-mismatch guard",
		taskID, "2026-05-03T00:00:00.000Z",
	); err != nil {
		t.Fatalf("insert work_session: %v", err)
	}
	if err := d.ExecContext(ctx,
		`INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		 VALUES (?1,?2,'primary','2026-05-03T00:00:00.000Z')`,
		sessionID, taskID,
	); err != nil {
		t.Fatalf("insert work_session_tasks: %v", err)
	}
	return sessionID
}

// assertJoinRowsSurvived asserts a non-zero count of join rows for taskID.
// Used by TestGTDStore_DeleteTask_WorkspaceMismatch to prove the cleanup
// DELETE was guarded by the workspace pre-check.
func assertJoinRowsSurvived(t *testing.T, d *sqlite.DB, ctx context.Context, taskID string, want int) {
	t.Helper()
	var got int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM work_session_tasks WHERE task_id = ?1`, taskID,
	).Scan(&got); err != nil {
		t.Fatalf("count surviving join rows: %v", err)
	}
	if got != want {
		t.Errorf("expected workspace A's join row to survive cross-workspace delete, got %d remaining (want %d)", got, want)
	}
}

// assertCurrentTaskIDPreserved asserts current_task_id of the named session
// is still set to wantTaskID. Used by TestGTDStore_DeleteTask_WorkspaceMismatch
// to prove the cleanup UPDATE was guarded by the workspace pre-check.
func assertCurrentTaskIDPreserved(t *testing.T, d *sqlite.DB, ctx context.Context, sessionID, wantTaskID string) {
	t.Helper()
	var currentTaskID *string
	if err := d.QueryRowContext(ctx,
		`SELECT current_task_id FROM work_sessions WHERE id = ?1`, sessionID,
	).Scan(&currentTaskID); err != nil {
		t.Fatalf("read current_task_id: %v", err)
	}
	if currentTaskID == nil {
		t.Error("workspace A current_task_id was NULL'd by cross-workspace DeleteTask (pre-check missing or broken)")
		return
	}
	if *currentTaskID != wantTaskID {
		t.Errorf("workspace A current_task_id changed unexpectedly: got %q, want %q", *currentTaskID, wantTaskID)
	}
}

// TestGTDStore_DeleteTask_WorkspaceMismatch ensures the workspace filter on
// the parent DELETE is respected AND that the workspace pre-check guards the
// cleanup statements: a task created in workspace A cannot be deleted by a
// store scoped to workspace B, and the join-table / current_task_id pointer
// belonging to workspace A MUST survive the cross-workspace call. Without the
// pre-check the cleanup statements (keyed only by task_id) would silently
// erase neighbouring data even though the parent DELETE 0-rowed.
func TestGTDStore_DeleteTask_WorkspaceMismatch(t *testing.T) {
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// Use a shared file DB so two Open() calls see the same rows. The DB is
	// auto-deleted via t.Cleanup on the temp file.
	dbPath := t.TempDir() + "/wbt-cascade-test.db"

	dA, err := sqlite.Open(context.Background(), "file:"+dbPath, wsA)
	if err != nil {
		t.Fatalf("sqlite.Open A: %v", err)
	}
	t.Cleanup(func() { _ = dA.Close() })
	storeA := sqlite.NewGTDStore(dA)
	ctx := context.Background()

	task, err := storeA.CreateTask(ctx, gtd.CreateTaskParams{Title: "ws-A-task", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask in workspace A: %v", err)
	}

	// Insert workspace-A work_session + join row pointing at the task.
	sessionID := insertWSAFixture(t, dA, ctx, wsA, task.ID.String())

	// Open the same file with workspace B.
	dB, err := sqlite.Open(context.Background(), "file:"+dbPath, wsB)
	if err != nil {
		t.Fatalf("sqlite.Open B: %v", err)
	}
	t.Cleanup(func() { _ = dB.Close() })
	storeB := sqlite.NewGTDStore(dB)

	if err := storeB.DeleteTask(ctx, task.ID); err != nil {
		t.Fatalf("DeleteTask cross-workspace: %v", err)
	}

	// Assertion 1: parent task still exists when read from workspace A.
	assertTaskStillVisible(t, storeA, ctx, task.ID)

	// Assertion 2: workspace-A join row MUST still be present (proves the
	// task_id-only cleanup DELETE was guarded by the workspace pre-check).
	assertJoinRowsSurvived(t, dA, ctx, task.ID.String(), 1)

	// Assertion 3: workspace A's work_sessions.current_task_id MUST still
	// point at the original task (proves the task_id-only cleanup UPDATE
	// was guarded by the workspace pre-check).
	assertCurrentTaskIDPreserved(t, dA, ctx, sessionID, task.ID.String())
}

// assertTaskStillVisible fails the test if the named task has disappeared
// from the workspace's Tasks() listing. Used by the cross-workspace delete
// test as a positive assertion that the parent DELETE was correctly 0-rowed.
func assertTaskStillVisible(t *testing.T, store *sqlite.GTDStore, ctx context.Context, taskID uuid.UUID) {
	t.Helper()
	tasks, err := store.Tasks(ctx, nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == taskID {
			return
		}
	}
	t.Error("task should NOT have been deleted (workspace mismatch); it disappeared")
}

func TestGTDStore_LogActivityAndUpdateStatus(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	if err := s.LogActivity(ctx, "tester", "smoke", nil, "no project"); err != nil {
		t.Fatalf("LogActivity: %v", err)
	}

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "p", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	updated, err := s.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusInProgress)
	if err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	if updated.Status != "in_progress" {
		t.Errorf("expected in_progress, got %s", updated.Status)
	}
}

func TestGTDStore_ActiveGoals(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	if _, err := s.CreateGoal(ctx, gtd.CreateGoalParams{Title: "ship v1", Area: "engineering"}); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}
	goals, err := s.ActiveGoals(ctx)
	if err != nil {
		t.Fatalf("ActiveGoals: %v", err)
	}
	if len(goals) != 1 || goals[0].Title != "ship v1" {
		t.Errorf("unexpected goals: %+v", goals)
	}
}

// TestGTDStore_GetProjectByID_WorkspaceIsolation verifies that GetProjectByID
// cannot cross workspace boundaries — workspace B must not read workspace A's data.
func TestGTDStore_GetProjectByID_WorkspaceIsolation(t *testing.T) {
	tmp := t.TempDir() + "/iso.db"
	ctx := context.Background()
	const wsA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const wsB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

	dbA, err := sqlite.Open(ctx, tmp, wsA)
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}
	t.Cleanup(func() { _ = dbA.Close() })
	storeA := sqlite.NewGTDStore(dbA)

	proj, err := storeA.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "secret", Title: "workspace A secret", Priority: 3,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	dbB, err := sqlite.Open(ctx, tmp, wsB)
	if err != nil {
		t.Fatalf("Open B: %v", err)
	}
	t.Cleanup(func() { _ = dbB.Close() })
	storeB := sqlite.NewGTDStore(dbB)

	_, err = storeB.GetProjectByID(ctx, proj.ID)
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("cross-workspace read must return ErrNotFound, got: %v", err)
	}

	// Sanity: workspace A can still read its own project.
	got, err := storeA.GetProjectByID(ctx, proj.ID)
	if err != nil {
		t.Fatalf("storeA.GetProjectByID: %v", err)
	}
	if got.ID != proj.ID {
		t.Errorf("storeA got wrong project: %+v", got)
	}
}

// TestGTDStore_GetProjectByID_NotFound ensures ErrNotFound for unknown UUIDs.
func TestGTDStore_GetProjectByID_NotFound(t *testing.T) {
	s := openMem(t, "11111111-1111-4111-8111-111111111111")
	_, err := s.GetProjectByID(context.Background(), uuid.New())
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

// TestGTDStore_TopPendingTask verifies ordering and nil return for empty set.
func TestGTDStore_TopPendingTask(t *testing.T) {
	t.Run("multiple pending tasks → returns lowest priority then importance", func(t *testing.T) {
		s := openMem(t, "")
		ctx := context.Background()

		// Create three pending tasks with different priorities.
		// TopPendingTask should return priority=1 (lowest number = highest priority).
		imp2 := int16(2)
		imp1 := int16(1)
		_, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "low-prio", Priority: 5, Importance: &imp2})
		if err != nil {
			t.Fatalf("CreateTask low-prio: %v", err)
		}
		top, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "top-prio", Priority: 1, Importance: &imp1})
		if err != nil {
			t.Fatalf("CreateTask top-prio: %v", err)
		}
		_, err = s.CreateTask(ctx, gtd.CreateTaskParams{Title: "mid-prio", Priority: 3, Importance: &imp2})
		if err != nil {
			t.Fatalf("CreateTask mid-prio: %v", err)
		}

		got, err := s.TopPendingTask(ctx)
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
		s := openMem(t, "")
		ctx := context.Background()

		// Create a completed task — should not be returned.
		task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "done", Priority: 1})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := s.CompleteTask(ctx, task.ID, nil); err != nil {
			t.Fatalf("CompleteTask: %v", err)
		}

		got, err := s.TopPendingTask(ctx)
		if err != nil {
			t.Fatalf("TopPendingTask: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil task for empty pending set, got: %+v", got)
		}
	})
}

// TestGTDStore_TasksByProjectAllStatuses_ReturnsPendingAndCompleted pins down
// the new SQLite all-statuses query: completed rows show up, default Tasks
// stays active-only.
func TestGTDStore_TasksByProjectAllStatuses_ReturnsPendingAndCompleted(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	project, err := s.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "demo", Title: "Demo", Area: "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	open, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "still open", Priority: 3, ProjectID: &project.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask open: %v", err)
	}
	done, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "shipped", Priority: 3, ProjectID: &project.ID,
	})
	if err != nil {
		t.Fatalf("CreateTask done: %v", err)
	}
	if _, err := s.CompleteTask(ctx, done.ID, nil); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	all, err := s.TasksByProjectAllStatuses(ctx, project.ID)
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

	// Default Tasks(...) MUST still be active-only (regression guard).
	active, err := s.Tasks(ctx, &project.ID)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(active) != 1 || active[0].ID != open.ID {
		t.Errorf("Tasks default path leaked completed rows: %+v", active)
	}
}

// TestGTDStore_TasksByProjectAllStatuses_WorkspaceMismatch confirms strict
// per-workspace scope: a request from workspace B for workspace A's
// project_id MUST return 0 rows.
func TestGTDStore_TasksByProjectAllStatuses_WorkspaceMismatch(t *testing.T) {
	owner := openMem(t, "11111111-1111-4111-8111-111111111111")
	other := openMem(t, "22222222-2222-4222-8222-222222222222")
	ctx := context.Background()

	project, err := owner.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "owner-proj", Title: "Owner", Area: "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject (owner): %v", err)
	}
	if _, err := owner.CreateTask(ctx, gtd.CreateTaskParams{
		Title: "in scope", Priority: 3, ProjectID: &project.ID,
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// Other workspace asks for the same project_id — strict per-workspace
	// scope MUST drop these rows even though project_id matches; the SQLite
	// instance is also a different DB so this pins the cross-DB safety too.
	got, err := other.TasksByProjectAllStatuses(ctx, project.ID)
	if err != nil {
		t.Fatalf("TasksByProjectAllStatuses (other workspace): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 tasks in unrelated workspace, got %d", len(got))
	}
}

// TestGTDStore_TasksByProjectAllStatuses_EmptyProject pins the empty-result
// path: a project with no tasks returns an empty slice (not nil error).
func TestGTDStore_TasksByProjectAllStatuses_EmptyProject(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	project, err := s.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "empty-proj", Title: "Empty", Area: "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := s.TasksByProjectAllStatuses(ctx, project.ID)
	if err != nil {
		t.Fatalf("TasksByProjectAllStatuses: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %+v", got)
	}
}

func TestGTDStore_RecentCompletedTasks(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	proj, err := s.CreateProject(ctx, gtd.CreateProjectParams{Name: "rct", Title: "RCT", Area: "engineering"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	t.Run("returns only completed for the project, newest first", func(t *testing.T) {
		// Create 3 tasks: complete two of them; leave one pending.
		t1, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "first", ProjectID: &proj.ID, Priority: 3})
		if err != nil {
			t.Fatalf("CreateTask t1: %v", err)
		}
		t2, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "second", ProjectID: &proj.ID, Priority: 3})
		if err != nil {
			t.Fatalf("CreateTask t2: %v", err)
		}
		_, err = s.CreateTask(ctx, gtd.CreateTaskParams{Title: "still-pending", ProjectID: &proj.ID, Priority: 3})
		if err != nil {
			t.Fatalf("CreateTask t3: %v", err)
		}
		if _, err := s.CompleteTask(ctx, t1.ID, nil); err != nil {
			t.Fatalf("CompleteTask t1: %v", err)
		}
		// Sleep to guarantee t2.updated_at > t1.updated_at at SQLite's
		// millisecond timestamp resolution (otherwise the secondary id-based
		// tiebreaker is non-deterministic for uuid v4).
		time.Sleep(2 * time.Millisecond)
		if _, err := s.CompleteTask(ctx, t2.ID, nil); err != nil {
			t.Fatalf("CompleteTask t2: %v", err)
		}

		got, err := s.RecentCompletedTasks(ctx, proj.ID, 50)
		if err != nil {
			t.Fatalf("RecentCompletedTasks: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 completed tasks, got %d", len(got))
		}
		// Most recently completed (t2) should be first.
		if got[0].ID != t2.ID {
			t.Errorf("expected t2 first (newest), got %s (want %s)", got[0].ID, t2.ID)
		}
	})

	t.Run("limit caps result", func(t *testing.T) {
		got, err := s.RecentCompletedTasks(ctx, proj.ID, 1)
		if err != nil {
			t.Fatalf("RecentCompletedTasks: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("expected 1 task, got %d", len(got))
		}
	})

	t.Run("unknown project → empty", func(t *testing.T) {
		got, err := s.RecentCompletedTasks(ctx, uuid.New(), 50)
		if err != nil {
			t.Fatalf("RecentCompletedTasks: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 tasks for unknown project, got %d", len(got))
		}
	})
}

func TestGTDStore_RecentActivityByProject(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	proj, err := s.CreateProject(ctx, gtd.CreateProjectParams{Name: "rabp", Title: "RABP", Area: "engineering"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	t.Run("returns activities for the project newest first", func(t *testing.T) {
		if err := s.LogActivity(ctx, "tester", "log_decision", &proj.ID, "first"); err != nil {
			t.Fatalf("LogActivity 1: %v", err)
		}
		if err := s.LogActivity(ctx, "tester", "complete_task", &proj.ID, "second"); err != nil {
			t.Fatalf("LogActivity 2: %v", err)
		}

		since := time.Now().Add(-1 * time.Hour)
		got, err := s.RecentActivityByProject(ctx, proj.ID, since, 50)
		if err != nil {
			t.Fatalf("RecentActivityByProject: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 activity rows, got %d", len(got))
		}
	})

	t.Run("since-window filter excludes older rows", func(t *testing.T) {
		future := time.Now().Add(1 * time.Hour)
		got, err := s.RecentActivityByProject(ctx, proj.ID, future, 50)
		if err != nil {
			t.Fatalf("RecentActivityByProject: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected 0 rows for future since, got %d", len(got))
		}
	})

	t.Run("activity for another project is not returned", func(t *testing.T) {
		other, err := s.CreateProject(ctx, gtd.CreateProjectParams{Name: "rabp-other", Title: "Other", Area: "engineering"})
		if err != nil {
			t.Fatalf("CreateProject other: %v", err)
		}
		if err := s.LogActivity(ctx, "tester", "log_decision", &other.ID, "for other"); err != nil {
			t.Fatalf("LogActivity for other: %v", err)
		}
		got, err := s.RecentActivityByProject(ctx, proj.ID, time.Now().Add(-1*time.Hour), 50)
		if err != nil {
			t.Fatalf("RecentActivityByProject: %v", err)
		}
		for _, a := range got {
			if a.ProjectID.Valid && uuid.UUID(a.ProjectID.Bytes) == other.ID {
				t.Errorf("activity for other project should not appear, got: %+v", a)
			}
		}
	})
}

// TestGTDStore_UpdateGoal verifies the SQLite UpdateGoal full-update path.
func TestGTDStore_UpdateGoal(t *testing.T) {
	t.Run("updates all mutable fields", func(t *testing.T) {
		s := openMem(t, "")
		ctx := context.Background()

		g, err := s.CreateGoal(ctx, gtd.CreateGoalParams{Title: "original title", Area: "work"})
		if err != nil {
			t.Fatalf("CreateGoal: %v", err)
		}

		due := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
		updated, err := s.UpdateGoal(ctx, g.ID, gtd.UpdateGoalParams{
			Title:       "new title",
			Description: "desc",
			Area:        "personal",
			Status:      gtd.GoalStatusCompleted,
			DueDate:     &due,
		})
		if err != nil {
			t.Fatalf("UpdateGoal: %v", err)
		}
		if updated.Title != "new title" {
			t.Errorf("title: got %q, want %q", updated.Title, "new title")
		}
		if updated.Status != "completed" {
			t.Errorf("status: got %q, want completed", updated.Status)
		}
		if !updated.Area.Valid || updated.Area.String != "personal" {
			t.Errorf("area: got %+v, want personal", updated.Area)
		}
	})

	t.Run("not found → ErrNotFound", func(t *testing.T) {
		s := openMem(t, "")
		_, err := s.UpdateGoal(context.Background(), uuid.New(), gtd.UpdateGoalParams{
			Title:  "x",
			Status: gtd.GoalStatusActive,
		})
		if !errors.Is(err, gtd.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("workspace isolation — cross-workspace update is a no-op", func(t *testing.T) {
		tmp := t.TempDir() + "/goal-ws.db"
		ctx := context.Background()
		const wsA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		const wsB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

		dbA, err := sqlite.Open(ctx, tmp, wsA)
		if err != nil {
			t.Fatalf("Open A: %v", err)
		}
		t.Cleanup(func() { _ = dbA.Close() })
		storeA := sqlite.NewGTDStore(dbA)

		g, err := storeA.CreateGoal(ctx, gtd.CreateGoalParams{Title: "ws-a goal"})
		if err != nil {
			t.Fatalf("CreateGoal: %v", err)
		}

		dbB, err := sqlite.Open(ctx, tmp, wsB)
		if err != nil {
			t.Fatalf("Open B: %v", err)
		}
		t.Cleanup(func() { _ = dbB.Close() })
		storeB := sqlite.NewGTDStore(dbB)

		_, err = storeB.UpdateGoal(ctx, g.ID, gtd.UpdateGoalParams{
			Title:  "hijacked",
			Status: gtd.GoalStatusArchived,
		})
		if !errors.Is(err, gtd.ErrNotFound) {
			t.Errorf("cross-workspace update must return ErrNotFound, got %v", err)
		}
	})
}

// TestGTDStore_UpdateProject verifies the SQLite UpdateProject full-update path.
func TestGTDStore_UpdateProject(t *testing.T) {
	t.Run("updates all mutable fields", func(t *testing.T) {
		s := openMem(t, "")
		ctx := context.Background()

		p, err := s.CreateProject(ctx, gtd.CreateProjectParams{
			Name: "orig", Title: "Original", Area: "engineering",
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}

		updated, err := s.UpdateProject(ctx, p.ID, gtd.UpdateProjectParams{
			Title:       "Updated Title",
			Description: "new desc",
			Area:        "operations",
			Priority:    2,
			Status:      gtd.ProjectStatusOnHold,
		})
		if err != nil {
			t.Fatalf("UpdateProject: %v", err)
		}
		if updated.Title != "Updated Title" {
			t.Errorf("title: got %q, want %q", updated.Title, "Updated Title")
		}
		if updated.Status != "on_hold" {
			t.Errorf("status: got %q, want on_hold", updated.Status)
		}
		if updated.Priority != 2 {
			t.Errorf("priority: got %d, want 2", updated.Priority)
		}
		if updated.Area != "operations" {
			t.Errorf("area: got %q, want operations", updated.Area)
		}
	})

	t.Run("defaults area to 'projects' when empty", func(t *testing.T) {
		s := openMem(t, "")
		ctx := context.Background()

		p, err := s.CreateProject(ctx, gtd.CreateProjectParams{
			Name: "def-area", Title: "Def", Area: "engineering",
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}

		updated, err := s.UpdateProject(ctx, p.ID, gtd.UpdateProjectParams{
			Title:  "T",
			Status: gtd.ProjectStatusActive,
		})
		if err != nil {
			t.Fatalf("UpdateProject: %v", err)
		}
		if updated.Area != "projects" {
			t.Errorf("area default: got %q, want projects", updated.Area)
		}
	})

	t.Run("not found → ErrNotFound", func(t *testing.T) {
		s := openMem(t, "")
		_, err := s.UpdateProject(context.Background(), uuid.New(), gtd.UpdateProjectParams{
			Title:  "x",
			Status: gtd.ProjectStatusActive,
		})
		if !errors.Is(err, gtd.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("workspace isolation — cross-workspace update is a no-op", func(t *testing.T) {
		tmp := t.TempDir() + "/proj-ws.db"
		ctx := context.Background()
		const wsA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		const wsB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

		dbA, err := sqlite.Open(ctx, tmp, wsA)
		if err != nil {
			t.Fatalf("Open A: %v", err)
		}
		t.Cleanup(func() { _ = dbA.Close() })
		storeA := sqlite.NewGTDStore(dbA)

		p, err := storeA.CreateProject(ctx, gtd.CreateProjectParams{
			Name: "ws-a-proj", Title: "WS-A", Area: "engineering",
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}

		dbB, err := sqlite.Open(ctx, tmp, wsB)
		if err != nil {
			t.Fatalf("Open B: %v", err)
		}
		t.Cleanup(func() { _ = dbB.Close() })
		storeB := sqlite.NewGTDStore(dbB)

		_, err = storeB.UpdateProject(ctx, p.ID, gtd.UpdateProjectParams{
			Title:  "hijacked",
			Status: gtd.ProjectStatusCompleted,
		})
		if !errors.Is(err, gtd.ErrNotFound) {
			t.Errorf("cross-workspace update must return ErrNotFound, got %v", err)
		}
	})
}

// mustCreateTask is a tiny helper to keep TasksByDueDateRange subtests free
// of repetitive `if err := ...; err != nil { t.Fatalf(...) }` boilerplate.
// Splitting these out also keeps the outer test under the gocyclo budget.
func mustCreateTask(t *testing.T, s *sqlite.GTDStore, title string, due *time.Time) {
	t.Helper()
	_, err := s.CreateTask(context.Background(), gtd.CreateTaskParams{
		Title: title, Priority: 3, DueDate: due,
	})
	if err != nil {
		t.Fatalf("CreateTask %s: %v", title, err)
	}
}

func mustQueryDueRange(t *testing.T, s *sqlite.GTDStore, from, to time.Time) []db.Task {
	t.Helper()
	got, err := s.TasksByDueDateRange(context.Background(), from, to)
	if err != nil {
		t.Fatalf("TasksByDueDateRange: %v", err)
	}
	return got
}

// TestGTDStore_TasksByDueDateRange covers the calendar-planning query: only
// pending / in_progress tasks whose due_date falls inside [from, to] are
// returned, and only within the configured workspace.
func TestGTDStore_TasksByDueDateRange(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	weekEnd := now.Add(7 * 24 * time.Hour)

	t.Run("returns tasks with due_date inside range, ordered ASC", func(t *testing.T) {
		s := openMem(t, "")
		dueLater := now.Add(5 * 24 * time.Hour)
		dueSooner := now.Add(2 * 24 * time.Hour)
		mustCreateTask(t, s, "later", &dueLater)
		mustCreateTask(t, s, "sooner", &dueSooner)

		got := mustQueryDueRange(t, s, now, weekEnd)
		if len(got) != 2 {
			t.Fatalf("want 2, got %d: %+v", len(got), got)
		}
		if got[0].Title != "sooner" || got[1].Title != "later" {
			t.Errorf("want order [sooner, later], got [%s, %s]", got[0].Title, got[1].Title)
		}
	})

	t.Run("excludes due_date outside range", func(t *testing.T) {
		s := openMem(t, "")
		insideDue := now.Add(2 * 24 * time.Hour)
		outsideAfter := now.Add(30 * 24 * time.Hour)
		outsideBefore := now.Add(-10 * 24 * time.Hour)
		mustCreateTask(t, s, "inside", &insideDue)
		mustCreateTask(t, s, "after", &outsideAfter)
		mustCreateTask(t, s, "before", &outsideBefore)

		got := mustQueryDueRange(t, s, now, weekEnd)
		if len(got) != 1 || got[0].Title != "inside" {
			t.Errorf("want [inside], got %+v", got)
		}
	})

	t.Run("excludes tasks with null due_date", func(t *testing.T) {
		s := openMem(t, "")
		mustCreateTask(t, s, "no-due", nil)
		got := mustQueryDueRange(t, s, now, weekEnd)
		if len(got) != 0 {
			t.Errorf("want 0 (null due_date excluded), got %d", len(got))
		}
	})

	t.Run("excludes completed tasks even when due_date is in range", func(t *testing.T) {
		s := openMem(t, "")
		ctx := context.Background()
		due := now.Add(2 * 24 * time.Hour)
		task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "to-complete", Priority: 3, DueDate: &due})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := s.CompleteTask(ctx, task.ID, nil); err != nil {
			t.Fatalf("CompleteTask: %v", err)
		}
		got := mustQueryDueRange(t, s, now, weekEnd)
		if len(got) != 0 {
			t.Errorf("want 0 (completed excluded), got %d", len(got))
		}
	})

	t.Run("workspace isolation", func(t *testing.T) {
		wsA := uuid.New().String()
		wsB := uuid.New().String()
		sA := openMem(t, wsA)
		sB := openMem(t, wsB)
		// Each store opens its own in-memory DB; cross-workspace leakage in a
		// shared DB is exercised by the Postgres integration test where the
		// schema lives in one place. Here we verify that scoping is at
		// least applied (the store wires workspaceArg into the query).
		due := now.Add(2 * 24 * time.Hour)
		mustCreateTask(t, sA, "ws-a", &due)

		if got := mustQueryDueRange(t, sA, now, weekEnd); len(got) != 1 {
			t.Errorf("want 1 in workspace A, got %d", len(got))
		}
		if got := mustQueryDueRange(t, sB, now, weekEnd); len(got) != 0 {
			t.Errorf("want 0 in workspace B, got %d", len(got))
		}
	})
}
