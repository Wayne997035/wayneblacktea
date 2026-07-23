package gtd_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestStore_BuildGoalsDue_Postgres verifies BuildGoalsDue against a real
// Postgres-backed gtd.Store (testcontainers, backend-security-design.md
// §6.5): active goals with a due date are surfaced with title + days_left,
// goals without a due date are omitted, and workspace scoping is honoured.
func TestStore_BuildGoalsDue_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	now := time.Now().UTC()

	tomorrow := now.AddDate(0, 0, 1)
	if _, err := store.CreateGoal(ctx, gtd.CreateGoalParams{Title: "ship P1", DueDate: &tomorrow}); err != nil {
		t.Fatalf("CreateGoal(with due): %v", err)
	}
	if _, err := store.CreateGoal(ctx, gtd.CreateGoalParams{Title: "no due date goal"}); err != nil {
		t.Fatalf("CreateGoal(no due): %v", err)
	}

	got, err := gtd.BuildGoalsDue(ctx, store, now)
	if err != nil {
		t.Fatalf("BuildGoalsDue: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 goal (no-due-date omitted), got %d: %+v", len(got), got)
	}
	if got[0].Title != "ship P1" {
		t.Errorf("Title = %q, want %q", got[0].Title, "ship P1")
	}
	if got[0].DaysLeft != 1 {
		t.Errorf("DaysLeft = %d, want 1", got[0].DaysLeft)
	}
}

// TestStore_BuildGoalsDue_Postgres_EmptyWorkspace verifies a fresh workspace
// with no goals returns a non-nil empty slice, not an error.
func TestStore_BuildGoalsDue_Postgres_EmptyWorkspace(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)

	got, err := gtd.BuildGoalsDue(ctx, store, time.Now())
	if err != nil {
		t.Fatalf("BuildGoalsDue: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected non-nil empty slice, got %+v", got)
	}
}

// TestStore_BuildTopPending_Postgres verifies BuildTopPending against a real
// Postgres store: the highest-priority pending task is surfaced with title +
// due date, reusing StoreIface.TopPendingTask's existing ordering.
func TestStore_BuildTopPending_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)

	if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "low-prio", Priority: 5}); err != nil {
		t.Fatalf("CreateTask(low-prio): %v", err)
	}
	if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "top-prio", Priority: 1}); err != nil {
		t.Fatalf("CreateTask(top-prio): %v", err)
	}

	got, err := gtd.BuildTopPending(ctx, store)
	if err != nil {
		t.Fatalf("BuildTopPending: %v", err)
	}
	if got == nil || got.Title != "top-prio" {
		t.Fatalf("expected top-prio task, got %+v", got)
	}
	if got.DueDate != nil {
		t.Errorf("expected nil DueDate (task created with no due date), got %v", *got.DueDate)
	}
}

// TestStore_BuildTopPending_Postgres_NoPendingTasks verifies BuildTopPending
// returns nil, nil (JSON null) for a workspace with no pending tasks.
func TestStore_BuildTopPending_Postgres_NoPendingTasks(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)

	got, err := gtd.BuildTopPending(ctx, store)
	if err != nil {
		t.Fatalf("BuildTopPending: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty pending set, got %+v", got)
	}
}
