//go:build integration

package gtd_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setupPool returns the package-level testcontainer Postgres pool initialised
// in TestMain (see pg_test_main_test.go). Earlier the function read
// `DATABASE_URL` and called `t.Skip` when unset — that silently SKIPPED every
// integration test under `go test -tags=integration` because the env var is
// never exported in CI. Mirror the `openTestPgPool` pattern used by the rest
// of the package so all PG tests use the same source-of-truth pool.
func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	if testPgPool == nil {
		t.Fatal("testPgPool not initialised — TestMain did not run or container start failed")
	}
	return testPgPool
}

func TestListActiveProjects(t *testing.T) {
	store := gtd.NewStore(setupPool(t), nil)

	projects, err := store.ListActiveProjects(context.Background())
	if err != nil {
		t.Fatalf("ListActiveProjects: %v", err)
	}
	_ = projects
}

func TestCreateAndCompleteTask(t *testing.T) {
	pool := setupPool(t)
	store := gtd.NewStore(pool, nil)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name:  "test-project-task5-" + t.Name(),
		Title: "Test Project",
		Area:  "projects",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM projects WHERE id = $1", proj.ID)
		if cleanErr != nil {
			t.Logf("cleanup project: %v", cleanErr)
		}
	})

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		ProjectID: &proj.ID,
		Title:     "Test task",
		Priority:  2,
		Assignee:  "claude-code",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", task.ID)
		if cleanErr != nil {
			t.Logf("cleanup task: %v", cleanErr)
		}
	})

	artifact := "https://github.com/test/pr/1"
	completed, err := store.CompleteTask(ctx, task.ID, &artifact)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if completed.Status != "completed" {
		t.Errorf("expected status=completed, got %s", completed.Status)
	}
	if !completed.Artifact.Valid || completed.Artifact.String != artifact {
		t.Errorf("expected artifact=%s, got %+v", artifact, completed.Artifact)
	}
}

func TestCreateTask_NoProject(t *testing.T) {
	pool := setupPool(t)
	store := gtd.NewStore(pool, nil)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:    "Orphan task",
		Priority: 1,
	})
	if err != nil {
		t.Fatalf("CreateTask without project: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", task.ID)
		if cleanErr != nil {
			t.Logf("cleanup task: %v", cleanErr)
		}
	})

	if task.ProjectID.Valid {
		t.Errorf("expected null project_id, got valid=%v bytes=%v", task.ProjectID.Valid, task.ProjectID.Bytes)
	}
}

func TestCompleteTask_NotFound(t *testing.T) {
	store := gtd.NewStore(setupPool(t), nil)
	ctx := context.Background()

	// All-zero UUID does not exist in the DB; CompleteTask must return ErrNotFound.
	nonexistent := uuid.UUID{}
	_, err := store.CompleteTask(ctx, nonexistent, nil)
	if err == nil {
		t.Fatal("expected error for non-existent task ID, got nil")
	}
}

func TestTasks_ByProject(t *testing.T) {
	pool := setupPool(t)
	store := gtd.NewStore(pool, nil)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name:  "test-get-tasks-" + t.Name(),
		Title: "Tasks project",
		Area:  "projects",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM tasks WHERE project_id = $1", proj.ID)
		_, cleanErr := pool.Exec(ctx, "DELETE FROM projects WHERE id = $1", proj.ID)
		if cleanErr != nil {
			t.Logf("cleanup project: %v", cleanErr)
		}
	})

	_, err = store.CreateTask(ctx, gtd.CreateTaskParams{
		ProjectID: &proj.ID,
		Title:     "Task A",
		Priority:  1,
	})
	if err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}

	tasks, err := store.Tasks(ctx, &proj.ID)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Error("expected at least one task for new project")
	}
}

