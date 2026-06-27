package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// seedSQLiteTask creates a task in the GTDStore with an optional due_date and
// status. status "" is treated as "pending" (the default).
func seedSQLiteTask(t *testing.T, s *sqlite.GTDStore, due *time.Time, status string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title:   "sqlite-task-" + uuid.NewString()[:8],
		DueDate: due,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if status != "" && status != "pending" {
		if _, err := s.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatus(status)); err != nil {
			t.Fatalf("UpdateTaskStatus %q: %v", status, err)
		}
	}
	return task.ID
}

// TestSQLiteStore_TasksFiltered_ActiveDefault verifies that empty status ("") and
// "active" both return pending + in_progress tasks and omit completed/cancelled.
func TestSQLiteStore_TasksFiltered_ActiveDefault(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	due := time.Now().Add(24 * time.Hour)

	pendingID := seedSQLiteTask(t, s, &due, "pending")
	inProgID := seedSQLiteTask(t, s, &due, "in_progress")
	_ = seedSQLiteTask(t, s, &due, "completed")
	_ = seedSQLiteTask(t, s, &due, "cancelled")

	for _, status := range []string{"", "active"} {
		status := status // capture
		t.Run("status="+status, func(t *testing.T) {
			tasks, err := s.TasksFiltered(ctx, gtd.TaskFilter{Status: status, Limit: 50})
			if err != nil {
				t.Fatalf("TasksFiltered: %v", err)
			}
			found := make(map[uuid.UUID]bool)
			for _, tk := range tasks {
				found[tk.ID] = true
				if tk.Status != "pending" && tk.Status != "in_progress" {
					t.Errorf("active filter returned task with status %q", tk.Status)
				}
			}
			if !found[pendingID] {
				t.Errorf("pending task %s not returned", pendingID)
			}
			if !found[inProgID] {
				t.Errorf("in_progress task %s not returned", inProgID)
			}
		})
	}
}

