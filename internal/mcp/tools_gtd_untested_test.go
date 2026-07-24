package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// This file covers the 11 tools_gtd.go handlers that had zero direct test
// coverage before the handler-seam migration (ADR 0001):
// list_projects, create_project, update_project, list_goals, create_goal,
// update_project_status, get_project, log_activity, task_checklist_add_item,
// task_checklist_toggle, task_checklist_complete.

// ---- callXxx helpers ----

func callListProjects(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "list_projects", args, s.handleListProjects)
}

func callCreateProject(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "create_project", args, s.handleCreateProject)
}

func callUpdateProject(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "update_project", args, s.handleUpdateProject)
}

func callListGoals(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "list_goals", args, s.handleListGoals)
}

func callCreateGoal(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "create_goal", args, s.handleCreateGoal)
}

func callUpdateProjectStatus(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "update_project_status", args, s.handleUpdateProjectStatus)
}

func callGetProject(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "get_project", args, s.handleGetProject)
}

func callLogActivity(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "log_activity", args, s.handleLogActivity)
}

func callChecklistAddItem(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "task_checklist_add_item", args, s.handleChecklistAddItem)
}

func callChecklistToggle(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "task_checklist_toggle", args, s.handleChecklistToggle)
}

func callChecklistComplete(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "task_checklist_complete", args, s.handleChecklistComplete)
}

// seedProject creates a project via the store and returns it.
func seedProject(t *testing.T, s *Server, name string) uuid.UUID {
	t.Helper()
	p, err := s.gtd.CreateProject(context.Background(), gtd.CreateProjectParams{
		Name:  name,
		Title: "title-" + name,
		Area:  "engineering",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return p.ID
}

// ---- list_projects ----

func TestListProjects_EmptyDB(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListProjects(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("empty DB should succeed, got: %s", resultText(r))
	}
	var projects []json.RawMessage
	if err := json.Unmarshal([]byte(resultText(r)), &projects); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}
}

func TestListProjects_ReturnsSeeded(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedProject(t, s, "proj-"+uuid.NewString()[:8])
	r := callListProjects(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), id.String()) {
		t.Errorf("seeded project %s not found in response", id)
	}
}

// ---- create_project ----

func TestCreateProject_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	name := "proj-" + uuid.NewString()[:8]
	r := callCreateProject(t, s, map[string]any{
		"name":  name,
		"title": "Title",
		"area":  "engineering",
	})
	if r.IsError {
		t.Fatalf("valid create_project should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), name) {
		t.Errorf("response should contain project name, got: %s", resultText(r))
	}
}

func TestCreateProject_MissingRequiredFields(t *testing.T) {
	s := newTestWorkSessionServer(t)
	cases := []map[string]any{
		{"title": "t", "area": "a"},
		{"name": "n", "area": "a"},
		{"name": "n", "title": "t"},
	}
	for _, args := range cases {
		r := callCreateProject(t, s, args)
		if !r.IsError {
			t.Fatalf("missing required field must error, args=%v", args)
		}
		want := "name, title and area are required"
		if resultText(r) != want {
			t.Errorf("message = %q, want %q", resultText(r), want)
		}
	}
}

func TestCreateProject_InvalidRepoName(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCreateProject(t, s, map[string]any{
		"name": "proj-" + uuid.NewString()[:8], "title": "t", "area": "a",
		"repo_name": "bad repo name!",
	})
	if !r.IsError {
		t.Fatalf("invalid repo_name must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "repo_name must match") {
		t.Errorf("error should mention repo_name format, got: %s", resultText(r))
	}
}

func TestCreateProject_DuplicateName(t *testing.T) {
	s := newTestWorkSessionServer(t)
	name := "proj-" + uuid.NewString()[:8]
	first := callCreateProject(t, s, map[string]any{"name": name, "title": "t", "area": "a"})
	if first.IsError {
		t.Fatalf("first create must succeed, got: %s", resultText(first))
	}
	second := callCreateProject(t, s, map[string]any{"name": name, "title": "t2", "area": "a"})
	if !second.IsError {
		t.Fatalf("duplicate name must error, got: %s", resultText(second))
	}
	if resultText(second) != "project name already exists" {
		t.Errorf("message = %q, want %q", resultText(second), "project name already exists")
	}
}

func TestCreateProject_InvalidGoalIDUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCreateProject(t, s, map[string]any{
		"name": "proj-" + uuid.NewString()[:8], "title": "t", "area": "a",
		"goal_id": "not-a-uuid",
	})
	if !r.IsError {
		t.Fatalf("invalid goal_id must error, got: %s", resultText(r))
	}
	if resultText(r) != "invalid goal_id UUID" {
		t.Errorf("message = %q, want %q", resultText(r), "invalid goal_id UUID")
	}
}

