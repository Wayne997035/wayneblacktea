//go:build integration

package gtd_test

import (
	"context"
	"os"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	tlsCfg, err := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if err != nil {
		t.Fatalf("build TLS config: %v", err)
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestListActiveProjects(t *testing.T) {
	store := gtd.NewStore(setupPool(t))

	projects, err := store.ListActiveProjects(context.Background())
	if err != nil {
		t.Fatalf("ListActiveProjects: %v", err)
	}
	_ = projects
}

func TestCreateAndCompleteTask(t *testing.T) {
	pool := setupPool(t)
	store := gtd.NewStore(pool)
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
	store := gtd.NewStore(pool)
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
	store := gtd.NewStore(setupPool(t))
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
	store := gtd.NewStore(pool)
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
	store := gtd.NewStore(setupPool(t))
	ctx := context.Background()

	completed, total, err := store.WeeklyProgress(ctx)
	if err != nil {
		t.Fatalf("WeeklyProgress: %v", err)
	}
	if completed < 0 || total < 0 {
		t.Errorf("unexpected negative counts: completed=%d total=%d", completed, total)
	}
}

func TestLogActivity(t *testing.T) {
	store := gtd.NewStore(setupPool(t))
	ctx := context.Background()

	err := store.LogActivity(ctx, "test-actor", "test-action", nil, "integration test note")
	if err != nil {
		t.Fatalf("LogActivity: %v", err)
	}
}

func TestCreateTask_WithImportanceContext(t *testing.T) {
	pool := setupPool(t)
	store := gtd.NewStore(pool)
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
	store := gtd.NewStore(pool)
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
