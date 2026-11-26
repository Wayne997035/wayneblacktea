package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// seedTaskWithAssignee creates a task with the given raw assignee value via
// a direct store call (bypassing MCP-layer normalization on purpose — this
// seeds fixture state for tests that then exercise the MCP handler).
func seedTaskWithAssignee(t *testing.T, s *Server, assignee string) uuid.UUID {
	t.Helper()
	task, err := s.gtd.CreateTask(context.Background(), gtd.CreateTaskParams{
		Title:    "throwaway task " + uuid.NewString(),
		Assignee: assignee,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	return task.ID
}

// --- add_task assignee schema ---

// TestAddTaskSchema_AssigneeMaxLength verifies the registered add_task tool
// exposes assignee's client-side length hint (mcp.MaxLength(100),
// tools_gtd.go) in its InputSchema — this is a defensive upper bound only
// (runtime discipline.NormalizeActor's canonical-actor allowlist remains
// the sole validation authority; this schema hint never replaces it). A
// regression here (the option dropped, or
// applied to the wrong property) would only be caught by a client-side
// schema inspector, never by a server-side test that only calls the handler
// — hence testing the registered *mcp.Tool directly instead of exercising
// handleAddTask.
func TestAddTaskSchema_AssigneeMaxLength(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	tool := s.MCPServer().GetTool("add_task")
	if tool == nil {
		t.Fatal("add_task not registered on MCPServer()")
	}

	assigneeSchema, ok := tool.Tool.InputSchema.Properties["assignee"].(map[string]any)
	if !ok {
		t.Fatalf("add_task InputSchema.Properties[%q] = %#v, want map[string]any",
			"assignee", tool.Tool.InputSchema.Properties["assignee"])
	}

	maxLength, ok := assigneeSchema["maxLength"]
	if !ok {
		t.Fatal("add_task assignee schema has no maxLength")
	}
	if maxLength != 100 {
		t.Errorf("add_task assignee maxLength = %v, want 100", maxLength)
	}
}

// --- add_task assignee validation ---

func TestAddTask_InvalidAssignee(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":    "test task",
		"assignee": "gemini",
	})
	if !r.IsError {
		t.Fatalf("add_task with unrecognized assignee must error, got: %s", resultText(r))
	}
	for _, canonical := range gtd.CanonicalActors {
		if !strings.Contains(resultText(r), canonical) {
			t.Errorf("error should list canonical value %q, got: %s", canonical, resultText(r))
		}
	}
}

func TestAddTask_ValidAssigneeAliasNormalized(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":    "test task",
		"assignee": "claude-code",
		"due_date": "2026-12-31T00:00:00Z",
	})
	if r.IsError {
		t.Fatalf("add_task with valid assignee alias must succeed, got: %s", resultText(r))
	}

	// jsonText(task) returns the bare task object, UNLESS vagueness warnings
	// are present (they are here — description is empty), in which case the
	// task is nested under a "task" key alongside "warnings". Handle both
	// shapes rather than assuming one.
	text := resultText(r)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v\nraw=%s", err, text)
	}
	idHolder := struct {
		ID string `json:"id"`
	}{}
	taskRaw := envelope["task"]
	if taskRaw == nil {
		taskRaw = json.RawMessage(text)
	}
	if err := json.Unmarshal(taskRaw, &idHolder); err != nil {
		t.Fatalf("unmarshal task id: %v\nraw=%s", err, text)
	}

	id, err := uuid.Parse(idHolder.ID)
	if err != nil {
		t.Fatalf("parsing returned task id %q: %v", idHolder.ID, err)
	}
	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if !got.Assignee.Valid || got.Assignee.String != "claude" {
		t.Errorf("assignee persisted = %+v, want normalized \"claude\"", got.Assignee)
	}
}

func TestAddTask_NoAssigneeAllowed(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":    "unowned task",
		"due_date": "2026-12-31T00:00:00Z",
	})
	if r.IsError {
		t.Fatalf("add_task with no assignee must succeed (unowned backlog item), got: %s", resultText(r))
	}
}

// --- update_task assignee validation ---

func TestUpdateTask_InvalidAssignee(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{
		"task_id":  id.String(),
		"assignee": "gemini",
	})
	if !r.IsError {
		t.Fatalf("update_task with unrecognized assignee must error, got: %s", resultText(r))
	}
	for _, canonical := range gtd.CanonicalActors {
		if !strings.Contains(resultText(r), canonical) {
			t.Errorf("error should list canonical value %q, got: %s", canonical, resultText(r))
		}
	}
}

func TestUpdateTask_ValidAssigneeAliasNormalized(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{
		"task_id":  id.String(),
		"assignee": "Codex Lead",
	})
	if r.IsError {
		t.Fatalf("update_task with valid assignee alias must succeed, got: %s", resultText(r))
	}
	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if !got.Assignee.Valid || got.Assignee.String != "codex" {
		t.Errorf("assignee persisted = %+v, want normalized \"codex\"", got.Assignee)
	}
}

