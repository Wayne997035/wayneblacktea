package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

const statusInProgress = "in_progress"

// finalResultSuccess / finalResultFailure are local goconst-required constants
// for this file's final_result test literals. NOTE: tools_behaviorrule_test.go
// defines outcomeSuccess/outcomeFailure with the same values, but that file is
// gated `//go:build !integration` — referencing its constants here would break
// `go test -tags integration` (constants would be undefined). This file has no
// build tag (compiled under both default and integration builds), so it needs
// its own always-available constants.
const (
	finalResultSuccess = "success"
	finalResultFailure = "failure"
)

// newTestWorkSessionServer creates a full Server backed by an in-memory SQLite
// database for worksession tool tests.
func newTestWorkSessionServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newTestWorkSessionServerWithDB(t)
	return srv
}

// newTestWorkSessionServerWithDB is like newTestWorkSessionServer but also
// returns a second wbtsqlite.DB handle opened on the same file so tests can
// insert fixture rows (e.g. tasks to satisfy FK constraints in
// work_session_tasks.task_id). Both handles share the same WAL journal.
func newTestWorkSessionServerWithDB(t *testing.T) (*Server, *wbtsqlite.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ws-test.db")

	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores: %v", err)
	}
	t.Cleanup(func() { _ = stores.Close() })

	// Open a second connection to the same file for test fixture insertion.
	db, err := wbtsqlite.Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("Open sqlite fixture handle: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	srv, err := New(stores)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, db
}

// callWorkSessionTool is a thin helper that directly invokes a handler by name.
// It does NOT go through the full MCPServer dispatch to avoid the HandleToolCall
// unexported method issue. Instead it calls the handler method on the server.
func callStartWork(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleStartWork(context.Background(), req)
	if err != nil {
		t.Fatalf("handleStartWork error: %v", err)
	}
	return result
}

func callGetActiveWork(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleGetActiveWork(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGetActiveWork error: %v", err)
	}
	return result
}

func callCheckpointWork(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleCheckpointWork(context.Background(), req)
	if err != nil {
		t.Fatalf("handleCheckpointWork error: %v", err)
	}
	return result
}

func callFinishWork(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleFinishWork(context.Background(), req)
	if err != nil {
		t.Fatalf("handleFinishWork error: %v", err)
	}
	return result
}

func resultText(r *mcpmsg.CallToolResult) string {
	for _, c := range r.Content {
		if tc, ok := c.(mcpmsg.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// ---- M-NEW-1: server-side length guards ----

func TestHandleStartWork_RejectsOversizedTitle(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callStartWork(t, s, map[string]any{
		"repo_name": "test-repo",
		"title":     strings.Repeat("x", 201), // 201 chars > limit 200
		"goal":      "valid goal",
	})
	if !r.IsError {
		t.Fatal("expected error for oversized title")
	}
	if !strings.Contains(resultText(r), "exceeds 200 character limit") {
		t.Errorf("error message should mention limit, got: %s", resultText(r))
	}
}

func TestHandleStartWork_RejectsOversizedGoal(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callStartWork(t, s, map[string]any{
		"repo_name": "test-repo",
		"title":     "valid title",
		"goal":      strings.Repeat("y", 2001), // 2001 chars > limit 2000
	})
	if !r.IsError {
		t.Fatal("expected error for oversized goal")
	}
	if !strings.Contains(resultText(r), "exceeds 2000 character limit") {
		t.Errorf("error message should mention limit, got: %s", resultText(r))
	}
}

func TestHandleCheckpointWork_RejectsOversizedSummary(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Start a session first so we have a valid session_id.
	startR := callStartWork(t, s, map[string]any{
		"repo_name": "size-test-repo",
		"title":     "size test",
		"goal":      "test goal",
	})
	if startR.IsError {
		t.Fatalf("start_work setup failed: %s", resultText(startR))
	}
	var startResult map[string]any
	if err := json.Unmarshal([]byte(resultText(startR)), &startResult); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	sessID, _ := startResult["session_id"].(string)

	r := callCheckpointWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    strings.Repeat("z", 5001), // 5001 chars > limit 5000
	})
	if !r.IsError {
		t.Fatal("expected error for oversized summary")
	}
	if !strings.Contains(resultText(r), "exceeds 5000 character limit") {
		t.Errorf("error message should mention limit, got: %s", resultText(r))
	}
}

func TestHandleFinishWork_RejectsOversizedSummary(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// Start a session first.
	startR := callStartWork(t, s, map[string]any{
		"repo_name": "finish-size-repo",
		"title":     "size test",
		"goal":      "test goal",
	})
	if startR.IsError {
		t.Fatalf("start_work setup failed: %s", resultText(startR))
	}
	var startResult map[string]any
	if err := json.Unmarshal([]byte(resultText(startR)), &startResult); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	sessID, _ := startResult["session_id"].(string)

	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    strings.Repeat("w", 5001), // 5001 chars > limit 5000
	})
	if !r.IsError {
		t.Fatal("expected error for oversized summary")
	}
	if !strings.Contains(resultText(r), "exceeds 5000 character limit") {
		t.Errorf("error message should mention limit, got: %s", resultText(r))
	}
}

// ---- get_active_work: no active session ----

