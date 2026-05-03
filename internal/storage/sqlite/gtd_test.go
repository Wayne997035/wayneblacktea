package sqlite_test

import (
	"context"
	"errors"
	"testing"

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