// ---- update_project ----

func TestUpdateProject_HappyPath_PartialUpdate(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedProject(t, s, "proj-"+uuid.NewString()[:8])

	r := callUpdateProject(t, s, map[string]any{
		"project_id": id.String(),
		"title":      "new title",
	})
	if r.IsError {
		t.Fatalf("update should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "new title") {
		t.Errorf("updated title not in response, got: %s", resultText(r))
	}
	// area was omitted — must be preserved (still "engineering" from seedProject).
	if !strings.Contains(resultText(r), "engineering") {
		t.Errorf("preserved area not in response, got: %s", resultText(r))
	}
}

func TestUpdateProject_MissingProjectID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateProject(t, s, map[string]any{"title": "x"})
	if !r.IsError {
		t.Fatalf("missing project_id must error, got: %s", resultText(r))
	}
	if resultText(r) != "project_id is required" {
		t.Errorf("message = %q, want %q", resultText(r), "project_id is required")
	}
}

func TestUpdateProject_InvalidUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateProject(t, s, map[string]any{"project_id": "not-a-uuid"})
	if !r.IsError {
		t.Fatalf("invalid UUID must error, got: %s", resultText(r))
	}
	if resultText(r) != errMsgInvalidProjectIDUUID {
		t.Errorf("message = %q, want %q", resultText(r), errMsgInvalidProjectIDUUID)
	}
}

func TestUpdateProject_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateProject(t, s, map[string]any{"project_id": uuid.New().String(), "title": "x"})
	if !r.IsError {
		t.Fatalf("nonexistent project must error, got: %s", resultText(r))
	}
	if resultText(r) != "project not found" {
		t.Errorf("message = %q, want %q", resultText(r), "project not found")
	}
}

func TestUpdateProject_InvalidStatusEnum(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedProject(t, s, "proj-"+uuid.NewString()[:8])
	r := callUpdateProject(t, s, map[string]any{"project_id": id.String(), "status": "bogus"})
	if !r.IsError {
		t.Fatalf("invalid status must error, got: %s", resultText(r))
	}
	want := "status must be one of: active, completed, archived, on_hold"
	if resultText(r) != want {
		t.Errorf("message = %q, want %q", resultText(r), want)
	}
}

func TestUpdateProject_TitleExceedsMaxLength(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedProject(t, s, "proj-"+uuid.NewString()[:8])
	r := callUpdateProject(t, s, map[string]any{
		"project_id": id.String(),
		"title":      strings.Repeat("a", 501),
	})
	if !r.IsError {
		t.Fatalf("title over 500 chars must error, got: %s", resultText(r))
	}
	if resultText(r) != "title exceeds 500 characters" {
		t.Errorf("message = %q, want %q", resultText(r), "title exceeds 500 characters")
	}
}

func TestUpdateProject_RepoNameClearedOnExplicitEmpty(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedProject(t, s, "proj-"+uuid.NewString()[:8])
	// Set repo_name first.
	set := callUpdateProject(t, s, map[string]any{"project_id": id.String(), "repo_name": "wayneblacktea"})
	if set.IsError {
		t.Fatalf("setting repo_name should succeed, got: %s", resultText(set))
	}
	// Explicit empty clears it.
	clear := callUpdateProject(t, s, map[string]any{"project_id": id.String(), "repo_name": ""})
	if clear.IsError {
		t.Fatalf("clearing repo_name should succeed, got: %s", resultText(clear))
	}
	got, err := s.gtd.GetProjectByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetProjectByID: %v", err)
	}
	if got.RepoName.Valid && got.RepoName.String != "" {
		t.Errorf("repo_name should be cleared, got: %+v", got.RepoName)
	}
}

// ---- list_goals ----