func TestHandleGetActiveWork_NoActiveSession(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetActiveWork(t, s, map[string]any{"repo_name": "test-repo"})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, resultText(r))
	}
	if result["active"] != false {
		t.Errorf("expected active=false, got %v", result["active"])
	}
	if result["implementation_allowed"] != false {
		t.Errorf("expected implementation_allowed=false, got %v", result["implementation_allowed"])
	}
}

func TestHandleGetActiveWork_MissingRepoName(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetActiveWork(t, s, map[string]any{})
	if !r.IsError {
		t.Error("expected error result for missing repo_name")
	}
	if !strings.Contains(resultText(r), "repo_name") {
		t.Errorf("error should mention repo_name, got: %s", resultText(r))
	}
}

// ---- start_work ----

func TestHandleStartWork_Success(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callStartWork(t, s, map[string]any{
		"repo_name": "my-repo",
		"title":     "Test session",
		"goal":      "Implement feature X",
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, resultText(r))
	}
	if result["session_id"] == nil || result["session_id"] == "" {
		t.Errorf("expected session_id in response, got: %v", result)
	}
	if result["status"] != statusInProgress {
		t.Errorf("expected status=in_progress, got %v", result["status"])
	}
}

func TestHandleStartWork_MissingRepoName(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callStartWork(t, s, map[string]any{
		"title": "test",
		"goal":  "test goal",
	})
	if !r.IsError {
		t.Error("expected error for missing repo_name")
	}
}

func TestHandleStartWork_AlreadyActive(t *testing.T) {
	s := newTestWorkSessionServer(t)
	// First start succeeds.
	callStartWork(t, s, map[string]any{
		"repo_name": "conflict-repo",
		"title":     "First",
		"goal":      "First goal",
	})
	// Second start for same repo should fail.
	r := callStartWork(t, s, map[string]any{
		"repo_name": "conflict-repo",
		"title":     "Second",
		"goal":      "Second goal",
	})
	if !r.IsError {
		t.Error("expected already-active error for duplicate session")
	}
	if !strings.Contains(resultText(r), statusInProgress) {
		t.Errorf("error should mention in_progress, got: %s", resultText(r))
	}
}

// insertMCPTestTask inserts a minimal task row into db so FK constraints in
// work_session_tasks.task_id are satisfied. The wsID arg may be "" when the
// store is not configured with a workspace (nil workspaceID).
func insertMCPTestTask(t *testing.T, db *wbtsqlite.DB, wsID, taskID string) {
	t.Helper()
	const q = `INSERT INTO tasks (id, workspace_id, title, status, priority)
		VALUES (?1,?2,'test task','pending',3)`
	if err := db.ExecContext(context.Background(), q, taskID, wsID); err != nil {
		t.Fatalf("insertMCPTestTask: %v", err)
	}
}

func TestHandleStartWork_WithTaskIDs(t *testing.T) {
	s, db := newTestWorkSessionServerWithDB(t)
	taskID := uuid.New().String()

	// Insert the task so the FK constraint on work_session_tasks.task_id is satisfied.
	insertMCPTestTask(t, db, "", taskID)

	taskIDsJSON := `["` + taskID + `"]`
	r := callStartWork(t, s, map[string]any{
		"repo_name": "task-linked-repo",
		"title":     "Session with tasks",
		"goal":      "Test task linking",
		"task_ids":  taskIDsJSON,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, resultText(r))
	}
	linkedTasks, ok := result["linked_tasks"].(float64)
	if !ok {
		t.Fatalf("linked_tasks missing or wrong type: %v", result["linked_tasks"])
	}
	if int(linkedTasks) != 1 {
		t.Errorf("expected linked_tasks=1, got %d", int(linkedTasks))
	}
}

// ---- checkpoint_work ----

func TestHandleCheckpointWork_Success(t *testing.T) {
	s := newTestWorkSessionServer(t)

	startR := callStartWork(t, s, map[string]any{
		"repo_name": "chkpt-repo",
		"title":     "Checkpoint test",
		"goal":      "Test checkpoint",
	})
	var startResult map[string]any
	if err := json.Unmarshal([]byte(resultText(startR)), &startResult); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	sessID, _ := startResult["session_id"].(string)

	r := callCheckpointWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "Phase 1 complete",
	})
	if r.IsError {
		t.Fatalf("checkpoint failed: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "checkpointed" {
		t.Errorf("expected status=checkpointed, got %v", result["status"])
	}
	if result["checkpoint_at"] == nil {
		t.Error("expected checkpoint_at in response")
	}
}

func TestHandleCheckpointWork_InvalidUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCheckpointWork(t, s, map[string]any{
		"session_id": "not-a-uuid",
		"summary":    "test",
	})
	if !r.IsError {
		t.Error("expected error for invalid UUID")
	}
}

func TestHandleCheckpointWork_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCheckpointWork(t, s, map[string]any{
		"session_id": "00000000-0000-0000-0000-000000000001",
		"summary":    "ghost",
	})
	if !r.IsError {
		t.Error("expected not-found error")
	}
}

func TestHandleCheckpointWork_MissingSummary(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCheckpointWork(t, s, map[string]any{
		"session_id": "00000000-0000-0000-0000-000000000001",
	})
	if !r.IsError {
		t.Error("expected error for missing summary")
	}
}

