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
	if result["status"] != "completed" {
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