func TestListGoals_EmptyDB(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListGoals(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("empty DB should succeed, got: %s", resultText(r))
	}
	var goals []json.RawMessage
	if err := json.Unmarshal([]byte(resultText(r)), &goals); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(goals) != 0 {
		t.Errorf("expected 0 goals, got %d", len(goals))
	}
}

func TestListGoals_ReturnsSeeded(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCreateGoal(t, s, map[string]any{"title": "goal-1", "area": "career"})
	if r.IsError {
		t.Fatalf("create_goal should succeed, got: %s", resultText(r))
	}
	list := callListGoals(t, s, map[string]any{})
	if list.IsError {
		t.Fatalf("list_goals should succeed, got: %s", resultText(list))
	}
	if !strings.Contains(resultText(list), "goal-1") {
		t.Errorf("seeded goal not found, got: %s", resultText(list))
	}
}

// ---- create_goal ----

func TestCreateGoal_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCreateGoal(t, s, map[string]any{"title": "ship v2", "area": "career"})
	if r.IsError {
		t.Fatalf("valid create_goal should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "ship v2") {
		t.Errorf("title not in response, got: %s", resultText(r))
	}
}

func TestCreateGoal_MissingRequiredFields(t *testing.T) {
	s := newTestWorkSessionServer(t)
	cases := []map[string]any{
		{"area": "career"},
		{"title": "t"},
	}
	for _, args := range cases {
		r := callCreateGoal(t, s, args)
		if !r.IsError {
			t.Fatalf("missing required field must error, args=%v", args)
		}
		want := "title and area are required"
		if resultText(r) != want {
			t.Errorf("message = %q, want %q", resultText(r), want)
		}
	}
}

func TestCreateGoal_InvalidDueDate(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCreateGoal(t, s, map[string]any{"title": "t", "area": "a", "due_date": "not-a-date"})
	if !r.IsError {
		t.Fatalf("invalid due_date must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "RFC3339") {
		t.Errorf("error should mention RFC3339, got: %s", resultText(r))
	}
}

func TestCreateGoal_ValidDueDate(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callCreateGoal(t, s, map[string]any{"title": "t", "area": "a", "due_date": "2026-12-31T00:00:00Z"})
	if r.IsError {
		t.Fatalf("valid due_date should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "2026-12-31") {
		t.Errorf("due_date not in response, got: %s", resultText(r))
	}
}

// ---- update_project_status ----

func TestUpdateProjectStatus_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedProject(t, s, "proj-"+uuid.NewString()[:8])
	r := callUpdateProjectStatus(t, s, map[string]any{"project_id": id.String(), "status": "completed"})
	if r.IsError {
		t.Fatalf("valid status update should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), `"completed"`) {
		t.Errorf("status not updated in response, got: %s", resultText(r))
	}
}

func TestUpdateProjectStatus_MissingFields(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedProject(t, s, "proj-"+uuid.NewString()[:8])

	r := callUpdateProjectStatus(t, s, map[string]any{"status": "completed"})
	if !r.IsError || resultText(r) != "project_id is required" {
		t.Errorf("missing project_id: got %q, want %q", resultText(r), "project_id is required")
	}

	r = callUpdateProjectStatus(t, s, map[string]any{"project_id": id.String()})
	if !r.IsError || resultText(r) != "status is required" {
		t.Errorf("missing status: got %q, want %q", resultText(r), "status is required")
	}
}

func TestUpdateProjectStatus_InvalidEnum(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedProject(t, s, "proj-"+uuid.NewString()[:8])
	r := callUpdateProjectStatus(t, s, map[string]any{"project_id": id.String(), "status": "bogus"})
	if !r.IsError {
		t.Fatalf("invalid status must error, got: %s", resultText(r))
	}
	want := "status must be one of: active, completed, archived, on_hold"
	if resultText(r) != want {
		t.Errorf("message = %q, want %q", resultText(r), want)
	}
}

func TestUpdateProjectStatus_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateProjectStatus(t, s, map[string]any{"project_id": uuid.New().String(), "status": "completed"})
	if !r.IsError || resultText(r) != "project not found" {
		t.Errorf("got %q, want %q", resultText(r), "project not found")
	}
}

// ---- get_project ----

func TestGetProject_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	name := "proj-" + uuid.NewString()[:8]
	seedProject(t, s, name)
	r := callGetProject(t, s, map[string]any{"name": name})
	if r.IsError {
		t.Fatalf("should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), name) {
		t.Errorf("project name not in response, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "recent_decisions") {
		t.Errorf("response should include recent_decisions envelope, got: %s", resultText(r))
	}
}

func TestGetProject_MissingName(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetProject(t, s, map[string]any{})
	if !r.IsError || resultText(r) != "name is required" {
		t.Errorf("got %q, want %q", resultText(r), "name is required")
	}
}

func TestGetProject_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callGetProject(t, s, map[string]any{"name": "does-not-exist"})
	if !r.IsError {
		t.Fatalf("nonexistent project must error, got: %s", resultText(r))
	}
	want := `project "does-not-exist" not found`
	if resultText(r) != want {
		t.Errorf("message = %q, want %q", resultText(r), want)
	}
}

// ---- log_activity ----

func TestLogActivity_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callLogActivity(t, s, map[string]any{"actor": "claude-code", "action": "did something"})
	if r.IsError {
		t.Fatalf("should succeed, got: %s", resultText(r))
	}
	if resultText(r) != "activity logged" {
		t.Errorf("message = %q, want %q", resultText(r), "activity logged")
	}
}