// ---- finish_work ----

func TestHandleFinishWork_Success(t *testing.T) {
	s := newTestWorkSessionServer(t)

	startR := callStartWork(t, s, map[string]any{
		"repo_name": "finish-repo",
		"title":     "Finish test",
		"goal":      "Test finish",
	})
	var startResult map[string]any
	if err := json.Unmarshal([]byte(resultText(startR)), &startResult); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	sessID, _ := startResult["session_id"].(string)

	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "All done!",
	})
	if r.IsError {
		t.Fatalf("finish failed: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != taskStatusCompleted {
		t.Errorf("expected status=completed, got %v", result["status"])
	}
}

func TestHandleFinishWork_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callFinishWork(t, s, map[string]any{
		"session_id": "00000000-0000-0000-0000-000000000001",
		"summary":    "ghost",
	})
	if !r.IsError {
		t.Error("expected not-found error")
	}
}

func TestHandleFinishWork_InvalidUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callFinishWork(t, s, map[string]any{
		"session_id": "not-a-uuid",
		"summary":    "test",
	})
	if !r.IsError {
		t.Error("expected error for invalid UUID")
	}
}

// ---- full lifecycle: start → get_active → checkpoint → finish → get_active ----

func TestWorkSessionLifecycle(t *testing.T) {
	s := newTestWorkSessionServer(t)

	// 1. No active initially.
	r0 := callGetActiveWork(t, s, map[string]any{"repo_name": "lifecycle-repo"})
	var init map[string]any
	if err := json.Unmarshal([]byte(resultText(r0)), &init); err != nil {
		t.Fatalf("unmarshal init: %v", err)
	}
	if init["active"] != false {
		t.Errorf("initial should be inactive, got %v", init["active"])
	}

	// 2. Start.
	startR := callStartWork(t, s, map[string]any{
		"repo_name": "lifecycle-repo",
		"title":     "Lifecycle",
		"goal":      "E2E test",
	})
	var startResult map[string]any
	if err := json.Unmarshal([]byte(resultText(startR)), &startResult); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	sessID, _ := startResult["session_id"].(string)

	// 3. get_active returns true.
	r2 := callGetActiveWork(t, s, map[string]any{"repo_name": "lifecycle-repo"})
	var active map[string]any
	if err := json.Unmarshal([]byte(resultText(r2)), &active); err != nil {
		t.Fatalf("unmarshal active: %v", err)
	}
	if active["active"] != true {
		t.Errorf("after start, active should be true, got %v", active["active"])
	}
	if active["implementation_allowed"] != true {
		t.Errorf("implementation_allowed should be true, got %v", active["implementation_allowed"])
	}

	// 4. Checkpoint.
	r3 := callCheckpointWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "Phase 1",
	})
	if r3.IsError {
		t.Fatalf("checkpoint failed: %s", resultText(r3))
	}

	// 5. Finish.
	r4 := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "Done",
	})
	if r4.IsError {
		t.Fatalf("finish failed: %s", resultText(r4))
	}

	// 6. After finish, get_active returns false.
	r5 := callGetActiveWork(t, s, map[string]any{"repo_name": "lifecycle-repo"})
	var final map[string]any
	if err := json.Unmarshal([]byte(resultText(r5)), &final); err != nil {
		t.Fatalf("unmarshal final: %v", err)
	}
	if final["active"] != false {
		t.Errorf("after finish, active should be false, got %v", final["active"])
	}
}

// ---- confirm_plan creates work session (regression test) ----

func TestHandleConfirmPlan_CreatesWorkSession(t *testing.T) {
	s := newTestWorkSessionServer(t)

	phases := `[{"title":"Phase 1","description":"First phase","priority":2}]`
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"phases":    phases,
		"repo_name": "confirm-repo",
	}
	result, err := s.handleConfirmPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConfirmPlan: %v", err)
	}
	if result.IsError {
		t.Fatalf("confirm_plan error: %s", resultText(result))
	}

	text := resultText(result)
	// Must mention "Plan confirmed" (old format).
	if !strings.Contains(text, "Plan confirmed") {
		t.Errorf("response should contain 'Plan confirmed', got: %s", text)
	}
	// Must mention the task title (regression: old format unchanged).
	if !strings.Contains(text, "Phase 1") {
		t.Errorf("response should contain task title, got: %s", text)
	}
	// Must mention session ID (new behavior).
	if !strings.Contains(text, "Work session started") {
		t.Errorf("response should contain 'Work session started', got: %s", text)
	}

	// After confirm_plan, get_active_work should return active=true.
	activeR := callGetActiveWork(t, s, map[string]any{"repo_name": "confirm-repo"})
	var activeResult map[string]any
	if err := json.Unmarshal([]byte(resultText(activeR)), &activeResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if activeResult["active"] != true {
		t.Errorf("after confirm_plan, active should be true, got: %v", activeResult["active"])
	}
}

