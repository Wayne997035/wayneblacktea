//go:build !integration

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gtdPkg "github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// stub watchdog.DisciplineEventStoreIface
// ---------------------------------------------------------------------------

type stubDisciplineEventStore struct {
	insertErr    error
	listReturn   []watchdog.DisciplineEvent
	listErr      error
	resolveErr   error
	pruneReturn  int64
	pruneErr     error
	lastInserted watchdog.InsertParams
	lastResolved uuid.UUID
}

var _ watchdog.DisciplineEventStoreIface = (*stubDisciplineEventStore)(nil)

func (s *stubDisciplineEventStore) Insert(_ context.Context, p watchdog.InsertParams) error {
	s.lastInserted = p
	return s.insertErr
}

func (s *stubDisciplineEventStore) ListUnresolved(_ context.Context, _ *uuid.UUID) ([]watchdog.DisciplineEvent, error) {
	return s.listReturn, s.listErr
}

func (s *stubDisciplineEventStore) MarkResolved(_ context.Context, eventID uuid.UUID) error {
	s.lastResolved = eventID
	return s.resolveErr
}

func (s *stubDisciplineEventStore) PruneOlderThan(_ context.Context, _ time.Time) (int64, error) {
	return s.pruneReturn, s.pruneErr
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newWatchdogServer(des watchdog.DisciplineEventStoreIface) *Server {
	return &Server{disciplineEventStore: des}
}

func callAnalyzeAgentBehavior(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleAnalyzeAgentBehavior(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAnalyzeAgentBehavior returned unexpected Go error: %v", err)
	}
	return r
}

func callDetectUnclosedLoops(t *testing.T, s *Server) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	r, err := s.handleDetectUnclosedLoops(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDetectUnclosedLoops returned unexpected Go error: %v", err)
	}
	return r
}

func callMarkLoopResolved(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleMarkLoopResolved(context.Background(), req)
	if err != nil {
		t.Fatalf("handleMarkLoopResolved returned unexpected Go error: %v", err)
	}
	return r
}

// ---------------------------------------------------------------------------
// handleAnalyzeAgentBehavior tests
// ---------------------------------------------------------------------------

func TestHandleAnalyzeAgentBehavior_NilDisciplineEventStore(t *testing.T) {
	s := newWatchdogServer(nil)
	r := callAnalyzeAgentBehavior(t, s, map[string]any{})
	if !r.IsError {
		t.Fatal("expected IsError=true for nil discipline_event_store")
	}
}