func TestLogActivity_MissingRequiredFields(t *testing.T) {
	s := newTestWorkSessionServer(t)
	cases := []map[string]any{
		{"action": "did something"},
		{"actor": "claude-code"},
	}
	for _, args := range cases {
		r := callLogActivity(t, s, args)
		want := "actor and action are required"
		if !r.IsError || resultText(r) != want {
			t.Errorf("args=%v: got %q, want %q", args, resultText(r), want)
		}
	}
}

func TestLogActivity_InvalidProjectIDUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callLogActivity(t, s, map[string]any{
		"actor": "claude-code", "action": "did something", "project_id": "not-a-uuid",
	})
	if !r.IsError || resultText(r) != errMsgInvalidProjectIDUUID {
		t.Errorf("got %q, want %q", resultText(r), errMsgInvalidProjectIDUUID)
	}
}

// ---- task_checklist_add_item ----

func TestChecklistAddItem_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callChecklistAddItem(t, s, map[string]any{"task_id": id.String(), "title": "step one"})
	if r.IsError {
		t.Fatalf("should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "step one") {
		t.Errorf("item title not in response, got: %s", resultText(r))
	}
}

func TestChecklistAddItem_MissingTaskID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callChecklistAddItem(t, s, map[string]any{"title": "step one"})
	if !r.IsError || resultText(r) != "task_id is required" {
		t.Errorf("got %q, want %q", resultText(r), "task_id is required")
	}
}

func TestChecklistAddItem_MissingTitle(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callChecklistAddItem(t, s, map[string]any{"task_id": id.String()})
	if !r.IsError || resultText(r) != wantTitleRequired {
		t.Errorf("got %q, want %q", resultText(r), wantTitleRequired)
	}
}

func TestChecklistAddItem_TitleExceedsMaxLength(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callChecklistAddItem(t, s, map[string]any{
		"task_id": id.String(), "title": strings.Repeat("a", 501),
	})
	if !r.IsError || resultText(r) != "title exceeds 500 characters" {
		t.Errorf("got %q, want %q", resultText(r), "title exceeds 500 characters")
	}
}

func TestChecklistAddItem_FileRefAndNotesExceedMaxLength(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	r := callChecklistAddItem(t, s, map[string]any{
		"task_id": id.String(), "title": "x", "file_ref": strings.Repeat("a", 2001),
	})
	if !r.IsError || resultText(r) != "file_ref exceeds 2000 characters" {
		t.Errorf("file_ref: got %q, want %q", resultText(r), "file_ref exceeds 2000 characters")
	}

	r = callChecklistAddItem(t, s, map[string]any{
		"task_id": id.String(), "title": "x", "notes": strings.Repeat("a", 2001),
	})
	if !r.IsError || resultText(r) != "notes exceeds 2000 characters" {
		t.Errorf("notes: got %q, want %q", resultText(r), "notes exceeds 2000 characters")
	}
}

// TestChecklistAddItem_TitleAllControlChars_BlankAfterSanitisation verifies a
// title made entirely of control characters passes the seam's raw-value
// required/maxLength checks (non-empty, under the cap) but is still rejected
// once sanitised down to an empty string — business logic the seam cannot
// express (it only sees the pre-sanitised value).
func TestChecklistAddItem_TitleAllControlChars_BlankAfterSanitisation(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callChecklistAddItem(t, s, map[string]any{"task_id": id.String(), "title": "\x01\x02\x03"})
	if !r.IsError || resultText(r) != "title must not be blank after sanitisation" {
		t.Errorf("got %q, want %q", resultText(r), "title must not be blank after sanitisation")
	}
}