func TestHandleConfirmPlan_OldFormatUnchanged(t *testing.T) {
	// Regression test: confirm_plan without repo_name should still work,
	// outputting "Plan confirmed.\nTasks created (2):..." format unchanged.
	// Uses HasPrefix + snapshot pattern to catch any spurious output additions
	// (e.g. "Work session started" must NOT appear when repo_name is absent).
	s := newTestWorkSessionServer(t)

	phases := `[{"title":"Do X","description":"desc","priority":2},{"title":"Do Y","priority":1}]`
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"phases": phases,
	}
	result, err := s.handleConfirmPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConfirmPlan: %v", err)
	}
	if result.IsError {
		t.Fatalf("confirm_plan error: %s", resultText(result))
	}
	text := resultText(result)

	// Snapshot: response MUST start with "Plan confirmed." (no prefix drift).
	if !strings.HasPrefix(text, "Plan confirmed.") {
		t.Errorf("response must start with 'Plan confirmed.', got: %q", text)
	}
	// Old fields must be present.
	if !strings.Contains(text, "Tasks created (2)") {
		t.Errorf("missing 'Tasks created (2)': %s", text)
	}
	if !strings.Contains(text, "Do X") {
		t.Errorf("missing task title 'Do X': %s", text)
	}
	if !strings.Contains(text, "Do Y") {
		t.Errorf("missing task title 'Do Y': %s", text)
	}
	// Without repo_name, no work session must be started.
	if strings.Contains(text, "Work session started") {
		t.Errorf("must NOT output 'Work session started' when repo_name is absent, got: %s", text)
	}
}