// --- update_task in_progress requires assignee ---

func TestUpdateTask_InProgressWithoutAssignee_NoExisting(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s) // no assignee set

	r := callUpdateTask(t, s, map[string]any{
		"task_id": id.String(),
		"status":  "in_progress",
	})
	if !r.IsError {
		t.Fatalf("update_task to in_progress with no assignee (this call or existing) must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "assignee") {
		t.Errorf("error should mention assignee, got: %s", resultText(r))
	}
}

func TestUpdateTask_InProgressWithAssigneeThisCall(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s) // no assignee set

	r := callUpdateTask(t, s, map[string]any{
		"task_id":  id.String(),
		"status":   "in_progress",
		"assignee": "claude",
	})
	if r.IsError {
		t.Fatalf("update_task to in_progress with assignee supplied this call must succeed, got: %s", resultText(r))
	}
	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
}

func TestUpdateTask_InProgressWithExistingAssignee_NotBlocked(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	id := seedTaskWithAssignee(t, s, "claude") // already has an assignee

	r := callUpdateTask(t, s, map[string]any{
		"task_id": id.String(),
		"status":  "in_progress",
	})
	if r.IsError {
		t.Fatalf("update_task to in_progress on a task that already has an assignee must NOT be blocked, got: %s", resultText(r))
	}
	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
}

func TestUpdateTask_PendingStatusDoesNotRequireAssignee(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s) // no assignee set

	r := callUpdateTask(t, s, map[string]any{
		"task_id": id.String(),
		"status":  "pending",
	})
	if r.IsError {
		t.Fatalf("update_task to pending (not in_progress) must not require an assignee, got: %s", resultText(r))
	}
}

// [F1126-05] TestAssigneeSchema_EnumOnThreeTools: add_task, update_task and
// begin_task advertise gtd.CanonicalActors as the assignee enum, and the two
// core tools keep the short "Owner" description that pays for it.
func TestAssigneeSchema_EnumOnThreeTools(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)

	for _, name := range []string{"add_task", "update_task", "begin_task"} {
		tool := s.MCPServer().GetTool(name)
		if tool == nil {
			t.Fatalf("%s not registered on MCPServer()", name)
		}
		prop, ok := tool.Tool.InputSchema.Properties["assignee"].(map[string]any)
		if !ok {
			t.Fatalf("%s assignee schema = %#v, want map", name, tool.Tool.InputSchema.Properties["assignee"])
		}
		got, ok := prop["enum"].([]string)
		if !ok {
			t.Fatalf("%s assignee has no []string enum: %#v", name, prop["enum"])
		}
		if strings.Join(got, ",") != strings.Join(gtd.CanonicalActors, ",") {
			t.Errorf("%s assignee enum = %v, want %v", name, got, gtd.CanonicalActors)
		}
	}
	for _, name := range []string{"add_task", "update_task"} {
		prop := s.MCPServer().GetTool(name).Tool.InputSchema.Properties["assignee"].(map[string]any)
		if prop["description"] != "Owner" {
			t.Errorf("%s assignee description = %v, want %q", name, prop["description"], "Owner")
		}
	}
}

// [F1126-05] TestAssignee_EnumDoesNotRejectAliasesAtSeam: the advertised enum
// must not make the seam reject what NormalizeActor accepts (case, whitespace,
// aliases). Guards the noEnum("assignee") option on update_task and begin_task
// (add_task is guarded by TestAddTask_ValidAssigneeAliasNormalized).
func TestAssignee_EnumDoesNotRejectAliasesAtSeam(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	const seamEnumMsg = "assignee must be one of"

	for _, alias := range []string{"claude-code", "Claude", " codex "} {
		upd := callUpdateTask(t, s, map[string]any{
			"task_id":  seedTask(t, s).String(),
			"assignee": alias,
		})
		if strings.Contains(resultText(upd), seamEnumMsg) {
			t.Errorf("update_task rejected alias %q at the seam: %s", alias, resultText(upd))
		}
		if upd.IsError {
			t.Errorf("update_task with alias %q errored: %s", alias, resultText(upd))
		}

		beg := callTool(t, s, "begin_task", map[string]any{
			"task_id":  seedTask(t, s).String(),
			"assignee": alias,
		}, s.handleBeginTask)
		if strings.Contains(resultText(beg), seamEnumMsg) {
			t.Errorf("begin_task rejected alias %q at the seam: %s", alias, resultText(beg))
		}
	}

	// A genuinely unknown value is still rejected (by NormalizeActor).
	bad := callUpdateTask(t, s, map[string]any{"task_id": seedTask(t, s).String(), "assignee": "gemini"})
	if !bad.IsError {
		t.Errorf("update_task with assignee gemini must still error, got %s", resultText(bad))
	}
}
