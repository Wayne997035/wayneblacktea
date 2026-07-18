package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

const (
	statusInProgress       = "in_progress"
	actionWorkSessionStart = "work_session_started"
)

// TestGTDStore_BeginTask_HappyPath verifies the basic path: pending → in_progress
// + activity_log row written.
func TestGTDStore_BeginTask_HappyPath(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "implement feature X", Priority: 2})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	result, err := s.BeginTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("BeginTask: %v", err)
	}
	if result.Status != statusInProgress {
		t.Errorf("expected status in_progress, got %q", result.Status)
	}
	if result.ID != task.ID {
		t.Errorf("expected task ID %s, got %s", task.ID, result.ID)
	}

	// Verify activity_log entry was written.
	logs, err := s.ListActivityLogsSince(ctx, task.CreatedAt.Time, 10)
	if err != nil {
		t.Fatalf("ListActivityLogsSince: %v", err)
	}
	var found bool
	for _, l := range logs {
		if l.Action == actionWorkSessionStart {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected work_session_started activity_log entry, none found in %+v", logs)
	}
}

// TestGTDStore_BeginTask_Idempotent verifies that calling BeginTask on an
// already in_progress task returns the task without error and without writing
// a duplicate activity_log entry.
func TestGTDStore_BeginTask_Idempotent(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "idempotent task", Priority: 1})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// First call — transitions to in_progress.
	if _, err := s.BeginTask(ctx, task.ID); err != nil {
		t.Fatalf("BeginTask first call: %v", err)
	}

	logsBefore, err := s.ListActivityLogsSince(ctx, task.CreatedAt.Time, 100)
	if err != nil {
		t.Fatalf("ListActivityLogsSince before second call: %v", err)
	}
	var countBefore int
	for _, l := range logsBefore {
		if l.Action == actionWorkSessionStart {
			countBefore++
		}
	}

	// Second call — should be idempotent.
	result, err := s.BeginTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("BeginTask second call (idempotent): %v", err)
	}
	if result.Status != statusInProgress {
		t.Errorf("expected in_progress on second call, got %q", result.Status)
	}

	logsAfter, err := s.ListActivityLogsSince(ctx, task.CreatedAt.Time, 100)
	if err != nil {
		t.Fatalf("ListActivityLogsSince after second call: %v", err)
	}
	var countAfter int
	for _, l := range logsAfter {
		if l.Action == actionWorkSessionStart {
			countAfter++
		}
	}
	if countAfter != countBefore {
		t.Errorf("expected no duplicate activity_log: before=%d after=%d", countBefore, countAfter)
	}
}

// TestGTDStore_BeginTask_NotFound verifies ErrNotFound is returned for a
// non-existent task ID.
func TestGTDStore_BeginTask_NotFound(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	_, err := s.BeginTask(ctx, uuid.New())
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected gtd.ErrNotFound, got %v", err)
	}
}

// TestGTDStore_BeginTask_WorkspaceIsolation verifies that BeginTask respects
// workspace scoping: a task created in workspace A cannot be begun with workspace B.
func TestGTDStore_BeginTask_WorkspaceIsolation(t *testing.T) {
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	storeA := openMem(t, wsA)
	storeB := openMem(t, wsB)
	ctx := context.Background()

	task, err := storeA.CreateTask(ctx, gtd.CreateTaskParams{Title: "workspace A task", Priority: 1})
	if err != nil {
		t.Fatalf("CreateTask in wsA: %v", err)
	}

	// Attempting to begin with workspace B store should return ErrNotFound.
	_, err = storeB.BeginTask(ctx, task.ID)
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("expected ErrNotFound when accessing cross-workspace task, got %v", err)
	}
}