// TestStartWork_CrossWorkspaceIsolation verifies that workspace scoping is
// enforced: a session created by workspaceA is not visible to workspaceB.
func TestStartWork_CrossWorkspaceIsolation(t *testing.T) {
	wsA := uuid.New()
	wsB := uuid.New()

	// storeA and storeB share the same in-memory DB intentionally so we can
	// test workspace scoping within the same SQLite instance.
	db, err := wbtsqlite.Open(context.Background(), ":memory:", wsA.String())
	if err != nil {
		t.Fatalf("Open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	wsStore := wbtsqlite.NewWorkSessionStore(db)

	// Create a session with workspaceA.
	srvA := &Server{workSession: wsStore, workspaceID: &wsA}
	callStartWork(t, srvA, map[string]any{
		"repo_name": "shared-repo",
		"title":     "WS A session",
		"goal":      "A goal",
	})

	// Check active for workspaceB (different workspace_id) — must return inactive.
	// Build a store that uses workspace B's UUID for lookup.
	wsBStore := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()
	resultB, err := wsBStore.GetActive(ctx, wsB, "shared-repo")
	if err != nil {
		t.Fatalf("GetActive workspace B: %v", err)
	}
	if resultB.Active {
		t.Error("workspace B should not see workspace A's session")
	}

	// workspaceA must still see its own session via the tool.
	activeR := callGetActiveWork(t, srvA, map[string]any{"repo_name": "shared-repo"})
	var activeResult map[string]any
	if err := json.Unmarshal([]byte(resultText(activeR)), &activeResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if activeResult["active"] != true {
		t.Errorf("workspace A should see its own session, got: %v", activeResult["active"])
	}

	// Silence unused-variable lint.
	_ = worksession.ErrAlreadyActive
}

// TestHandleFinishWork_WithDecisions verifies that finish_work accepts a
// new_decisions JSON array and completes successfully (decisions are best-effort,
// so even noisy titles must not prevent completion).
func TestHandleFinishWork_WithDecisions(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{
		"repo_name": "decisions-test-repo",
		"title":     "test session",
		"goal":      "verify decision logging",
	})
	if startR.IsError {
		t.Fatalf("start_work failed: %s", resultText(startR))
	}
	var startResult map[string]any
	if err := json.Unmarshal([]byte(resultText(startR)), &startResult); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	sessID, _ := startResult["session_id"].(string)

	// Valid decisions should be logged; noisy title with newline should be skipped without error.
	decisionsJSON := `["Switch to pgx/v5 for connection pooling","Adopt testcontainers for integration tests"]`
	r := callFinishWork(t, s, map[string]any{
		"session_id":    sessID,
		"summary":       "wrapped up the session",
		"new_decisions": decisionsJSON,
	})
	if r.IsError {
		t.Fatalf("finish_work with new_decisions must succeed, got: %s", resultText(r))
	}

	// A noisy decision title (contains newline) must not prevent completion.
	startR2 := callStartWork(t, s, map[string]any{
		"repo_name": "decisions-test-repo2",
		"title":     "test session 2",
		"goal":      "verify noisy title handling",
	})
	if startR2.IsError {
		t.Fatalf("start_work 2 failed: %s", resultText(startR2))
	}
	var startResult2 map[string]any
	if err := json.Unmarshal([]byte(resultText(startR2)), &startResult2); err != nil {
		t.Fatalf("unmarshal start 2: %v", err)
	}
	sessID2, _ := startResult2["session_id"].(string)

	noisyJSON := `["injected\ndecision","clean decision title"]`
	r2 := callFinishWork(t, s, map[string]any{
		"session_id":    sessID2,
		"summary":       "done",
		"new_decisions": noisyJSON,
	})
	if r2.IsError {
		t.Fatalf("finish_work with noisy decisions must still succeed (best-effort), got: %s", resultText(r2))
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2.3 — start_work context pack, finish_work evidence chain,
// list_recent_work_sessions, get_work_session_trace
// ---------------------------------------------------------------------------

func callListRecentWorkSessions(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleListRecentWorkSessions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListRecentWorkSessions error: %v", err)
	}
	return result
}

func callGetWorkSessionTrace(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleGetWorkSessionTrace(context.Background(), req)
	if err != nil {
		t.Fatalf("handleGetWorkSessionTrace error: %v", err)
	}
	return result
}

// ---- start_work: context pack ----

func TestHandleStartWork_ReturnsContextPack(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callStartWork(t, s, map[string]any{
		"repo_name": "context-pack-repo",
		"title":     "Context pack test",
		"goal":      "verify assemble_context is called on start_work",
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, resultText(r))
	}
	pack, ok := result["context_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected context_pack object in response, got: %v", result["context_pack"])
	}
	if pack["objective"] != "verify assemble_context is called on start_work" {
		t.Errorf("context_pack.objective mismatch: got %v", pack["objective"])
	}
}

// TestHandleStartWork_NilContextAssembler_NonFatal verifies start_work still
// succeeds (with a null context_pack) when contextAssembler is not wired —
// mirrors TestStartWork_CrossWorkspaceIsolation's bare Server construction.
func TestHandleStartWork_NilContextAssembler_NonFatal(t *testing.T) {
	wsID := uuid.New()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", wsID.String())
	if err != nil {
		t.Fatalf("Open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := &Server{workSession: wbtsqlite.NewWorkSessionStore(db), workspaceID: &wsID}

	r := callStartWork(t, srv, map[string]any{
		"repo_name": "no-assembler-repo",
		"title":     "No assembler test",
		"goal":      "verify non-fatal without contextAssembler",
	})
	if r.IsError {
		t.Fatalf("expected success even without contextAssembler, got error: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, resultText(r))
	}
	if result["context_pack"] != nil {
		t.Errorf("expected null context_pack when contextAssembler is nil, got: %v", result["context_pack"])
	}
}

// ---- start_work: branch_name ----

func TestHandleStartWork_RejectsOversizedBranchName(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callStartWork(t, s, map[string]any{
		"repo_name":   "branch-size-repo",
		"title":       "t",
		"goal":        "g",
		"branch_name": strings.Repeat("b", 201),
	})
	if !r.IsError {
		t.Fatal("expected error for oversized branch_name")
	}
	if !strings.Contains(resultText(r), "branch_name exceeds") {
		t.Errorf("error should mention branch_name limit, got: %s", resultText(r))
	}
}

func TestHandleStartWork_PersistsBranchName(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callStartWork(t, s, map[string]any{
		"repo_name":   "branch-persist-repo",
		"title":       "t",
		"goal":        "g",
		"branch_name": "feature/action-lifecycle",
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	sessID, _ := uuid.Parse(result["session_id"].(string))

	got, err := s.workSession.GetByID(context.Background(), s.workspaceUUIDVal(), sessID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.BranchName == nil || *got.BranchName != "feature/action-lifecycle" {
		t.Errorf("BranchName: got %v, want feature/action-lifecycle", got.BranchName)
	}
}

// ---- finish_work: evidence-chain enum validation ----

func TestHandleFinishWork_InvalidVerificationStatus(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "invalid-verif-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id":          sessID,
		"summary":             "done",
		"verification_status": "bogus",
	})
	if !r.IsError {
		t.Fatal("expected tool error for invalid verification_status, not a panic or success")
	}
	if !strings.Contains(resultText(r), "invalid verification_status") {
		t.Errorf("error should mention invalid verification_status, got: %s", resultText(r))
	}
}

func TestHandleFinishWork_InvalidFinalResult(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "invalid-result-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id":   sessID,
		"summary":      "done",
		"final_result": "bogus",
	})
	if !r.IsError {
		t.Fatal("expected tool error for invalid final_result, not a panic or success")
	}
	if !strings.Contains(resultText(r), "invalid final_result") {
		t.Errorf("error should mention invalid final_result, got: %s", resultText(r))
	}
}

func TestHandleFinishWork_RejectsOversizedVerificationCommand(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "oversized-cmd-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id":           sessID,
		"summary":              "done",
		"verification_command": strings.Repeat("c", 501),
	})
	if !r.IsError {
		t.Fatal("expected error for oversized verification_command")
	}
}

func TestHandleFinishWork_RejectsOversizedVerificationOutputExcerpt(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "oversized-excerpt-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id":                  sessID,
		"summary":                     "done",
		"verification_output_excerpt": strings.Repeat("e", 2001),
	})
	if !r.IsError {
		t.Fatal("expected error for oversized verification_output_excerpt")
	}
}

func TestHandleFinishWork_WritesVerificationFields(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "writes-verif-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id":                  sessID,
		"summary":                     "verified fine",
		"verification_status":         "passed",
		"verification_command":        "task check",
		"verification_output_excerpt": "0 issues",
		"final_result":                finalResultSuccess,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// final_result=success is not in negativeFinalResults, so no outcome should
	// be auto-created.
	if result["outcome_id"] != nil {
		t.Errorf("expected null outcome_id for final_result=success, got: %v", result["outcome_id"])
	}

	sessIDParsed, _ := uuid.Parse(sessID)
	got, err := s.workSession.GetByID(context.Background(), s.workspaceUUIDVal(), sessIDParsed)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.VerificationStatus == nil || *got.VerificationStatus != "passed" {
		t.Errorf("VerificationStatus not persisted: got %v", got.VerificationStatus)
	}
}

