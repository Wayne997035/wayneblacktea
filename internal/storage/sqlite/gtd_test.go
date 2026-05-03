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

// TestGTDStore_DeleteTask_WorkspaceMismatch ensures the workspace filter on
// the parent DELETE is respected. A task created in workspace A cannot be
// deleted by a store scoped to workspace B; the parent DELETE affects 0 rows.
// (Cleanup statements are keyed by task_id only; if a different-workspace
// row pointed at this UUID it would be cleaned, but UUIDs are globally
// unique so this only runs for the legitimate task.)
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
	// CreateTask via the store uses workspace_id from the store config; verify
	// the row carries workspace A.
	var rowWS *string
	if err := dA.QueryRowContext(ctx,
		`SELECT workspace_id FROM tasks WHERE id = ?1`, task.ID.String(),
	).Scan(&rowWS); err != nil {
		t.Fatalf("read workspace_id: %v", err)
	}
	if rowWS == nil || *rowWS != wsA {
		t.Fatalf("expected task workspace_id=%s, got %v", wsA, rowWS)
	}

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

	// Task should still exist when read from workspace A.
	tasks, _ := storeA.Tasks(ctx, nil)
	found := false
	for _, tk := range tasks {
		if tk.ID == task.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("task should NOT have been deleted (workspace mismatch); it disappeared")
	}
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
