package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callAddTask invokes add_task (seam + handleAddTask) with the given args.
func callAddTask(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "add_task", args, s.handleAddTask)
}

// callUpdateTask invokes update_task (seam + handleUpdateTask) with the given
// args. Never returns a Go error; tool-level errors surface via
// CallToolResult.IsError.
func callUpdateTask(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "update_task", args, s.handleUpdateTask)
}

// callGetUpcomingWork invokes get_upcoming_work (seam + handleGetUpcomingWork)
// with the given args.
func callGetUpcomingWork(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "get_upcoming_work", args, s.handleGetUpcomingWork)
}

// --- parseUpdateTaskArgs validation ---
//
// TestParseUpdateTaskArgs_InvalidStatus was removed: status enum-membership
// moved from this function to the seam (update_task's status arg declares
// mcp.Enum(...) at registration — see toolspec.go validate()). Coverage for
// "status=completed via update_task must error" is preserved end-to-end by
// TestUpdateTask_InvalidStatusReturnsError below, which now exercises the
// seam's check rather than parseUpdateTaskArgs' own.

func TestParseUpdateTaskArgs_ValidStatuses(t *testing.T) {
	for _, st := range []string{"pending", "in_progress", "cancelled"} {
		p, msg := parseUpdateTaskArgs(UpdateTaskArgs{Status: st})
		if msg != "" {
			t.Errorf("status %q should be valid, got error: %s", st, msg)
		}
		if p.Status == nil || *p.Status != st {
			t.Errorf("status %q not propagated correctly", st)
		}
	}
}

func TestParseUpdateTaskArgs_PriorityOutOfRange(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{Priority: 6})
	if msg == "" {
		t.Fatal("expected error for priority=6")
	}
}

func TestParseUpdateTaskArgs_ImportanceOutOfRange(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{Importance: 4})
	if msg == "" {
		t.Fatal("expected error for importance=4")
	}
}

func TestParseUpdateTaskArgs_InvalidDueDate(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{DueDate: "not-a-date"})
	if msg == "" {
		t.Fatal("expected error for bad due_date")
	}
	if !strings.Contains(msg, "RFC3339") {
		t.Errorf("error should mention RFC3339, got: %s", msg)
	}
}

func TestParseUpdateTaskArgs_ValidDueDate(t *testing.T) {
	p, msg := parseUpdateTaskArgs(UpdateTaskArgs{DueDate: "2026-12-31T00:00:00Z"})
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
	// p6-6: in_progress now requires an assignee (this call supplies one
	// since the seeded task has none) — see
	// tools_gtd_assignee_test.go for the dedicated enforcement tests.
	r := callUpdateTask(t, s, map[string]any{"task_id": id.String(), "status": "in_progress", "assignee": "claude"})
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

// --- branch_name / pr_url validation (M-1, M-2) ---

func TestParseUpdateTaskArgs_BranchNameTooLong(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{BranchName: strPtr(strings.Repeat("a", 256))})
	if msg == "" {
		t.Fatal("expected error for branch_name > 255 chars")
	}
	if !strings.Contains(msg, "255") {
		t.Errorf("error should mention 255, got: %s", msg)
	}
}

func TestParseUpdateTaskArgs_BranchNameNewline(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{BranchName: strPtr("feature/bad\nname")})
	if msg == "" {
		t.Fatal("expected error for branch_name with \\n control char")
	}
	if !strings.Contains(msg, "control") {
		t.Errorf("error should mention control characters, got: %s", msg)
	}
}

func TestParseUpdateTaskArgs_BranchNameDEL(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{BranchName: strPtr("feature/bad\x7fname")})
	if msg == "" {
		t.Fatal("expected error for branch_name with DEL (0x7F)")
	}
}

func TestParseUpdateTaskArgs_BranchNameUnicodeControl(t *testing.T) {
	// U+200B zero-width space — a Unicode format char (Cf) that bytes < 0x20 alone would miss.
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{BranchName: strPtr("feature/bad" + "\u200b" + "name")})
	if msg == "" {
		t.Fatal("expected error for branch_name with U+200B zero-width space")
	}
}

func TestParseUpdateTaskArgs_PRUrlInvalidHost(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{PRUrl: strPtr("https://notgithub.com/foo/bar/pull/1")})
	if msg == "" {
		t.Fatal("expected error for pr_url on non-github host")
	}
}

func TestParseUpdateTaskArgs_PRUrlJavaScript(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{PRUrl: strPtr("javascript:alert(1)")})
	if msg == "" {
		t.Fatal("expected error for javascript: pr_url")
	}
}

func TestParseUpdateTaskArgs_PRUrlIssuesNotPulls(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{PRUrl: strPtr("https://github.com/foo/bar/issues/1")})
	if msg == "" {
		t.Fatal("expected error for pr_url pointing to issues (not pulls)")
	}
}