// ---- finish_work: auto-outcome-on-failure ----

func TestHandleFinishWork_AutoCreatesOutcomeOnFailure(t *testing.T) {
	s, db := newTestWorkSessionServerWithDB(t)
	taskID := uuid.New().String()
	insertMCPTestTask(t, db, "", taskID)

	startR := callStartWork(t, s, map[string]any{
		"repo_name": "auto-outcome-repo",
		"title":     "t",
		"goal":      "g",
		"task_ids":  `["` + taskID + `"]`,
	})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id":   sessID,
		"summary":      "hit a regression",
		"final_result": finalResultFailure,
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["outcome_id"] == nil || result["outcome_id"] == "" {
		t.Fatalf("expected non-null outcome_id for final_result=failure with a task ID, got: %v", result["outcome_id"])
	}

	// SetOutcomeLink must have persisted outcome_id back onto the session.
	sessIDParsed, _ := uuid.Parse(sessID)
	got, err := s.workSession.GetByID(context.Background(), s.workspaceUUIDVal(), sessIDParsed)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.OutcomeID == nil {
		t.Error("expected SetOutcomeLink to have persisted outcome_id onto the session")
	}
}

// TestHandleFinishWork_AutoOutcome_NoTaskID_NonFatal verifies that when
// final_result is negative but no task ID is available (no task_ids linked,
// no current_task_id), finish_work still succeeds — the auto-outcome step is
// skipped entirely, not treated as an error.
func TestHandleFinishWork_AutoOutcome_NoTaskID_NonFatal(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "no-task-outcome-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id":   sessID,
		"summary":      "regressed with no task context",
		"final_result": "regressed",
	})
	if r.IsError {
		t.Fatalf("finish_work must succeed even when no task ID is available for auto-outcome, got: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["outcome_id"] != nil {
		t.Errorf("expected null outcome_id when no task ID is available, got: %v", result["outcome_id"])
	}
}

// TestHandleFinishWork_AutoOutcome_NilOutcomeStore_NonFatal verifies that
// finish_work succeeds (and simply skips auto-outcome creation) when the
// server has no outcome store wired (s.outcome == nil).
func TestHandleFinishWork_AutoOutcome_NilOutcomeStore_NonFatal(t *testing.T) {
	wsID := uuid.New()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", wsID.String())
	if err != nil {
		t.Fatalf("Open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	srv := &Server{workSession: wbtsqlite.NewWorkSessionStore(db), workspaceID: &wsID}

	startR := callStartWork(t, srv, map[string]any{"repo_name": "nil-outcome-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, srv, map[string]any{
		"session_id":   sessID,
		"summary":      "failed with no outcome store configured",
		"final_result": finalResultFailure,
	})
	if r.IsError {
		t.Fatalf("finish_work must succeed when outcome store is nil, got: %s", resultText(r))
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["outcome_id"] != nil {
		t.Errorf("expected null outcome_id when outcome store is nil, got: %v", result["outcome_id"])
	}
}

// ---- list_recent_work_sessions ----

func TestHandleListRecentWorkSessions_ExcludesOutputExcerpt(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "list-excerpt-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	finishR := callFinishWork(t, s, map[string]any{
		"session_id":                  sessID,
		"summary":                     "done",
		"verification_output_excerpt": "super-secret-command-output-marker",
	})
	if finishR.IsError {
		t.Fatalf("finish_work failed: %s", resultText(finishR))
	}

	r := callListRecentWorkSessions(t, s, map[string]any{"repo_name": "list-excerpt-repo"})
	if r.IsError {
		t.Fatalf("list_recent_work_sessions failed: %s", resultText(r))
	}
	text := resultText(r)
	if strings.Contains(text, "super-secret-command-output-marker") {
		t.Errorf("list_recent_work_sessions must NOT include verification_output_excerpt, got: %s", text)
	}
	if strings.Contains(text, "verification_output_excerpt") {
		t.Errorf("list_recent_work_sessions response must not even have the excerpt key, got: %s", text)
	}
}

func TestHandleListRecentWorkSessions_ReturnsSummaries(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "list-summaries-repo", "title": "list test", "goal": "g"})
	sessID := startSessionID(t, startR)
	if r := callFinishWork(t, s, map[string]any{"session_id": sessID, "summary": "done", "final_result": finalResultSuccess}); r.IsError {
		t.Fatalf("finish_work failed: %s", resultText(r))
	}

	r := callListRecentWorkSessions(t, s, map[string]any{"repo_name": "list-summaries-repo"})
	if r.IsError {
		t.Fatalf("list_recent_work_sessions failed: %s", resultText(r))
	}
	var summaries []map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &summaries); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, resultText(r))
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0]["title"] != "list test" {
		t.Errorf("title mismatch: got %v", summaries[0]["title"])
	}
	if summaries[0]["final_result"] != finalResultSuccess {
		t.Errorf("final_result mismatch: got %v", summaries[0]["final_result"])
	}
}

// ---- get_work_session_trace ----

