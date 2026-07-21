package gtd_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// pgPullForwardCreateTask creates a task for TestPullForwardTasks_* tests with
// an explicit priority (unlike pgUpcomingCreateTask in store_postgres_test.go,
// which hardcodes priority=2 — the ordering tests below need to control it).
func pgPullForwardCreateTask(
	t *testing.T, ctx context.Context, store *gtd.Store,
	title string, due *time.Time, importance *int16, priority int32, status gtd.TaskStatus,
) *db.Task {
	t.Helper()
	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{
		Title:      title,
		Priority:   priority,
		DueDate:    due,
		Importance: importance,
		// Assignee always set: some callers transition to in_progress below,
		// which requires an owner (P6.7 domain-layer gate).
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

func pullForwardTaskTitles(tasks []db.Task) []string {
	out := make([]string, len(tasks))
	for i, tk := range tasks {
		out[i] = tk.Title
	}
	return out
}

func pullForwardContainsTaskID(tasks []db.Task, id uuid.UUID) bool {
	for _, tk := range tasks {
		if tk.ID == id {
			return true
		}
	}
	return false
}

// TestPullForwardTasks_ImportantNoDueIncluded verifies acceptance case A:
// importance=1, due_date=NULL, status=pending → pulled forward.
func TestPullForwardTasks_ImportantNoDueIncluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)

	task := pgPullForwardCreateTask(t, ctx, store, "important-no-due", nil, &imp1, 3, "")

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if !pullForwardContainsTaskID(tasks, task.ID) {
		t.Errorf("expected importance=1/due=null task to be pulled forward; got %v", pullForwardTaskTitles(tasks))
	}
}

// TestPullForwardTasks_ImportantFutureDueIncluded verifies acceptance case B:
// importance=1, due_date=+7d → pulled forward.
func TestPullForwardTasks_ImportantFutureDueIncluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)
	futureDue := now.AddDate(0, 0, 7)

	task := pgPullForwardCreateTask(t, ctx, store, "important-future-due", &futureDue, &imp1, 3, "")

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if !pullForwardContainsTaskID(tasks, task.ID) {
		t.Errorf("expected importance=1/due=+7d task to be pulled forward; got %v", pullForwardTaskTitles(tasks))
	}
}

// TestPullForwardTasks_LowImportanceExcluded verifies acceptance case C:
// importance=2, due_date=NULL → NOT pulled forward.
func TestPullForwardTasks_LowImportanceExcluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp2 := int16(2)

	task := pgPullForwardCreateTask(t, ctx, store, "medium-importance-no-due", nil, &imp2, 3, "")

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if pullForwardContainsTaskID(tasks, task.ID) {
		t.Errorf("importance=2 task must not be pulled forward; got %v", pullForwardTaskTitles(tasks))
	}
}

// TestPullForwardTasks_DueTodayExcluded verifies acceptance case D:
// importance=1, due_date=today → NOT pulled forward (that's today's work,
// not pull-forward — the boundary is due_date >= tomorrow).
func TestPullForwardTasks_DueTodayExcluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)
	todayDue := now

	task := pgPullForwardCreateTask(t, ctx, store, "important-due-today", &todayDue, &imp1, 3, "")

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if pullForwardContainsTaskID(tasks, task.ID) {
		t.Errorf("task due today must not be pulled forward; got %v", pullForwardTaskTitles(tasks))
	}
}

// TestPullForwardTasks_CompletedExcluded verifies acceptance case E:
// importance=1, status=completed → NOT pulled forward.
func TestPullForwardTasks_CompletedExcluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)

	task := pgPullForwardCreateTask(t, ctx, store, "important-completed", nil, &imp1, 3, gtd.TaskStatusCompleted)

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if pullForwardContainsTaskID(tasks, task.ID) {
		t.Errorf("completed task must not be pulled forward; got %v", pullForwardTaskTitles(tasks))
	}
}

// TestPullForwardTasks_CancelledExcluded verifies that cancelled tasks are
// excluded — "active" means pending/in_progress only, per the target
// behaviour spec (not merely "not completed").
func TestPullForwardTasks_CancelledExcluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)

	task := pgPullForwardCreateTask(t, ctx, store, "important-cancelled", nil, &imp1, 3, gtd.TaskStatusCancelled)

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if pullForwardContainsTaskID(tasks, task.ID) {
		t.Errorf("cancelled task must not be pulled forward; got %v", pullForwardTaskTitles(tasks))
	}
}

// TestPullForwardTasks_InProgressIncluded verifies that in_progress (not just
// pending) qualifies as "active" per the target behaviour spec.
func TestPullForwardTasks_InProgressIncluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)

	task := pgPullForwardCreateTask(t, ctx, store, "important-in-progress", nil, &imp1, 3, gtd.TaskStatusInProgress)

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if !pullForwardContainsTaskID(tasks, task.ID) {
		t.Errorf("in_progress task must be pulled forward; got %v", pullForwardTaskTitles(tasks))
	}
}