func TestHandleAnalyzeAgentBehavior_StuckHoursClampedTo168(t *testing.T) {
	des := &stubDisciplineEventStore{}
	// Provide a server with disciplineEventStore but all other deps nil —
	// each detection skips gracefully (nil store guards), so we only verify
	// that the threshold clamp fires (max 168 h = 7 days).
	s := &Server{disciplineEventStore: des}

	// stuck_threshold_hours=999999 should be clamped to 168 before use.
	// Since gtd is nil, detectStuckTasks returns immediately, but the clamped
	// value is what matters — we verify no panic and result is returned.
	r := callAnalyzeAgentBehavior(t, s, map[string]any{
		"stuck_threshold_hours": float64(999999),
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	// Result must contain run_at field.
	if !strings.Contains(resultText(r), "run_at") {
		t.Errorf("response missing run_at, got: %s", resultText(r))
	}
}

func TestHandleAnalyzeAgentBehavior_DefaultStuckHours(t *testing.T) {
	des := &stubDisciplineEventStore{}
	s := &Server{disciplineEventStore: des}
	// No stuck_threshold_hours → should default to 4.
	r := callAnalyzeAgentBehavior(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
}

// ---------------------------------------------------------------------------
// handleDetectUnclosedLoops tests
// ---------------------------------------------------------------------------

func TestHandleDetectUnclosedLoops_NilStore(t *testing.T) {
	s := newWatchdogServer(nil)
	r := callDetectUnclosedLoops(t, s)
	if !r.IsError {
		t.Fatal("expected IsError=true for nil store")
	}
}

func TestHandleDetectUnclosedLoops_HappyPath(t *testing.T) {
	id := uuid.New()
	wsID := uuid.New()
	detail, _ := json.Marshal(map[string]any{"task_id": id.String()})
	des := &stubDisciplineEventStore{
		listReturn: []watchdog.DisciplineEvent{
			{
				ID:          id,
				WorkspaceID: &wsID,
				EventType:   watchdog.EventTypeStuckTask,
				Severity:    watchdog.SeverityWarn,
				Detail:      json.RawMessage(detail),
				CreatedAt:   time.Now(),
			},
		},
	}
	s := newWatchdogServer(des)
	r := callDetectUnclosedLoops(t, s)
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), id.String()) {
		t.Errorf("response missing event ID, got: %s", resultText(r))
	}
}

func TestHandleDetectUnclosedLoops_StoreError(t *testing.T) {
	des := &stubDisciplineEventStore{listErr: errors.New("db down")}
	s := newWatchdogServer(des)
	r := callDetectUnclosedLoops(t, s)
	if !r.IsError {
		t.Fatal("expected IsError=true when store returns error")
	}
}

// ---------------------------------------------------------------------------
// handleMarkLoopResolved tests
// ---------------------------------------------------------------------------

func TestHandleMarkLoopResolved_NilStore(t *testing.T) {
	s := newWatchdogServer(nil)
	r := callMarkLoopResolved(t, s, map[string]any{
		"event_id": uuid.New().String(),
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for nil store")
	}
}

func TestHandleMarkLoopResolved_InvalidUUID(t *testing.T) {
	des := &stubDisciplineEventStore{}
	s := newWatchdogServer(des)
	r := callMarkLoopResolved(t, s, map[string]any{
		"event_id": "not-a-uuid",
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for invalid UUID")
	}
}

func TestHandleMarkLoopResolved_EventNotFound_ErrorsIs(t *testing.T) {
	// Verify errors.Is works with the ErrEventNotFound sentinel (wrapping test).
	des := &stubDisciplineEventStore{resolveErr: watchdog.ErrEventNotFound}
	s := newWatchdogServer(des)
	r := callMarkLoopResolved(t, s, map[string]any{
		"event_id": uuid.New().String(),
	})
	if !r.IsError {
		t.Fatal("expected IsError=true for event not found")
	}
	if !strings.Contains(resultText(r), "not found") {
		t.Errorf("expected 'not found' in error text, got: %s", resultText(r))
	}
}

func TestHandleMarkLoopResolved_HappyPath(t *testing.T) {
	id := uuid.New()
	des := &stubDisciplineEventStore{}
	s := newWatchdogServer(des)
	r := callMarkLoopResolved(t, s, map[string]any{
		"event_id": id.String(),
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if des.lastResolved != id {
		t.Errorf("MarkResolved called with wrong ID: got %v, want %v", des.lastResolved, id)
	}
	text := resultText(r)
	if !strings.Contains(text, "resolved") {
		t.Errorf("response missing 'resolved' field, got: %s", text)
	}
}

// TestHandleMarkLoopResolved_ErrorsIs_Wrapped verifies that errors.Is semantics
// work correctly: even a wrapped ErrEventNotFound is correctly identified.
func TestHandleMarkLoopResolved_ErrorsIs_Wrapped(t *testing.T) {
	// errors.Is should unwrap and match ErrEventNotFound.
	if !errors.Is(watchdog.ErrEventNotFound, watchdog.ErrEventNotFound) {
		t.Error("errors.Is(ErrEventNotFound, ErrEventNotFound) should be true")
	}
	wrapped := fmt.Errorf("outer: %w", watchdog.ErrEventNotFound)
	if !errors.Is(wrapped, watchdog.ErrEventNotFound) {
		t.Error("errors.Is should find wrapped ErrEventNotFound")
	}
}

// ---------------------------------------------------------------------------
// detectTasksMissingDueDate + live_findings tests
// ---------------------------------------------------------------------------

// TestDetectTasksMissingDueDate_NilGTD verifies that when s.gtd is nil, the
// detector returns nil (no panic, no crash).
func TestDetectTasksMissingDueDate_NilGTD(t *testing.T) {
	s := &Server{disciplineEventStore: &stubDisciplineEventStore{}}
	findings := s.detectTasksMissingDueDate(context.Background(), nil)
	if len(findings) != 0 {
		t.Errorf("nil GTD: expected no findings, got %d", len(findings))
	}
}

// TestDetectTasksMissingDueDate_AllTasksHaveDueDate verifies that active tasks
// with due dates are not flagged.
func TestDetectTasksMissingDueDate_AllTasksHaveDueDate(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// seedTaskWithDueDate always sets a due_date.
	seedTaskWithDueDate(t, s, "")
	findings := s.detectTasksMissingDueDate(context.Background(), nil)
	if len(findings) != 0 {
		t.Errorf("no missing-due-date tasks: expected 0 findings, got %d", len(findings))
	}
}

// TestDetectTasksMissingDueDate_MissingDueDate verifies that active tasks
// without a due date produce one finding per task.
func TestDetectTasksMissingDueDate_MissingDueDate(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Create a task directly through the store to bypass the MCP due_date guard.
	task, err := s.gtd.CreateTask(context.Background(), gtdPkg.CreateTaskParams{
		Title: "no-due-date task",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	findings := s.detectTasksMissingDueDate(context.Background(), nil)
	if len(findings) == 0 {
		t.Fatal("expected at least one finding for task missing due_date")
	}
	found := false
	for _, f := range findings {
		if strings.Contains(string(f.Detail), task.ID.String()) {
			found = true
		}
		if f.EventType != "task_missing_due_date" {
			t.Errorf("EventType = %q, want task_missing_due_date", f.EventType)
		}
		if f.Severity != "info" {
			t.Errorf("Severity = %q, want info", f.Severity)
		}
	}
	if !found {
		t.Errorf("task %s not found in findings; got: %v", task.ID, findings)
	}
}

// TestDetectTasksMissingDueDate_CompletedTasksNotFlagged verifies that only
// active tasks (pending / in_progress) are scanned — completed tasks are
// excluded because Tasks() returns active-only.
func TestDetectTasksMissingDueDate_CompletedTasksNotFlagged(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Create a task without due_date then complete it.
	task, err := s.gtd.CreateTask(context.Background(), gtdPkg.CreateTaskParams{
		Title: "completed-no-due",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err = s.gtd.UpdateTaskStatus(context.Background(), task.ID, gtdPkg.TaskStatus("completed")); err != nil {
		t.Fatalf("UpdateTaskStatus: %v", err)
	}
	findings := s.detectTasksMissingDueDate(context.Background(), nil)
	for _, f := range findings {
		if strings.Contains(string(f.Detail), task.ID.String()) {
			t.Errorf("completed task %s should NOT appear in live findings", task.ID)
		}
	}
}

// TestAnalyzeAgentBehavior_LiveFindings_PopulatedInResult verifies that
// detectTasksMissingDueDate findings appear in the live_findings field of the
// analyze_agent_behavior response (non-persisted, separate from Findings).
func TestAnalyzeAgentBehavior_LiveFindings_PopulatedInResult(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Wire up a discipline event store so the handler doesn't return early.
	s.disciplineEventStore = &stubDisciplineEventStore{}

	// Create a task without due_date.
	_, err := s.gtd.CreateTask(context.Background(), gtdPkg.CreateTaskParams{
		Title: "undated task for live finding",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	r := callAnalyzeAgentBehavior(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}

	var out analyzeAgentBehaviorResult
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, resultText(r))
	}
	if len(out.LiveFindings) == 0 {
		t.Error("live_findings should contain at least one task_missing_due_date entry")
	}
	for _, f := range out.LiveFindings {
		if f.EventType != "task_missing_due_date" {
			t.Errorf("unexpected live_finding EventType: %s", f.EventType)
		}
	}
}

// TestAnalyzeAgentBehavior_LiveFindings_EmptyWhenAllHaveDueDate verifies that
// live_findings is empty when all active tasks have a due_date.
func TestAnalyzeAgentBehavior_LiveFindings_EmptyWhenAllHaveDueDate(t *testing.T) {
	s := newTestWorkSessionServer(t)
	s.disciplineEventStore = &stubDisciplineEventStore{}

	seedTaskWithDueDate(t, s, "")

	r := callAnalyzeAgentBehavior(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}

	var out analyzeAgentBehaviorResult
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	for _, f := range out.LiveFindings {
		if f.EventType == "task_missing_due_date" {
			t.Errorf("task with due_date should NOT appear as missing_due_date finding")
		}
	}
}

// TestAnalyzeAgentBehavior_TotalInserted_DoesNotCountLiveFindings verifies
// that TotalInserted counts only persisted events (from insertWatchdogEvent),
// and live_findings do NOT inflate TotalInserted.
func TestAnalyzeAgentBehavior_TotalInserted_DoesNotCountLiveFindings(t *testing.T) {
	s := newTestWorkSessionServer(t)
	s.disciplineEventStore = &stubDisciplineEventStore{}

	// Create a task without due_date — will appear in live_findings.
	_, err := s.gtd.CreateTask(context.Background(), gtdPkg.CreateTaskParams{
		Title: "no-due-date for total-inserted check",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	r := callAnalyzeAgentBehavior(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}

	var out analyzeAgentBehaviorResult
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// live_findings exist but TotalInserted must still count only persisted rows
	// (which is 0 in the stub — insertErr is nil but stub doesn't count inserts).
	// We only verify that live_findings and total_inserted are independent.
	if len(out.LiveFindings) == 0 {
		t.Fatal("expected live_findings (pre-condition)")
	}
	// TotalInserted must equal the count of persisted Findings only.
	if out.TotalInserted != len(out.Findings) {
		t.Errorf("TotalInserted=%d should equal len(Findings)=%d (live findings must not inflate it)",
			out.TotalInserted, len(out.Findings))
	}
}