func TestHandleGetWorkSessionTrace_ReturnsEvidence(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "trace-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)
	sessIDParsed, _ := uuid.Parse(sessID)

	cmd := "task check"
	if _, err := s.workSession.AddEvidence(context.Background(), worksession.Evidence{
		SessionID:    sessIDParsed,
		EvidenceType: "command",
		Status:       "passed",
		Command:      &cmd,
	}); err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}

	r := callGetWorkSessionTrace(t, s, map[string]any{"session_id": sessID})
	if r.IsError {
		t.Fatalf("get_work_session_trace failed: %s", resultText(r))
	}
	var trace struct {
		Session  map[string]any   `json:"session"`
		Evidence []map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &trace); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, resultText(r))
	}
	if len(trace.Evidence) != 1 {
		t.Fatalf("expected 1 evidence row, got %d", len(trace.Evidence))
	}
	if trace.Evidence[0]["command"] != cmd {
		t.Errorf("evidence command mismatch: got %v", trace.Evidence[0]["command"])
	}
}

func TestHandleGetWorkSessionTrace_EmptyEvidenceReturnsEmptyArray(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "trace-empty-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callGetWorkSessionTrace(t, s, map[string]any{"session_id": sessID})
	if r.IsError {
		t.Fatalf("get_work_session_trace failed: %s", resultText(r))
	}
	text := resultText(r)
	if strings.Contains(text, `"evidence": null`) {
		t.Errorf("evidence must be an empty array, not null: %s", text)
	}
	var trace struct {
		Evidence []map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(text), &trace); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if trace.Evidence == nil {
		t.Error("expected non-nil empty evidence array")
	}
	if len(trace.Evidence) != 0 {
		t.Errorf("expected 0 evidence rows, got %d", len(trace.Evidence))
	}
}

func TestHandleGetWorkSessionTrace_InvalidUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetWorkSessionTrace(t, s, map[string]any{"session_id": "not-a-uuid"})
	if !r.IsError {
		t.Error("expected tool error for malformed session_id, not a panic or success")
	}
}

func TestHandleGetWorkSessionTrace_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetWorkSessionTrace(t, s, map[string]any{"session_id": uuid.New().String()})
	if !r.IsError {
		t.Error("expected not-found tool error for unknown session_id")
	}
}

func TestHandleGetWorkSessionTrace_MissingSessionID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetWorkSessionTrace(t, s, map[string]any{})
	if !r.IsError {
		t.Error("expected error for missing session_id")
	}
}