func TestParseUpdateTaskArgs_PRUrlTrailingPath(t *testing.T) {
	_, msg := parseUpdateTaskArgs(UpdateTaskArgs{PRUrl: strPtr("https://github.com/owner/repo/pull/42/files")})
	if msg == "" {
		t.Fatal("expected error for pr_url with trailing path /files")
	}
}

func TestParseUpdateTaskArgs_ValidBranchAndPR(t *testing.T) {
	p, msg := parseUpdateTaskArgs(UpdateTaskArgs{
		BranchName: strPtr("feature/my-feature"),
		PRUrl:      strPtr("https://github.com/owner/repo/pull/42"),
	})
	if msg != "" {
		t.Fatalf("valid branch_name+pr_url should pass, got: %s", msg)
	}
	if p.BranchName == nil || *p.BranchName != "feature/my-feature" {
		t.Errorf("BranchName not set correctly, got: %v", p.BranchName)
	}
	if p.PRUrl == nil || *p.PRUrl != "https://github.com/owner/repo/pull/42" {
		t.Errorf("PRUrl not set correctly, got: %v", p.PRUrl)
	}
}

func TestParseUpdateTaskArgs_ClearPRUrl(t *testing.T) {
	// Explicit empty string clears the field without triggering URL validation.
	p, msg := parseUpdateTaskArgs(UpdateTaskArgs{PRUrl: strPtr("")})
	if msg != "" {
		t.Fatalf("explicit empty pr_url should pass (clear semantics), got: %s", msg)
	}
	if p.PRUrl == nil {
		t.Fatal("PRUrl should be set (to empty string) for explicit clear")
	}
	if *p.PRUrl != "" {
		t.Errorf("expected empty string, got: %q", *p.PRUrl)
	}
}

// --- handleAddTask required-title (seam-absorbed) ---

func TestAddTask_MissingTitle(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{"due_date": "2026-12-31T00:00:00Z"})
	if !r.IsError || resultText(r) != wantTitleRequired {
		t.Errorf("got %q, want %q", resultText(r), wantTitleRequired)
	}
}

// --- handleAddTask branch_name / pr_url end-to-end ---

func TestAddTask_BranchNameTooLong(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":       "test task",
		"branch_name": strings.Repeat("a", 256),
	})
	if !r.IsError {
		t.Fatalf("add_task with branch_name > 255 must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "255") {
		t.Errorf("error should mention 255, got: %s", resultText(r))
	}
}

func TestAddTask_BranchNameControlChar(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":       "test task",
		"branch_name": "feature/bad\nname",
	})
	if !r.IsError {
		t.Fatalf("add_task with control char in branch_name must error, got: %s", resultText(r))
	}
}

func TestAddTask_PRUrlInvalid(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":  "test task",
		"pr_url": "javascript:alert(1)",
	})
	if !r.IsError {
		t.Fatalf("add_task with invalid pr_url must error, got: %s", resultText(r))
	}
}

func TestAddTask_ValidBranchAndPR(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddTask(t, s, map[string]any{
		"title":       "test task",
		"branch_name": "feature/my-feature",
		"pr_url":      "https://github.com/owner/repo/pull/42",
		"due_date":    "2026-12-31T00:00:00Z", // required since OVERRIDE 1
	})
	if r.IsError {
		t.Fatalf("add_task with valid branch+pr must succeed, got: %s", resultText(r))
	}
}

// --- handleUpdateTask branch_name / pr_url end-to-end ---

func TestUpdateTask_BranchNameTooLong(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{
		"task_id":     id.String(),
		"branch_name": strings.Repeat("a", 256),
	})
	if !r.IsError {
		t.Fatalf("update_task with branch_name > 255 must error, got: %s", resultText(r))
	}
}

func TestUpdateTask_BranchNameDEL(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{
		"task_id":     id.String(),
		"branch_name": "feature/bad\x7fname",
	})
	if !r.IsError {
		t.Fatalf("update_task with DEL in branch_name must error, got: %s", resultText(r))
	}
}

func TestUpdateTask_PRUrlNotGitHub(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{
		"task_id": id.String(),
		"pr_url":  "https://notgithub.com/foo/bar/pull/1",
	})
	if !r.IsError {
		t.Fatalf("update_task with non-github pr_url must error, got: %s", resultText(r))
	}
}

func TestUpdateTask_ValidBranchAndPR(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)
	r := callUpdateTask(t, s, map[string]any{
		"task_id":     id.String(),
		"branch_name": "feature/my-feature",
		"pr_url":      "https://github.com/owner/repo/pull/42",
	})
	if r.IsError {
		t.Fatalf("update_task with valid branch+pr must succeed, got: %s", resultText(r))
	}
}
