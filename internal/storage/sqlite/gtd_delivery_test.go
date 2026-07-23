package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestGTDStore_BuildGoalsDue_SQLite verifies gtd.BuildGoalsDue against a
// real SQLite-backed GTDStore (backend-security-design.md §6.5: SQLite is
// the no-container exception, still a real file/:memory: DB, never mocked),
// mirroring TestStore_BuildGoalsDue_Postgres so both backends are proven to
// produce identical delivery-visibility output shapes.
func TestGTDStore_BuildGoalsDue_SQLite(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()
	now := time.Now().UTC()

	tomorrow := now.AddDate(0, 0, 1)
	if _, err := s.CreateGoal(ctx, gtd.CreateGoalParams{Title: "ship P1", DueDate: &tomorrow}); err != nil {
		t.Fatalf("CreateGoal(with due): %v", err)
	}
	if _, err := s.CreateGoal(ctx, gtd.CreateGoalParams{Title: "no due date goal"}); err != nil {
		t.Fatalf("CreateGoal(no due): %v", err)
	}

	got, err := gtd.BuildGoalsDue(ctx, s, now)
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

// TestGTDStore_BuildGoalsDue_SQLite_EmptyWorkspace verifies a fresh SQLite
// DB with no goals returns a non-nil empty slice, not an error.
func TestGTDStore_BuildGoalsDue_SQLite_EmptyWorkspace(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	got, err := gtd.BuildGoalsDue(ctx, s, time.Now())
	if err != nil {
		t.Fatalf("BuildGoalsDue: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected non-nil empty slice, got %+v", got)
	}
}

// TestGTDStore_BuildTopPending_SQLite mirrors
// TestStore_BuildTopPending_Postgres against SQLite.
func TestGTDStore_BuildTopPending_SQLite(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	if _, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "low-prio", Priority: 5}); err != nil {
		t.Fatalf("CreateTask(low-prio): %v", err)
	}
	if _, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "top-prio", Priority: 1}); err != nil {
		t.Fatalf("CreateTask(top-prio): %v", err)
	}

	got, err := gtd.BuildTopPending(ctx, s)
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

// TestGTDStore_BuildTopPending_SQLite_NoPendingTasks verifies BuildTopPending
// returns nil, nil (JSON null) for an empty SQLite DB.
func TestGTDStore_BuildTopPending_SQLite_NoPendingTasks(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	got, err := gtd.BuildTopPending(ctx, s)
	if err != nil {
		t.Fatalf("BuildTopPending: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty pending set, got %+v", got)
	}
}
