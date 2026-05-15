package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callUpdateTask invokes handleUpdateTask with the given args. Never returns a
// Go error; tool-level errors surface via CallToolResult.IsError.
func callUpdateTask(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleUpdateTask(context.Background(), req)
	if err != nil {
		t.Fatalf("handleUpdateTask error: %v", err)
	}
	return res
}

// callGetUpcomingWork invokes handleGetUpcomingWork with the given args.
func callGetUpcomingWork(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleGetUpcomingWork(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGetUpcomingWork error: %v", err)
	}
	return res
}

// --- parseUpdateTaskArgs validation ---

func TestParseUpdateTaskArgs_InvalidStatus(t *testing.T) {
	_, msg := parseUpdateTaskArgs(map[string]any{"status": "completed"})
	if msg == "" {
		t.Fatal("expected error for completed status via update_task")
	}
	if !strings.Contains(msg, "pending") {
		t.Errorf("error should mention valid statuses, got: %s", msg)
	}
}

func TestParseUpdateTaskArgs_ValidStatuses(t *testing.T) {
	for _, s := range []string{"pending", "in_progress", "cancelled"} {
		p, msg := parseUpdateTaskArgs(map[string]any{"status": s})
		if msg != "" {
			t.Errorf("status %q should be valid, got error: %s", s, msg)
		}
		if p.Status == nil || *p.Status != s {
			t.Errorf("status %q not propagated correctly", s)
		}
	}
}

func TestParseUpdateTaskArgs_PriorityOutOfRange(t *testing.T) {
	_, msg := parseUpdateTaskArgs(map[string]any{"priority": float64(6)})
	if msg == "" {
		t.Fatal("expected error for priority=6")
	}
}

func TestParseUpdateTaskArgs_ImportanceOutOfRange(t *testing.T) {
	_, msg := parseUpdateTaskArgs(map[string]any{"importance": float64(4)})
	if msg == "" {
		t.Fatal("expected error for importance=4")
	}
}

func TestParseUpdateTaskArgs_InvalidDueDate(t *testing.T) {
	_, msg := parseUpdateTaskArgs(map[string]any{"due_date": "not-a-date"})
	if msg == "" {
		t.Fatal("expected error for bad due_date")
	}
	if !strings.Contains(msg, "RFC3339") {
		t.Errorf("error should mention RFC3339, got: %s", msg)
	}
}

func TestParseUpdateTaskArgs_ValidDueDate(t *testing.T) {
	p, msg := parseUpdateTaskArgs(map[string]any{"due_date": "2026-12-31T00:00:00Z"})
	if msg != "" {
		t.Fatalf("valid RFC3339 should pass, got: %s", msg)
	}
	if p.DueDate == nil {
		t.Fatal("DueDate should be set")
	}
}

// --- handleUpdateTask validation paths ---

func TestUpdateTask_MissingTaskID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateTask(t, s, map[string]any{"status": "pending"})
	if !r.IsError {
		t.Fatalf("missing task_id must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "task_id") {
		t.Errorf("error should mention task_id, got: %s", resultText(r))
	}
}

func TestUpdateTask_InvalidUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateTask(t, s, map[string]any{"task_id": "not-a-uuid", "status": "pending"})
	if !r.IsError {
		t.Fatalf("invalid UUID must error, got: %s", resultText(r))
	}
}

func TestUpdateTask_AllNilParamsFails(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{"task_id": id.String()})
	if !r.IsError {
		t.Fatalf("no fields provided must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "at least one field") {
		t.Errorf("error should mention 'at least one field', got: %s", resultText(r))
	}
}

func TestUpdateTask_InvalidStatusReturnsError(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{"task_id": id.String(), "status": "completed"})
	if !r.IsError {
		t.Fatalf("status=completed via update_task must error, got: %s", resultText(r))
	}
}

func TestUpdateTask_StatusToInProgress(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{"task_id": id.String(), "status": "in_progress"})
	if r.IsError {
		t.Fatalf("status=in_progress must succeed, got: %s", resultText(r))
	}
	// Verify task status was actually updated.
	tasks, err := s.gtd.Tasks(context.Background(), nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	var found bool
	for _, tk := range tasks {
		if tk.ID == id {
			found = true
			if tk.Status != string(gtd.TaskStatusInProgress) {
				t.Errorf("task.Status = %q, want %q", tk.Status, gtd.TaskStatusInProgress)
			}
			break
		}
	}
	if !found {
		t.Fatal("task not found after update")
	}
}

// --- handleGetUpcomingWork ---

func TestGetUpcomingWork_EmptyDB(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetUpcomingWork(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("empty DB should return success, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "Upcoming tasks") {
		t.Errorf("result should contain 'Upcoming tasks', got: %s", resultText(r))
	}
}

func TestGetUpcomingWork_DaysClampedTo14(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetUpcomingWork(t, s, map[string]any{"days": float64(999)})
	if r.IsError {
		t.Fatalf("oversized days should succeed (clamped), got: %s", resultText(r))
	}
}

func TestGetUpcomingWork_DefaultsApplied(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetUpcomingWork(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("no-arg call must succeed, got: %s", resultText(r))
	}
	// Default window is 7 days — header says "next 7 days".
	if !strings.Contains(resultText(r), "7 days") {
		t.Errorf("expected '7 days' in header, got: %s", resultText(r))
	}
}
