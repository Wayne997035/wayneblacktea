package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// sqlitePullForwardCreateTask creates a task for TestPullForwardTasks_* SQLite
// parity tests with an explicit priority and optional status transition.
func sqlitePullForwardCreateTask(
	t *testing.T, ctx context.Context, s *sqlite.GTDStore,
	title string, due *time.Time, importance *int16, priority int32, status gtd.TaskStatus,
) *db.Task {
	t.Helper()
	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{
		Title:      title,
		Priority:   priority,
		DueDate:    due,
		Importance: importance,
	})
	if err != nil {
		t.Fatalf("CreateTask(%q): %v", title, err)
	}
	if status != "" && status != gtd.TaskStatusPending {
		task, err = s.UpdateTaskStatus(ctx, task.ID, status)
		if err != nil {
			t.Fatalf("UpdateTaskStatus(%q → %s): %v", title, status, err)
		}
	}
	return task
}

// TestPullForwardTasks_SQLite_ImportantNoDueIncluded mirrors PG acceptance
// case A: importance=1, due_date=NULL, status=pending → pulled forward.
func TestPullForwardTasks_SQLite_ImportantNoDueIncluded(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)

	task := sqlitePullForwardCreateTask(t, ctx, s, "important-no-due", nil, &imp1, 3, "")

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	found := false
	for _, tk := range tasks {
		if tk.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected importance=1/due=null task to be pulled forward; got %d tasks", len(tasks))
	}
}

// TestPullForwardTasks_SQLite_ImportantFutureDueIncluded mirrors PG
// acceptance case B: importance=1, due_date=+7d → pulled forward.
func TestPullForwardTasks_SQLite_ImportantFutureDueIncluded(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)
	futureDue := now.AddDate(0, 0, 7)

	task := sqlitePullForwardCreateTask(t, ctx, s, "important-future-due", &futureDue, &imp1, 3, "")

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	found := false
	for _, tk := range tasks {
		if tk.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("expected importance=1/due=+7d task to be pulled forward; got %d tasks", len(tasks))
	}
}

// TestPullForwardTasks_SQLite_LowImportanceExcluded mirrors PG acceptance
// case C: importance=2, due_date=NULL → NOT pulled forward.
func TestPullForwardTasks_SQLite_LowImportanceExcluded(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp2 := int16(2)

	task := sqlitePullForwardCreateTask(t, ctx, s, "medium-importance-no-due", nil, &imp2, 3, "")

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("importance=2 task must not be pulled forward")
		}
	}
}

// TestPullForwardTasks_SQLite_DueTodayExcluded mirrors PG acceptance case D:
// importance=1, due_date=today → NOT pulled forward.
func TestPullForwardTasks_SQLite_DueTodayExcluded(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)
	todayDue := now

	task := sqlitePullForwardCreateTask(t, ctx, s, "important-due-today", &todayDue, &imp1, 3, "")

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("task due today must not be pulled forward")
		}
	}
}

// TestPullForwardTasks_SQLite_CompletedExcluded mirrors PG acceptance case E:
// importance=1, status=completed → NOT pulled forward.
func TestPullForwardTasks_SQLite_CompletedExcluded(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)

	task := sqlitePullForwardCreateTask(t, ctx, s, "important-completed", nil, &imp1, 3, gtd.TaskStatusCompleted)

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("completed task must not be pulled forward")
		}
	}
}

// TestPullForwardTasks_SQLite_CancelledExcluded verifies cancelled tasks are
// excluded — "active" means pending/in_progress only.
func TestPullForwardTasks_SQLite_CancelledExcluded(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)

	task := sqlitePullForwardCreateTask(t, ctx, s, "important-cancelled", nil, &imp1, 3, gtd.TaskStatusCancelled)

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == task.ID {
			t.Errorf("cancelled task must not be pulled forward")
		}
	}
}

// TestPullForwardTasks_SQLite_InProgressIncluded verifies in_progress tasks
// qualify as "active".
func TestPullForwardTasks_SQLite_InProgressIncluded(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)

	task := sqlitePullForwardCreateTask(t, ctx, s, "important-in-progress", nil, &imp1, 3, gtd.TaskStatusInProgress)

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	found := false
	for _, tk := range tasks {
		if tk.ID == task.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("in_progress task must be pulled forward")
	}
}

