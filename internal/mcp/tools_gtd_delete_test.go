package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callDeleteTask invokes handleDeleteTask with the supplied arguments. Returns
// the tool result; never returns a Go error (tool errors surface via
// CallToolResult.IsError = true).
func callDeleteTask(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleDeleteTask(context.Background(), req)
	if err != nil {
		t.Fatalf("handleDeleteTask error: %v", err)
	}
	return res
}

// seedTask creates a single GTD task and returns its UUID for delete tests.
func seedTask(t *testing.T, s *Server) uuid.UUID {
	t.Helper()
	task, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{
		Title: "throwaway task " + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task.ID
}

// extractToken parses the first-call JSON envelope and returns the deletion
// token string. Fails the test if the envelope shape is unexpected.
func extractToken(t *testing.T, r *mcpmsg.CallToolResult) string {
	t.Helper()
	if r.IsError {
		t.Fatalf("expected first-call success, got error: %s", resultText(r))
	}
	var payload struct {
		Status        string `json:"status"`
		DeletionToken string `json:"deletion_token"`
		ExpiresAt     string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &payload); err != nil {
		t.Fatalf("unmarshal first-call envelope: %v\nraw=%s", err, resultText(r))
	}
	if payload.Status != "confirmation_required" {
		t.Errorf("status = %q, want confirmation_required", payload.Status)
	}
	if payload.DeletionToken == "" {
		t.Error("deletion_token must be non-empty")
	}
	if payload.ExpiresAt == "" {
		t.Error("expires_at must be present")
	}
	return payload.DeletionToken
}

func TestDeleteTask_FirstCallIssuesToken(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	r := callDeleteTask(t, s, map[string]any{"task_id": id.String()})
	token := extractToken(t, r)
	if token == "" {
		t.Fatal("token must not be empty")
	}

	// The task must STILL exist after the first (token-only) call.
	tasks, err := s.gtd.Tasks(context.Background(), nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	var found bool
	for _, tk := range tasks {
		if tk.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Error("task should still exist after first delete_task call (no deletion yet)")
	}
}

func TestDeleteTask_SecondCallWithValidTokenSucceeds(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	first := callDeleteTask(t, s, map[string]any{"task_id": id.String()})
	token := extractToken(t, first)

	second := callDeleteTask(t, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": token,
	})
	if second.IsError {
		t.Fatalf("second call must succeed, got: %s", resultText(second))
	}
	if !strings.Contains(resultText(second), "task deleted") {
		t.Errorf("expected 'task deleted', got: %s", resultText(second))
	}

	// Task must be gone.
	tasks, err := s.gtd.Tasks(context.Background(), nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.ID == id {
			t.Fatal("task should have been deleted")
		}
	}
}

func TestDeleteTask_MissingConfirmReturnsTokenInstead(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	// confirm=false explicitly is equivalent to omitting it — both should
	// return a token rather than delete.
	r := callDeleteTask(t, s, map[string]any{
		"task_id": id.String(),
		"confirm": false,
	})
	if r.IsError {
		t.Fatalf("confirm=false should still issue token, got error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "deletion_token") {
		t.Errorf("expected deletion_token in payload, got: %s", resultText(r))
	}
}

func TestDeleteTask_ConfirmWithoutTokenFails(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	// Skip step 1 entirely — caller jumps straight to confirm=true with no
	// token. Must be rejected (otherwise the gate is useless).
	r := callDeleteTask(t, s, map[string]any{
		"task_id": id.String(),
		"confirm": true,
	})
	if !r.IsError {
		t.Fatalf("confirm=true without token must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "deletion_token") {
		t.Errorf("error should mention deletion_token, got: %s", resultText(r))
	}
}

func TestDeleteTask_ConfirmWithoutFirstCallFails(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	// confirm=true + a fabricated token, but no prior step-1 call. The
	// in-memory map has no entry → must reject.
	r := callDeleteTask(t, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": "fabricated-token-not-from-us",
	})
	if !r.IsError {
		t.Fatalf("confirm without prior step-1 must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "no pending deletion") {
		t.Errorf("error should mention 'no pending deletion', got: %s", resultText(r))
	}
}

func TestDeleteTask_WrongTokenFails(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	first := callDeleteTask(t, s, map[string]any{"task_id": id.String()})
	_ = extractToken(t, first) // we ignore the real token and use a wrong one

	r := callDeleteTask(t, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": "completely-different-token",
	})
	if !r.IsError {
		t.Fatalf("wrong token must error, got: %s", resultText(r))
	}
	// After a failed second call the stored token is consumed (single-use);
	// a retry with the REAL token MUST now fail too.
	tasks, err := s.gtd.Tasks(context.Background(), nil)
	if err != nil {
		t.Fatalf("Tasks: %v", err)
	}
	var stillThere bool
	for _, tk := range tasks {
		if tk.ID == id {
			stillThere = true
			break
		}
	}
	if !stillThere {
		t.Error("wrong-token second call should not delete the task")
	}
}

func TestDeleteTask_ExpiredTokenFails(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Freeze nowFn to a known instant; jump it forward past the TTL between
	// step 1 and step 2 to simulate expiry deterministically.
	base := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	current := base
	s.nowFn = func() time.Time { return current }
	id := seedTask(t, s)

	first := callDeleteTask(t, s, map[string]any{"task_id": id.String()})
	token := extractToken(t, first)

	// Advance past the TTL window.
	current = base.Add(deleteTokenTTL + 5*time.Second)

	r := callDeleteTask(t, s, map[string]any{
		"task_id":        id.String(),
		"confirm":        true,
		"deletion_token": token,
	})
	if !r.IsError {
		t.Fatalf("expired token must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "expired") {
		t.Errorf("error should mention 'expired', got: %s", resultText(r))
	}
}

func TestDeleteTask_InvalidUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callDeleteTask(t, s, map[string]any{"task_id": "not-a-uuid"})
	if !r.IsError {
		t.Fatalf("invalid UUID must error, got: %s", resultText(r))
	}
}

func TestDeleteTask_MissingTaskID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callDeleteTask(t, s, map[string]any{})
	if !r.IsError {
		t.Fatalf("missing task_id must error, got: %s", resultText(r))
	}
}