func TestWeeklyProgress(t *testing.T) {
	store := gtd.NewStore(setupPool(t), nil)
	ctx := context.Background()

	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "invariant: completed <= total",
			fn: func(t *testing.T) {
				completed, total, err := store.WeeklyProgress(ctx)
				if err != nil {
					t.Fatalf("WeeklyProgress: %v", err)
				}
				if completed < 0 || total < 0 {
					t.Errorf("negative counts: completed=%d total=%d", completed, total)
				}
				if completed > total {
					t.Errorf("invariant broken: completed > total (%d > %d)", completed, total)
				}
			},
		},
		{
			name: "1 task created+completed this week",
			fn: func(t *testing.T) {
				task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "test_completed", Priority: 3})
				if err != nil {
					t.Fatalf("CreateTask: %v", err)
				}
				if _, err := store.CompleteTask(ctx, task.ID, nil); err != nil {
					t.Fatalf("CompleteTask: %v", err)
				}
				completed, total, err := store.WeeklyProgress(ctx)
				if err != nil {
					t.Fatalf("WeeklyProgress: %v", err)
				}
				if completed < 1 {
					t.Errorf("expected completed >= 1, got %d", completed)
				}
				if completed > total {
					t.Errorf("invariant broken: completed > total (%d > %d)", completed, total)
				}
			},
		},
		{
			name: "1 pending created this week",
			fn: func(t *testing.T) {
				if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "test_pending", Priority: 3}); err != nil {
					t.Fatalf("CreateTask: %v", err)
				}
				completed, total, err := store.WeeklyProgress(ctx)
				if err != nil {
					t.Fatalf("WeeklyProgress: %v", err)
				}
				if total < 1 {
					t.Errorf("expected total >= 1, got %d", total)
				}
				if completed > total {
					t.Errorf("invariant broken: completed > total (%d > %d)", completed, total)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.fn(t)
		})
	}
}

func TestLogActivity(t *testing.T) {
	store := gtd.NewStore(setupPool(t), nil)
	ctx := context.Background()

	err := store.LogActivity(ctx, "test-actor", "test-action", nil, "integration test note")
	if err != nil {
		t.Fatalf("LogActivity: %v", err)
	}
}

func TestCreateTask_WithImportanceContext(t *testing.T) {
	pool := setupPool(t)
	store := gtd.NewStore(pool, nil)
	ctx := context.Background()

	importance := int16(1)
	taskCtx := "Discussed in 4/27 architecture sync — must ship before Phase B."

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:      "Phase A schema upgrade",
		Priority:   2,
		Importance: &importance,
		Context:    taskCtx,
	})
	if err != nil {
		t.Fatalf("CreateTask with importance/context: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", task.ID)
		if cleanErr != nil {
			t.Logf("cleanup task: %v", cleanErr)
		}
	})

	if !task.Importance.Valid || task.Importance.Int16 != 1 {
		t.Errorf("expected importance=1, got valid=%v value=%d", task.Importance.Valid, task.Importance.Int16)
	}
	if !task.Context.Valid || task.Context.String != taskCtx {
		t.Errorf("expected context=%q, got valid=%v value=%q", taskCtx, task.Context.Valid, task.Context.String)
	}
}

func TestCreateTask_BackwardCompat_NoImportance(t *testing.T) {
	pool := setupPool(t)
	store := gtd.NewStore(pool, nil)
	ctx := context.Background()

	// Caller from Phase A or earlier that does not pass importance/context must
	// still produce a valid task with NULL in those columns.
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:    "legacy task",
		Priority: 3,
	})
	if err != nil {
		t.Fatalf("CreateTask without importance/context: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM tasks WHERE id = $1", task.ID)
		if cleanErr != nil {
			t.Logf("cleanup task: %v", cleanErr)
		}
	})

	if task.Importance.Valid {
		t.Errorf("expected NULL importance, got valid=%v value=%d", task.Importance.Valid, task.Importance.Int16)
	}
	if task.Context.Valid {
		t.Errorf("expected NULL context, got valid=%v value=%q", task.Context.Valid, task.Context.String)
	}
}