// TestPullForwardTasks_SQLite_CapAtFiveOrdered mirrors the PG cap+ordering
// test: 8 qualifying tasks (6 due-dated ascending + 2 no-due-date) → exactly
// gtd.PullForwardCap (5) returned, the 5 earliest due-dated tasks, proving
// SQLite's NULLS LAST equivalent behaves the same as Postgres.
func TestPullForwardTasks_SQLite_CapAtFiveOrdered(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)

	due := func(days int) *time.Time {
		d := now.AddDate(0, 0, days)
		return &d
	}

	sqlitePullForwardCreateTask(t, ctx, s, "t1", due(1), &imp1, 3, "")
	sqlitePullForwardCreateTask(t, ctx, s, "t2", due(2), &imp1, 3, "")
	sqlitePullForwardCreateTask(t, ctx, s, "t3", due(3), &imp1, 1, "")
	sqlitePullForwardCreateTask(t, ctx, s, "t4", due(4), &imp1, 2, "")
	sqlitePullForwardCreateTask(t, ctx, s, "t5", due(5), &imp1, 1, "")
	sqlitePullForwardCreateTask(t, ctx, s, "t6", due(6), &imp1, 1, "")
	sqlitePullForwardCreateTask(t, ctx, s, "t7-null", nil, &imp1, 5, "")
	sqlitePullForwardCreateTask(t, ctx, s, "t8-null", nil, &imp1, 1, "")

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if len(tasks) != gtd.PullForwardCap {
		titles := make([]string, len(tasks))
		for i, tk := range tasks {
			titles[i] = tk.Title
		}
		t.Fatalf("expected exactly %d tasks (cap), got %d: %v", gtd.PullForwardCap, len(tasks), titles)
	}
	wantOrder := []string{"t1", "t2", "t3", "t4", "t5"}
	for i, want := range wantOrder {
		if tasks[i].Title != want {
			t.Errorf("position %d: got %q, want %q", i, tasks[i].Title, want)
		}
	}
}

// TestPullForwardTasks_SQLite_PriorityTiebreakSameDueDate verifies the
// second sort key: when due_date is equal, lower priority number sorts first.
func TestPullForwardTasks_SQLite_PriorityTiebreakSameDueDate(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)
	sameDue := now.AddDate(0, 0, 3)

	sqlitePullForwardCreateTask(t, ctx, s, "prio-3", &sameDue, &imp1, 3, "")
	sqlitePullForwardCreateTask(t, ctx, s, "prio-1", &sameDue, &imp1, 1, "")
	sqlitePullForwardCreateTask(t, ctx, s, "prio-2", &sameDue, &imp1, 2, "")

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	wantOrder := []string{"prio-1", "prio-2", "prio-3"}
	for i, want := range wantOrder {
		if tasks[i].Title != want {
			t.Errorf("position %d: got %q, want %q", i, tasks[i].Title, want)
		}
	}
}

// TestPullForwardTasks_SQLite_WorkspaceScoping verifies each in-memory store
// is isolated — sA cannot see sB's importance=1 tasks.
func TestPullForwardTasks_SQLite_WorkspaceScoping(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	sA := openMem(t, uuid.New().String())
	sB := openMem(t, uuid.New().String())
	imp1 := int16(1)

	sqlitePullForwardCreateTask(t, ctx, sA, "ws-a-important", nil, &imp1, 3, "")
	sqlitePullForwardCreateTask(t, ctx, sB, "ws-b-important", nil, &imp1, 3, "")

	tasksB, err := sB.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks (B): %v", err)
	}
	if len(tasksB) != 1 || tasksB[0].Title != "ws-b-important" {
		t.Errorf("want [ws-b-important], got %d tasks", len(tasksB))
	}
}

// TestPullForwardTasks_SQLite_EmptyWorkspace verifies a fresh store with no
// tasks returns an empty (not nil-erroring) slice.
func TestPullForwardTasks_SQLite_EmptyWorkspace(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := openMem(t, uuid.New().String())

	tasks, err := s.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks (empty): %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

// TestPullForwardTasks_SQLite_TaipeiDayBoundary_DueTonightExcluded_DueTomorrowIncluded
// mirrors the PG regression test for wbt-2.0 review round2 Fix 4: refDate is
// Taipei 2026-07-20 05:00 (= UTC 2026-07-19 21:00); a task due Taipei
// 2026-07-20 23:00 (still today locally) MUST be excluded, and a task due
// Taipei 2026-07-21 (tomorrow locally) MUST be included.
func TestPullForwardTasks_SQLite_TaipeiDayBoundary_DueTonightExcluded_DueTomorrowIncluded(t *testing.T) {
	ctx := context.Background()
	s := openMem(t, uuid.New().String())
	imp1 := int16(1)

	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("LoadLocation(Asia/Taipei): %v", err)
	}
	refDate := time.Date(2026, 7, 20, 5, 0, 0, 0, loc)
	dueTonightTaipei := time.Date(2026, 7, 20, 23, 0, 0, 0, loc)
	dueTomorrowTaipei := time.Date(2026, 7, 21, 9, 0, 0, 0, loc)

	tonightTask := sqlitePullForwardCreateTask(t, ctx, s, "due-tonight-taipei", &dueTonightTaipei, &imp1, 3, "")
	tomorrowTask := sqlitePullForwardCreateTask(t, ctx, s, "due-tomorrow-taipei", &dueTomorrowTaipei, &imp1, 3, "")

	tasks, err := s.PullForwardTasks(ctx, refDate)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	found := make(map[uuid.UUID]bool)
	for _, tk := range tasks {
		found[tk.ID] = true
	}
	if found[tonightTask.ID] {
		t.Errorf("task due tonight in Taipei (still today locally) must NOT be pulled forward; got %d tasks", len(tasks))
	}
	if !found[tomorrowTask.ID] {
		t.Errorf("task due tomorrow in Taipei must be pulled forward; got %d tasks", len(tasks))
	}
}
