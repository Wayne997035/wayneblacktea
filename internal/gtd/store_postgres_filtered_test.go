package gtd_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// seedPGTask creates a task on the PG store with an optional due_date and an
// optional status transition. Status "" or "pending" leaves the task at its
// default pending state.
func seedPGTask(t *testing.T, store *gtd.Store, due *time.Time, status string) *gtd.Store {
	t.Helper()
	ctx := context.Background()
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:   "pg-task-" + uuid.NewString()[:8],
		DueDate: due,
		// Assignee always set: some callers transition to in_progress below,
		// which requires an owner (P6.7 domain-layer gate).
		Assignee: "claude",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if status != "" && status != statusPending {
		if _, err := store.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatus(status)); err != nil {
			t.Fatalf("UpdateTaskStatus %q: %v", status, err)
		}
	}
	return store
}

// seedPGTaskAndReturn creates a task and returns its ID for use in assertions.
func seedPGTaskAndReturn(t *testing.T, store *gtd.Store, due *time.Time, status string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:   "pg-task-" + uuid.NewString()[:8],
		DueDate: due,
		// Assignee always set: some callers transition to in_progress below,
		// which requires an owner (P6.7 domain-layer gate).
		Assignee: "claude",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if status != "" && status != statusPending {
		if _, err := store.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatus(status)); err != nil {
			t.Fatalf("UpdateTaskStatus %q: %v", status, err)
		}
	}
	return task.ID
}

