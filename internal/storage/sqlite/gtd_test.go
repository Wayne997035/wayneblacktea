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