// startSessionID unmarshals a start_work result and extracts session_id,
// failing the test if the start_work call itself errored.
func startSessionID(t *testing.T, startR *mcpmsg.CallToolResult) string {
	t.Helper()
	if startR.IsError {
		t.Fatalf("start_work setup failed: %s", resultText(startR))
	}
	var startResult map[string]any
	if err := json.Unmarshal([]byte(resultText(startR)), &startResult); err != nil {
		t.Fatalf("unmarshal start: %v", err)
	}
	sessID, _ := startResult["session_id"].(string)
	if sessID == "" {
		t.Fatal("start_work response missing session_id")
	}
	return sessID
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2 review F1 — finish_work evidence array wiring
// ---------------------------------------------------------------------------

// TestHandleFinishWork_WithEvidence_PersistsAndReadableViaTrace verifies the
// full round trip: finish_work's evidence JSON array is parsed, validated,
// and stored, then readable back via get_work_session_trace.
func TestHandleFinishWork_WithEvidence_PersistsAndReadableViaTrace(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "evidence-wire-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	evidenceJSON := `[` +
		`{"evidence_type":"command","status":"passed","command":"cd build && task check","output_excerpt":"0 issues"},` +
		`{"evidence_type":"pr","status":"passed","artifact":"https://github.com/example/repo/pull/1"}` +
		`]`
	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "done with evidence",
		"evidence":   evidenceJSON,
	})
	if r.IsError {
		t.Fatalf("finish_work with evidence must succeed, got: %s", resultText(r))
	}

	trace := callGetWorkSessionTrace(t, s, map[string]any{"session_id": sessID})
	if trace.IsError {
		t.Fatalf("get_work_session_trace failed: %s", resultText(trace))
	}
	var parsed struct {
		Evidence []map[string]any `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(resultText(trace)), &parsed); err != nil {
		t.Fatalf("unmarshal trace: %v (raw: %s)", err, resultText(trace))
	}
	if len(parsed.Evidence) != 2 {
		t.Fatalf("expected 2 evidence rows via trace, got %d", len(parsed.Evidence))
	}
	// GetEvidence orders by created_at ASC, i.e. insertion order.
	if parsed.Evidence[0]["evidence_type"] != "command" || parsed.Evidence[0]["command"] != "cd build && task check" {
		t.Errorf("evidence[0] mismatch: %v", parsed.Evidence[0])
	}
	if parsed.Evidence[1]["evidence_type"] != "pr" || parsed.Evidence[1]["artifact"] != "https://github.com/example/repo/pull/1" {
		t.Errorf("evidence[1] mismatch: %v", parsed.Evidence[1])
	}
}

func TestHandleFinishWork_RejectsOversizedEvidenceArray(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "evidence-oversized-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	items := make([]string, 0, 21)
	for range 21 {
		items = append(items, `{"evidence_type":"manual_note","status":"unknown"}`)
	}
	evidenceJSON := "[" + strings.Join(items, ",") + "]"

	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "too much evidence",
		"evidence":   evidenceJSON,
	})
	if !r.IsError {
		t.Fatal("expected error for oversized evidence array")
	}
	if !strings.Contains(resultText(r), "evidence exceeds limit") {
		t.Errorf("error should mention limit, got: %s", resultText(r))
	}
}

func TestHandleFinishWork_RejectsInvalidEvidenceType(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "evidence-badtype-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "bad type",
		"evidence":   `[{"evidence_type":"bogus","status":"passed"}]`,
	})
	if !r.IsError {
		t.Fatal("expected error for invalid evidence_type")
	}
	if !strings.Contains(resultText(r), "invalid evidence_type") {
		t.Errorf("error should mention invalid evidence_type, got: %s", resultText(r))
	}
}

func TestHandleFinishWork_RejectsInvalidEvidenceStatus(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "evidence-badstatus-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "bad status",
		"evidence":   `[{"evidence_type":"command","status":"bogus"}]`,
	})
	if !r.IsError {
		t.Fatal("expected error for invalid status")
	}
	if !strings.Contains(resultText(r), "invalid status") {
		t.Errorf("error should mention invalid status, got: %s", resultText(r))
	}
}

// TestHandleFinishWork_RejectsControlCharsInEvidenceCommand verifies
// adversarial-input handling (backend-security-design.md §2.1): a
// prompt-injected agent must not be able to smuggle a second shell
// instruction into evidence.command via an embedded newline.
func TestHandleFinishWork_RejectsControlCharsInEvidenceCommand(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "evidence-controlchars-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	evidenceJSON := `[{"evidence_type":"command","status":"passed","command":"task check\nrm -rf /"}]`
	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "injected newline",
		"evidence":   evidenceJSON,
	})
	if !r.IsError {
		t.Fatal("expected error for control chars in evidence command")
	}
}

func TestHandleFinishWork_RejectsOversizedEvidenceCommand(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "evidence-oversizedcmd-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	longCmd := strings.Repeat("c", 501)
	evidenceJSON, err := json.Marshal([]map[string]any{
		{"evidence_type": "command", "status": "passed", "command": longCmd},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "oversized command",
		"evidence":   string(evidenceJSON),
	})
	if !r.IsError {
		t.Fatal("expected error for oversized evidence command")
	}
	if !strings.Contains(resultText(r), "command exceeds") {
		t.Errorf("error should mention command length limit, got: %s", resultText(r))
	}
}

func TestHandleFinishWork_RejectsMalformedEvidenceJSON(t *testing.T) {
	s := newTestWorkSessionServer(t)
	startR := callStartWork(t, s, map[string]any{"repo_name": "evidence-malformed-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id": sessID,
		"summary":    "not json",
		"evidence":   "not-a-json-array",
	})
	if !r.IsError {
		t.Fatal("expected error for malformed evidence JSON")
	}
	if !strings.Contains(resultText(r), "invalid evidence JSON") {
		t.Errorf("error should mention invalid evidence JSON, got: %s", resultText(r))
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2 review F2 — deferred_task_ids end-to-end smoke test (MCP layer)
// ---------------------------------------------------------------------------

// queryMCPTaskStatus reads the status column for a task directly from the
// SQLite DB backing the test server. Mirrors queryTaskStatus in
// internal/worksession/store_test.go (unexported there, so duplicated here).
func queryMCPTaskStatus(t *testing.T, db *wbtsqlite.DB, taskID string) string {
	t.Helper()
	row := db.QueryRowContext(context.Background(), `SELECT status FROM tasks WHERE id = ?1`, taskID)
	var status string
	if err := row.Scan(&status); err != nil {
		t.Fatalf("queryMCPTaskStatus %s: %v", taskID, err)
	}
	return status
}

// TestHandleFinishWork_DeferredTaskNotMarkedCompleted is the end-to-end (MCP
// request → handler → store) regression test for F2: a task listed in
// deferred_task_ids must never come out of finish_work as completed, even
// when a sibling task is explicitly completed in the same call.
func TestHandleFinishWork_DeferredTaskNotMarkedCompleted(t *testing.T) {
	s, db := newTestWorkSessionServerWithDB(t)
	taskA := uuid.New().String()
	taskB := uuid.New().String()
	insertMCPTestTask(t, db, "", taskA)
	insertMCPTestTask(t, db, "", taskB)

	startR := callStartWork(t, s, map[string]any{
		"repo_name": "deferred-e2e-repo",
		"title":     "t",
		"goal":      "g",
		"task_ids":  `["` + taskA + `","` + taskB + `"]`,
	})
	sessID := startSessionID(t, startR)

	r := callFinishWork(t, s, map[string]any{
		"session_id":         sessID,
		"summary":            "taskA done, taskB deferred via MCP handler",
		"completed_task_ids": `["` + taskA + `"]`,
		"deferred_task_ids":  `["` + taskB + `"]`,
	})
	if r.IsError {
		t.Fatalf("finish_work failed: %s", resultText(r))
	}

	if got := queryMCPTaskStatus(t, db, taskA); got != taskStatusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryMCPTaskStatus(t, db, taskB); got == taskStatusCompleted {
		t.Errorf("taskB (deferred) must NOT be completed, got %q", got)
	}
}