// TestPullForwardTasks_CapAtFiveOrdered verifies acceptance case: 8
// qualifying tasks → exactly gtd.PullForwardCap (5) returned, ordered
// due_date ASC NULLS LAST, priority ASC. 6 due-dated tasks (ascending due
// dates, all safely >= tomorrow) plus 2 no-due-date tasks make 8 candidates;
// the cap keeps only the 5 earliest-due, so the two NULL-due tasks never
// surface (proving NULLS LAST sorts them after every due-dated task, and
// that the cap is enforced server-side, not just Go-side truncation by a
// caller).
func TestPullForwardTasks_CapAtFiveOrdered(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)

	due := func(days int) *time.Time {
		d := now.AddDate(0, 0, days)
		return &d
	}

	pgPullForwardCreateTask(t, ctx, store, "t1", due(1), &imp1, 3, "")
	pgPullForwardCreateTask(t, ctx, store, "t2", due(2), &imp1, 3, "")
	pgPullForwardCreateTask(t, ctx, store, "t3", due(3), &imp1, 1, "")
	pgPullForwardCreateTask(t, ctx, store, "t4", due(4), &imp1, 2, "")
	pgPullForwardCreateTask(t, ctx, store, "t5", due(5), &imp1, 1, "")
	pgPullForwardCreateTask(t, ctx, store, "t6", due(6), &imp1, 1, "")
	pgPullForwardCreateTask(t, ctx, store, "t7-null", nil, &imp1, 5, "")
	pgPullForwardCreateTask(t, ctx, store, "t8-null", nil, &imp1, 1, "")

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if len(tasks) != gtd.PullForwardCap {
		t.Fatalf("expected exactly %d tasks (cap), got %d: %v", gtd.PullForwardCap, len(tasks), pullForwardTaskTitles(tasks))
	}
	wantOrder := []string{"t1", "t2", "t3", "t4", "t5"}
	for i, want := range wantOrder {
		if tasks[i].Title != want {
			t.Errorf("position %d: got %q, want %q (full order: %v)", i, tasks[i].Title, want, pullForwardTaskTitles(tasks))
		}
	}
}

// TestPullForwardTasks_PriorityTiebreakSameDueDate verifies the second sort
// key: when due_date is equal, lower priority number (higher priority) sorts
// first.
func TestPullForwardTasks_PriorityTiebreakSameDueDate(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)
	sameDue := now.AddDate(0, 0, 3)

	pgPullForwardCreateTask(t, ctx, store, "prio-3", &sameDue, &imp1, 3, "")
	pgPullForwardCreateTask(t, ctx, store, "prio-1", &sameDue, &imp1, 1, "")
	pgPullForwardCreateTask(t, ctx, store, "prio-2", &sameDue, &imp1, 2, "")

	tasks, err := store.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d: %v", len(tasks), pullForwardTaskTitles(tasks))
	}
	wantOrder := []string{"prio-1", "prio-2", "prio-3"}
	for i, want := range wantOrder {
		if tasks[i].Title != want {
			t.Errorf("position %d: got %q, want %q (full order: %v)", i, tasks[i].Title, want, pullForwardTaskTitles(tasks))
		}
	}
}

// TestPullForwardTasks_WorkspaceScoping verifies each store only sees
// pull-forward candidates from its own workspace.
func TestPullForwardTasks_WorkspaceScoping(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	imp1 := int16(1)

	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)

	pgPullForwardCreateTask(t, ctx, storeA, "ws-a-important", nil, &imp1, 3, "")
	pgPullForwardCreateTask(t, ctx, storeB, "ws-b-important", nil, &imp1, 3, "")

	tasksB, err := storeB.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks (B): %v", err)
	}
	if len(tasksB) != 1 || tasksB[0].Title != "ws-b-important" {
		t.Errorf("want [ws-b-important], got %v", pullForwardTaskTitles(tasksB))
	}
}

// TestPullForwardTasks_EmptyWorkspace verifies a fresh workspace with no
// tasks returns an empty (not nil-erroring) slice.
func TestPullForwardTasks_EmptyWorkspace(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	wsEmpty := uuid.New()
	storeEmpty := newPgGTDStore(pool, &wsEmpty)

	tasks, err := storeEmpty.PullForwardTasks(ctx, now)
	if err != nil {
		t.Fatalf("PullForwardTasks (empty): %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks in empty workspace, got %d", len(tasks))
	}
}

// TestPullForwardTasks_TaipeiDayBoundary_DueTonightExcluded_DueTomorrowIncluded
// is the direct regression test for wbt-2.0 review round2 Fix 4: production
// confirmed a task due later THIS Asia/Taipei calendar day was wrongly pulled
// forward because the old boundary truncated to a UTC day (landing at Taipei
// 08:00, not midnight). refDate is Taipei 2026-07-20 05:00 (= UTC 2026-07-19
// 21:00): a task due Taipei 2026-07-20 23:00 (still today locally) MUST be
// excluded, and a task due Taipei 2026-07-21 (tomorrow locally) MUST be
// included.
func TestPullForwardTasks_TaipeiDayBoundary_DueTonightExcluded_DueTomorrowIncluded(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	imp1 := int16(1)

	loc, err := time.LoadLocation("Asia/Taipei")
	if err != nil {
		t.Fatalf("LoadLocation(Asia/Taipei): %v", err)
	}
	refDate := time.Date(2026, 7, 20, 5, 0, 0, 0, loc)
	dueTonightTaipei := time.Date(2026, 7, 20, 23, 0, 0, 0, loc)
	dueTomorrowTaipei := time.Date(2026, 7, 21, 9, 0, 0, 0, loc)

	tonightTask := pgPullForwardCreateTask(t, ctx, store, "due-tonight-taipei", &dueTonightTaipei, &imp1, 3, "")
	tomorrowTask := pgPullForwardCreateTask(t, ctx, store, "due-tomorrow-taipei", &dueTomorrowTaipei, &imp1, 3, "")

	tasks, err := store.PullForwardTasks(ctx, refDate)
	if err != nil {
		t.Fatalf("PullForwardTasks: %v", err)
	}
	if pullForwardContainsTaskID(tasks, tonightTask.ID) {
		t.Errorf("task due tonight in Taipei (still today locally) must NOT be pulled forward; got %v",
			pullForwardTaskTitles(tasks))
	}
	if !pullForwardContainsTaskID(tasks, tomorrowTask.ID) {
		t.Errorf("task due tomorrow in Taipei must be pulled forward; got %v", pullForwardTaskTitles(tasks))
	}
}