// TestSQLiteStore_TasksFiltered_StatusCompleted returns only completed tasks.
func TestSQLiteStore_TasksFiltered_StatusCompleted(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	due := time.Now().Add(24 * time.Hour)

	completedID := seedSQLiteTask(t, s, &due, "completed")
	_ = seedSQLiteTask(t, s, &due, "") // pending — must not appear

	tasks, err := s.TasksFiltered(ctx, gtd.TaskFilter{Status: "completed", Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered(completed): %v", err)
	}
	found := false
	for _, tk := range tasks {
		if tk.Status != "completed" {
			t.Errorf("status=completed returned task with status %q", tk.Status)
		}
		if tk.ID == completedID {
			found = true
		}
	}
	if !found {
		t.Errorf("completed task %s not returned", completedID)
	}
}

// TestSQLiteStore_TasksFiltered_StatusCancelled returns only cancelled tasks.
func TestSQLiteStore_TasksFiltered_StatusCancelled(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	due := time.Now().Add(24 * time.Hour)

	cancelledID := seedSQLiteTask(t, s, &due, "cancelled")
	_ = seedSQLiteTask(t, s, &due, "")

	tasks, err := s.TasksFiltered(ctx, gtd.TaskFilter{Status: "cancelled", Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered(cancelled): %v", err)
	}
	found := false
	for _, tk := range tasks {
		if tk.Status != "cancelled" {
			t.Errorf("status=cancelled returned task with status %q", tk.Status)
		}
		if tk.ID == cancelledID {
			found = true
		}
	}
	if !found {
		t.Errorf("cancelled task %s not returned", cancelledID)
	}
}

// TestSQLiteStore_TasksFiltered_StatusAll returns open and terminal tasks.
func TestSQLiteStore_TasksFiltered_StatusAll(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	due := time.Now().Add(24 * time.Hour)

	pendingID := seedSQLiteTask(t, s, &due, "")
	completedID := seedSQLiteTask(t, s, &due, "completed")
	cancelledID := seedSQLiteTask(t, s, &due, "cancelled")

	tasks, err := s.TasksFiltered(ctx, gtd.TaskFilter{Status: "all", Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered(all): %v", err)
	}
	found := make(map[uuid.UUID]bool)
	for _, tk := range tasks {
		found[tk.ID] = true
	}
	for _, id := range []uuid.UUID{pendingID, completedID, cancelledID} {
		if !found[id] {
			t.Errorf("status=all did not return task %s", id)
		}
	}
}

// TestSQLiteStore_TasksFiltered_Pagination verifies Limit and Offset work.
func TestSQLiteStore_TasksFiltered_Pagination(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	due := time.Now().Add(24 * time.Hour)

	for i := 0; i < 5; i++ {
		seedSQLiteTask(t, s, &due, "")
	}

	page1, err := s.TasksFiltered(ctx, gtd.TaskFilter{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("TasksFiltered page1: %v", err)
	}
	if len(page1) != 3 {
		t.Errorf("page1: got %d rows, want 3", len(page1))
	}

	page2, err := s.TasksFiltered(ctx, gtd.TaskFilter{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("TasksFiltered page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2: got %d rows, want 2", len(page2))
	}

	page3, err := s.TasksFiltered(ctx, gtd.TaskFilter{Limit: 3, Offset: 100})
	if err != nil {
		t.Fatalf("TasksFiltered page3 (past end): %v", err)
	}
	if len(page3) != 0 {
		t.Errorf("page3 past end: got %d rows, want 0", len(page3))
	}
}

// TestSQLiteStore_TasksFiltered_ProjectIDFilter verifies project-scoped queries.
func TestSQLiteStore_TasksFiltered_ProjectIDFilter(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	proj, err := s.CreateProject(ctx, gtd.CreateProjectParams{
		Name:  "proj-" + uuid.NewString()[:8],
		Title: "Filter Test Project",
		Area:  "eng",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	due := time.Now().Add(24 * time.Hour)
	projTask, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title:     "project task",
		ProjectID: &proj.ID,
		DueDate:   &due,
	})
	if err != nil {
		t.Fatalf("CreateTask (project): %v", err)
	}
	_ = seedSQLiteTask(t, s, &due, "") // unscoped task — must not appear

	tasks, err := s.TasksFiltered(ctx, gtd.TaskFilter{ProjectID: &proj.ID, Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered(project_id): %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task for project, got %d", len(tasks))
	}
	if len(tasks) > 0 && tasks[0].ID != projTask.ID {
		t.Errorf("wrong task: got %s, want %s", tasks[0].ID, projTask.ID)
	}
}

// TestSQLiteStore_TasksFiltered_WorkspaceScoping verifies that tasks from a
// different workspace (separate in-memory DB) are not returned.
func TestSQLiteStore_TasksFiltered_WorkspaceScoping(t *testing.T) {
	wsA := uuid.NewString()
	wsB := uuid.NewString()
	storeA := openMem(t, wsA)
	storeB := openMem(t, wsB)
	ctx := context.Background()

	due := time.Now().Add(24 * time.Hour)
	wsBTaskID := seedSQLiteTask(t, storeB, &due, "")

	tasks, err := storeA.TasksFiltered(ctx, gtd.TaskFilter{Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered (wsA): %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == wsBTaskID {
			t.Errorf("workspace-A store returned workspace-B task %s", wsBTaskID)
		}
	}
}

// TestSQLiteStore_TasksFiltered_EmptyDB_NoError verifies that querying an empty
// database returns an empty result (not nil error, not panic).
func TestSQLiteStore_TasksFiltered_EmptyDB_NoError(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	tasks, err := s.TasksFiltered(ctx, gtd.TaskFilter{Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered on empty DB: %v", err)
	}
	// nil or empty slice are both acceptable.
	if len(tasks) != 0 {
		t.Errorf("empty DB should return 0 tasks, got %d", len(tasks))
	}
}