// TestPGStore_TasksFiltered_StatusActive_Default verifies that TasksFiltered
// with no status (default "active") returns pending and in_progress tasks.
func TestPGStore_TasksFiltered_StatusActive_Default(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	due := time.Now().Add(24 * time.Hour)
	pendingID := seedPGTaskAndReturn(t, store, &due, "pending")
	inProgID := seedPGTaskAndReturn(t, store, &due, statusInProgress)
	_ = seedPGTaskAndReturn(t, store, &due, "completed")
	_ = seedPGTaskAndReturn(t, store, &due, "cancelled")

	tasks, err := store.TasksFiltered(ctx, gtd.TaskFilter{Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered: %v", err)
	}

	found := make(map[uuid.UUID]bool)
	for _, tk := range tasks {
		found[tk.ID] = true
		if tk.Status != statusPending && tk.Status != statusInProgress {
			t.Errorf("default active filter returned task with status %q", tk.Status)
		}
	}
	if !found[pendingID] {
		t.Errorf("pending task %s not returned", pendingID)
	}
	if !found[inProgID] {
		t.Errorf("in_progress task %s not returned", inProgID)
	}
}

// TestPGStore_TasksFiltered_StatusActive_Explicit verifies status="active"
// behaves identically to the default (empty string) filter.
func TestPGStore_TasksFiltered_StatusActive_Explicit(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	due := time.Now().Add(24 * time.Hour)
	_ = seedPGTaskAndReturn(t, store, &due, "completed")
	pendingID := seedPGTaskAndReturn(t, store, &due, "")

	tasks, err := store.TasksFiltered(ctx, gtd.TaskFilter{Status: "active", Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered(active): %v", err)
	}

	found := false
	for _, tk := range tasks {
		if tk.ID == pendingID {
			found = true
		}
		if tk.Status == statusCompleted {
			t.Errorf("status=active filter returned completed task")
		}
	}
	if !found {
		t.Errorf("pending task %s not returned under status=active", pendingID)
	}
}

// TestPGStore_TasksFiltered_StatusCompleted returns only completed tasks.
func TestPGStore_TasksFiltered_StatusCompleted(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	due := time.Now().Add(24 * time.Hour)
	completedID := seedPGTaskAndReturn(t, store, &due, "completed")
	_ = seedPGTaskAndReturn(t, store, &due, "") // pending — should not appear

	tasks, err := store.TasksFiltered(ctx, gtd.TaskFilter{Status: "completed", Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered(completed): %v", err)
	}

	found := false
	for _, tk := range tasks {
		if tk.Status != statusCompleted {
			t.Errorf("filter=completed returned task with status %q", tk.Status)
		}
		if tk.ID == completedID {
			found = true
		}
	}
	if !found {
		t.Errorf("completed task %s not returned", completedID)
	}
}

// TestPGStore_TasksFiltered_StatusCancelled returns only cancelled tasks.
func TestPGStore_TasksFiltered_StatusCancelled(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	due := time.Now().Add(24 * time.Hour)
	cancelledID := seedPGTaskAndReturn(t, store, &due, "cancelled")
	_ = seedPGTaskAndReturn(t, store, &due, "")

	tasks, err := store.TasksFiltered(ctx, gtd.TaskFilter{Status: "cancelled", Limit: 50})
	if err != nil {
		t.Fatalf("TasksFiltered(cancelled): %v", err)
	}

	found := false
	for _, tk := range tasks {
		if tk.Status != "cancelled" {
			t.Errorf("filter=cancelled returned task with status %q", tk.Status)
		}
		if tk.ID == cancelledID {
			found = true
		}
	}
	if !found {
		t.Errorf("cancelled task %s not returned", cancelledID)
	}
}

// TestPGStore_TasksFiltered_StatusAll returns open and terminal tasks.
func TestPGStore_TasksFiltered_StatusAll(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	due := time.Now().Add(24 * time.Hour)
	pendingID := seedPGTaskAndReturn(t, store, &due, "")
	completedID := seedPGTaskAndReturn(t, store, &due, "completed")
	cancelledID := seedPGTaskAndReturn(t, store, &due, "cancelled")

	tasks, err := store.TasksFiltered(ctx, gtd.TaskFilter{Status: "all", Limit: 50})
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

// TestPGStore_TasksFiltered_Pagination verifies Limit and Offset work correctly
// and has_more detection via limit+1 pattern (caller fetches Limit+1 to detect
// whether more rows exist; the Store itself returns exactly Limit rows when
// Limit+1 rows exist, so the caller detects has_more from len(tasks)==Limit).
func TestPGStore_TasksFiltered_Pagination(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	due := time.Now().Add(24 * time.Hour)
	// Seed 5 tasks.
	for i := 0; i < 5; i++ {
		seedPGTask(t, store, &due, "")
	}

	// Fetch limit=3 — should return exactly 3.
	page1, err := store.TasksFiltered(ctx, gtd.TaskFilter{Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("TasksFiltered page1: %v", err)
	}
	if len(page1) != 3 {
		t.Errorf("page1: got %d rows, want 3", len(page1))
	}

	// Fetch with offset=3 — should return 2 (5-3=2).
	page2, err := store.TasksFiltered(ctx, gtd.TaskFilter{Limit: 3, Offset: 3})
	if err != nil {
		t.Fatalf("TasksFiltered page2: %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2: got %d rows, want 2 (remaining)", len(page2))
	}

	// Fetch with offset past end — should return empty slice.
	page3, err := store.TasksFiltered(ctx, gtd.TaskFilter{Limit: 3, Offset: 100})
	if err != nil {
		t.Fatalf("TasksFiltered page3: %v", err)
	}
	if len(page3) != 0 {
		t.Errorf("page3 (past end): got %d rows, want 0", len(page3))
	}

	// Verify limit+1 has_more detection: fetch 4 where 5 exist → caller sees
	// len==4 which equals requested limit → knows more rows exist.
	hasMoreCheck, err := store.TasksFiltered(ctx, gtd.TaskFilter{Limit: 4, Offset: 0})
	if err != nil {
		t.Fatalf("TasksFiltered has_more check: %v", err)
	}
	if len(hasMoreCheck) != 4 {
		t.Errorf("has_more check: got %d rows, want 4", len(hasMoreCheck))
	}
}

// TestPGStore_TasksFiltered_ProjectIDFilter verifies that project_id scoping
// returns only tasks belonging to that project.
func TestPGStore_TasksFiltered_ProjectIDFilter(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name:  "proj-" + uuid.NewString()[:8],
		Title: "Test Project",
		Area:  "eng",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	due := time.Now().Add(24 * time.Hour)
	// Task inside the project.
	projTask, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:     "project task",
		ProjectID: &proj.ID,
		DueDate:   &due,
	})
	if err != nil {
		t.Fatalf("CreateTask (project): %v", err)
	}

	// Task outside any project — must NOT appear.
	_ = seedPGTaskAndReturn(t, store, &due, "")

	tasks, err := store.TasksFiltered(ctx, gtd.TaskFilter{
		ProjectID: &proj.ID,
		Limit:     50,
	})
	if err != nil {
		t.Fatalf("TasksFiltered(project_id): %v", err)
	}

	if len(tasks) != 1 {
		t.Errorf("expected 1 task for project, got %d", len(tasks))
	}
	if len(tasks) > 0 && tasks[0].ID != projTask.ID {
		t.Errorf("wrong task returned: got %s, want %s", tasks[0].ID, projTask.ID)
	}
}

// TestPGStore_TasksFiltered_WorkspaceScoping verifies that tasks belonging to
// a different workspace are never returned (foreign-workspace isolation).
func TestPGStore_TasksFiltered_WorkspaceScoping(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()

	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)

	due := time.Now().Add(24 * time.Hour)
	// Seed a task in workspace B.
	wsBTask, err := storeB.CreateTask(ctx, gtd.CreateTaskParams{
		Title:   "workspace-b task",
		DueDate: &due,
	})
	if err != nil {
		t.Fatalf("CreateTask (wsB): %v", err)
	}

	// Query from workspace A — workspace B task must not appear.
	tasks, err := storeA.TasksFiltered(ctx, gtd.TaskFilter{Limit: 100})
	if err != nil {
		t.Fatalf("TasksFiltered (wsA): %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == wsBTask.ID {
			t.Errorf("workspace-A store returned workspace-B task %s (workspace leak)", wsBTask.ID)
		}
	}
}

// TestPGStore_TasksFiltered_UpdatedSince verifies the UpdatedSince filter
// excludes rows updated before the cutoff and includes rows updated at/after
// it. Regression coverage for wbt-2.0 review round2 F2 (Postgres side —
// mirrors TestSQLiteStore_TasksFiltered_UpdatedSince).
func TestPGStore_TasksFiltered_UpdatedSince(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	oldID := seedPGTaskAndReturn(t, store, nil, "completed")
	recentID := seedPGTaskAndReturn(t, store, nil, "completed")
	if _, err := pool.Exec(ctx, `UPDATE tasks SET updated_at = $1 WHERE id = $2`, time.Now().Add(-30*24*time.Hour), oldID); err != nil {
		t.Fatalf("back-date oldID: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE tasks SET updated_at = $1 WHERE id = $2`, time.Now().Add(-2*24*time.Hour), recentID); err != nil {
		t.Fatalf("back-date recentID: %v", err)
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	tasks, err := store.TasksFiltered(ctx, gtd.TaskFilter{Status: "completed", Limit: 50, UpdatedSince: &cutoff})
	if err != nil {
		t.Fatalf("TasksFiltered: %v", err)
	}
	found := make(map[uuid.UUID]bool)
	for _, tk := range tasks {
		found[tk.ID] = true
	}
	if found[oldID] {
		t.Errorf("task updated 30 days ago must be excluded by UpdatedSince cutoff, but was returned")
	}
	if !found[recentID] {
		t.Errorf("task updated 2 days ago must be included (inside the cutoff window), was not returned")
	}
}

// TestPGStore_TasksFiltered_UpdatedSince_SurvivesLimitCap is the direct
// regression test for wbt-2.0 review round2 F2: seed more completed tasks
// than Limit, all updated before the cutoff except one updated after it.
// Without UpdatedSince pushed into the WHERE clause, ORDER BY priority ASC,
// created_at ASC would surface the old tasks first and the LIMIT would cut
// off before ever reaching the one recently-updated task.
func TestPGStore_TasksFiltered_UpdatedSince_SurvivesLimitCap(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	const limit = 5
	for range limit + 2 {
		id := seedPGTaskAndReturn(t, store, nil, "completed")
		if _, err := pool.Exec(ctx, `UPDATE tasks SET updated_at = $1 WHERE id = $2`, time.Now().Add(-30*24*time.Hour), id); err != nil {
			t.Fatalf("back-date old task: %v", err)
		}
	}
	recentID := seedPGTaskAndReturn(t, store, nil, "completed")
	if _, err := pool.Exec(ctx, `UPDATE tasks SET updated_at = $1 WHERE id = $2`, time.Now().Add(-2*24*time.Hour), recentID); err != nil {
		t.Fatalf("back-date recentID: %v", err)
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	tasks, err := store.TasksFiltered(ctx, gtd.TaskFilter{Status: "completed", Limit: limit, UpdatedSince: &cutoff})
	if err != nil {
		t.Fatalf("TasksFiltered: %v", err)
	}
	found := false
	for _, tk := range tasks {
		if tk.ID == recentID {
			found = true
		}
	}
	if !found {
		t.Errorf("recently-updated task %s must survive the Limit cap once UpdatedSince filters at the DB level; got %d tasks",
			recentID, len(tasks))
	}
}
