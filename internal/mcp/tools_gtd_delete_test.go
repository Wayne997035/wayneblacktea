package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callDeleteTask invokes delete_task (seam + handleDeleteTask) with the
// supplied arguments. Returns the tool result; never returns a Go error
// (tool errors surface via CallToolResult.IsError = true).
func callDeleteTask(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "delete_task", args, s.handleDeleteTask)
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

// TestDeleteTask_TokenMapBounded verifies that when maxPendingDeletions valid
// tokens are already stored, a further step-1 call is rejected with the
// "too many pending deletions" error rather than growing the map without bound.
func TestDeleteTask_TokenMapBounded(t *testing.T) {
	s := newTestWorkSessionServer(t)

	// Freeze time so all tokens we insert stay valid (non-expired) throughout.
	base := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	s.nowFn = func() time.Time { return base }

	// Prime the map with maxPendingDeletions valid entries using distinct
	// synthetic task-id keys so we don't need real DB rows (the sync.Map key
	// is just a string; no DB lookup is done at step-1 time).
	for i := 0; i < maxPendingDeletions; i++ {
		key := uuid.NewString()
		s.deleteTokens.Store(key, deletionToken{
			token:     uuid.NewString(),
			expiresAt: base.Add(deleteTokenTTL),
		})
	}

	// 257th request — step 1 for a real task (task must exist for the UUID
	// parse to succeed, but that's all step-1 checks).
	id := seedTask(t, s)
	r := callDeleteTask(t, s, map[string]any{"task_id": id.String()})
	if !r.IsError {
		t.Fatalf("expected cap error, got success: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "too many pending deletions") {
		t.Errorf("expected 'too many pending deletions', got: %s", resultText(r))
	}
}

// TestDeleteTask_PruneExpiredOnWrite verifies that expired tokens are removed
// from the map during a step-1 call so that the map does not grow indefinitely
// through accumulated dead entries.
func TestDeleteTask_PruneExpiredOnWrite(t *testing.T) {
	s := newTestWorkSessionServer(t)

	// Start at a base time and issue 256 step-1 tokens via the real handler so
	// they are stored at baseTime + TTL expiry.
	base := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	current := base
	s.nowFn = func() time.Time { return current }

	// Seed 256 entries with distinct synthetic keys directly (faster than 256
	// DB round-trips and the handler's prune logic is the unit under test, not
	// the DB).
	for i := 0; i < 256; i++ {
		key := uuid.NewString()
		s.deleteTokens.Store(key, deletionToken{
			token:     uuid.NewString(),
			expiresAt: base.Add(deleteTokenTTL),
		})
	}

	// Advance clock past the TTL — all 256 entries are now expired.
	current = base.Add(deleteTokenTTL + 5*time.Second)

	// Issue one step-1 call. The prune loop should delete all 256 expired
	// entries before storing the new one, leaving exactly 1 entry.
	id := seedTask(t, s)
	r := callDeleteTask(t, s, map[string]any{"task_id": id.String()})
	if r.IsError {
		t.Fatalf("step-1 after prune must succeed, got: %s", resultText(r))
	}

	var remaining int
	s.deleteTokens.Range(func(_, _ any) bool {
		remaining++
		return true
	})
	if remaining != 1 {
		t.Errorf("after prune expected 1 entry in deleteTokens, got %d", remaining)
	}
}

// --- U9: session-binding partial mitigation (Category S, 2026-08-20-mcp-surface-spec.md) ---
//
// fakeClientSession is a minimal server.ClientSession for tests: only
// SessionID matters here, the other 3 methods are unused by
// currentSessionID/issueDeletionToken/deletionTokenMatchesSession.
type fakeClientSession struct{ id string }

func (f fakeClientSession) SessionID() string                                   { return f.id }
func (f fakeClientSession) NotificationChannel() chan<- mcpmsg.JSONRPCNotification { return nil }
func (f fakeClientSession) Initialize()                                         {}
func (f fakeClientSession) Initialized() bool                                   { return true }

// callDeleteTaskCtx is callDeleteTask with a caller-supplied context, so
// tests can simulate a specific (or absent) MCP client session via
// s.MCPServer().WithContext.
func callDeleteTaskCtx(t *testing.T, ctx context.Context, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := seam("delete_task", s.handleDeleteTask)(ctx, req)
	if err != nil {
		t.Fatalf("delete_task error: %v", err)
	}
	return res
}

// TestDeleteTask_CrossSessionConfirmRejected is U9's bad case: identity A
// (session A) issues the deletion token; identity B (session B) attempts
// step 2 with that exact token string. Rejected — task not deleted. This is
// the session-transport-level partial mitigation, not the full
// authenticated-actor-identity fix (blocked on F16/U15, not yet landed) —
// see issueDeletionToken's doc comment for the distinction.
func TestDeleteTask_CrossSessionConfirmRejected(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	ctxA := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "session-A"})
	ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "session-B"})

	r1 := callDeleteTaskCtx(t, ctxA, s, map[string]any{"task_id": id.String()})
	token := extractToken(t, r1)

	r2 := callDeleteTaskCtx(t, ctxB, s, map[string]any{
		"task_id": id.String(), "confirm": true, "deletion_token": token,
	})
	if !r2.IsError {
		t.Fatalf("cross-session confirm must be rejected, got: %s", resultText(r2))
	}
	if !strings.Contains(resultText(r2), "different session") {
		t.Errorf("error should mention session mismatch, got: %s", resultText(r2))
	}

	if _, err := s.gtd.GetTaskByID(context.Background(), id); err != nil {
		t.Errorf("task should still exist after rejected cross-session confirm, GetTaskByID: %v", err)
	}
}

// TestDeleteTask_SameSessionConfirmSucceeds is U9's positive control: the
// same identity (session) completes both steps — unchanged behaviour, task
// deleted.
func TestDeleteTask_SameSessionConfirmSucceeds(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	ctx := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "session-same"})

	r1 := callDeleteTaskCtx(t, ctx, s, map[string]any{"task_id": id.String()})
	token := extractToken(t, r1)

	r2 := callDeleteTaskCtx(t, ctx, s, map[string]any{
		"task_id": id.String(), "confirm": true, "deletion_token": token,
	})
	if r2.IsError {
		t.Fatalf("same-session confirm must succeed, got: %s", resultText(r2))
	}
	if _, err := s.gtd.GetTaskByID(context.Background(), id); !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("task should be deleted after same-session confirm, got err: %v", err)
	}
}

// TestDeleteTask_NoTrackedSessionUnchangedBehaviour pins the fallback: when
// ctx carries no session at all (e.g. context.Background(), exactly what
// every OTHER test in this file uses), the original task-id-only protection
// applies unchanged — same-string confirm succeeds regardless of "session".
// This is what TestDeleteTask_SecondCallWithValidTokenSucceeds already
// covers end-to-end; this test exists to name the fallback explicitly so a
// future change can't silently start requiring a session everywhere.
func TestDeleteTask_NoTrackedSessionUnchangedBehaviour(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	r1 := callDeleteTask(t, s, map[string]any{"task_id": id.String()})
	token := extractToken(t, r1)

	r2 := callDeleteTask(t, s, map[string]any{
		"task_id": id.String(), "confirm": true, "deletion_token": token,
	})
	if r2.IsError {
		t.Fatalf("no-session confirm must still succeed (unchanged fallback), got: %s", resultText(r2))
	}
}