func TestChecklistAddItem_TaskNotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callChecklistAddItem(t, s, map[string]any{"task_id": uuid.New().String(), "title": "x"})
	if !r.IsError || resultText(r) != "task not found" {
		t.Errorf("got %q, want %q", resultText(r), "task not found")
	}
}

// ---- task_checklist_toggle ----

func seedChecklistItem(t *testing.T, s *Server, taskID uuid.UUID) uuid.UUID {
	t.Helper()
	wsID := workspaceUUIDFromPgtype(s.gtd.WorkspaceID())
	item := gtd.ChecklistItem{ID: uuid.New(), Title: "item"}
	items, err := s.gtd.AddChecklistItem(context.Background(), taskID, wsID, item)
	if err != nil {
		t.Fatalf("AddChecklistItem: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected at least one checklist item after AddChecklistItem")
	}
	return items[len(items)-1].ID
}

func TestChecklistToggle_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)
	itemID := seedChecklistItem(t, s, taskID)

	r := callChecklistToggle(t, s, map[string]any{
		"task_id": taskID.String(), "item_id": itemID.String(), "done": true,
	})
	if r.IsError {
		t.Fatalf("should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), `"done": true`) {
		t.Errorf("item should be marked done, got: %s", resultText(r))
	}
}

func TestChecklistToggle_MissingDone(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)
	itemID := seedChecklistItem(t, s, taskID)
	r := callChecklistToggle(t, s, map[string]any{"task_id": taskID.String(), "item_id": itemID.String()})
	if !r.IsError || resultText(r) != "done is required" {
		t.Errorf("got %q, want %q", resultText(r), "done is required")
	}
}

func TestChecklistToggle_TaskOrItemNotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)
	r := callChecklistToggle(t, s, map[string]any{
		"task_id": taskID.String(), "item_id": uuid.New().String(), "done": true,
	})
	if !r.IsError || resultText(r) != "task or item not found" {
		t.Errorf("got %q, want %q", resultText(r), "task or item not found")
	}
}

func TestChecklistToggle_EvidenceURLExceedsMaxLength(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)
	itemID := seedChecklistItem(t, s, taskID)
	r := callChecklistToggle(t, s, map[string]any{
		"task_id": taskID.String(), "item_id": itemID.String(), "done": true,
		"evidence_url": strings.Repeat("a", 2001),
	})
	if !r.IsError || resultText(r) != "evidence_url exceeds 2000 characters" {
		t.Errorf("got %q, want %q", resultText(r), "evidence_url exceeds 2000 characters")
	}
}

func TestChecklistToggle_InvalidItemIDUUID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)
	r := callChecklistToggle(t, s, map[string]any{
		"task_id": taskID.String(), "item_id": "not-a-uuid", "done": true,
	})
	if !r.IsError || resultText(r) != "invalid item_id UUID" {
		t.Errorf("got %q, want %q", resultText(r), "invalid item_id UUID")
	}
}

// ---- task_checklist_complete ----

func TestChecklistComplete_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)
	itemID := seedChecklistItem(t, s, taskID)

	r := callChecklistComplete(t, s, map[string]any{"task_id": taskID.String(), "item_id": itemID.String()})
	if r.IsError {
		t.Fatalf("should succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), `"done": true`) {
		t.Errorf("item should be marked done, got: %s", resultText(r))
	}
}

func TestChecklistComplete_MissingItemID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)
	r := callChecklistComplete(t, s, map[string]any{"task_id": taskID.String()})
	if !r.IsError || resultText(r) != "item_id is required" {
		t.Errorf("got %q, want %q", resultText(r), "item_id is required")
	}
}

func TestChecklistComplete_TaskOrItemNotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)
	r := callChecklistComplete(t, s, map[string]any{
		"task_id": taskID.String(), "item_id": uuid.New().String(),
	})
	if !r.IsError || resultText(r) != "task or item not found" {
		t.Errorf("got %q, want %q", resultText(r), "task or item not found")
	}
}
