package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callBeginTask invokes begin_task (seam + handleBeginTask) with the given args.
func callBeginTask(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "begin_task", args, s.handleBeginTask)
}

// beginTaskResponse mirrors the MCP envelope.
type beginTaskResponse struct {
	Task struct {
		ID         string  `json:"id"`
		Status     string  `json:"status"`
		BranchName *string `json:"branch_name,omitempty"`
		PRUrl      *string `json:"pr_url,omitempty"`
	} `json:"task"`
	BranchNameSuggestion string `json:"branch_name_suggestion"`
}

// TestMCPBeginTask_PersistsBranchAndPR covers the sprint M-8 linkage flow:
// the MCP begin_task tool MUST persist branch_name + pr_url when supplied so a
// later reconcile_merged_prs call can match.
func TestMCPBeginTask_PersistsBranchAndPR(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	r := callBeginTask(t, s, map[string]any{
		"task_id":     id.String(),
		"branch_name": "feature/foo",
		"pr_url":      "https://github.com/Wayne997035/wayneblacktea/pull/999",
		"assignee":    "claude",
	})
	if r.IsError {
		t.Fatalf("begin_task error: %s", resultText(r))
	}

	// Inspect the response — task.branch_name and task.pr_url must be set.
	var resp beginTaskResponse
	if err := json.Unmarshal([]byte(resultText(r)), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nraw=%s", err, resultText(r))
	}
	if resp.Task.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", resp.Task.Status)
	}
	// Verify via direct store read — defence-in-depth in case the response
	// shape changes.
	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if !got.BranchName.Valid || got.BranchName.String != "feature/foo" {
		t.Errorf("branch_name persisted = %+v, want feature/foo", got.BranchName)
	}
	if !got.PRUrl.Valid || got.PRUrl.String != "https://github.com/Wayne997035/wayneblacktea/pull/999" {
		t.Errorf("pr_url persisted = %+v, want the PR URL", got.PRUrl)
	}
}

// TestMCPBeginTask_RejectsInvalidPRURL covers MCP-side validation.
func TestMCPBeginTask_RejectsInvalidPRURL(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	cases := []struct {
		name    string
		args    map[string]any
		wantSub string
	}{
		{
			name:    "non-github URL",
			args:    map[string]any{"task_id": id.String(), "pr_url": "https://notgithub.com/foo/bar/pull/1"},
			wantSub: "valid GitHub PR URL",
		},
		{
			name:    "branch_name with newline",
			args:    map[string]any{"task_id": id.String(), "branch_name": "feature/bad\nname"},
			wantSub: "control characters",
		},
		{
			name:    "branch_name too long",
			args:    map[string]any{"task_id": id.String(), "branch_name": strings.Repeat("a", 256)},
			wantSub: "255 characters",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := callBeginTask(t, s, tc.args)
			if !r.IsError {
				t.Fatalf("expected tool error, got: %s", resultText(r))
			}
			if !strings.Contains(resultText(r), tc.wantSub) {
				t.Errorf("error must mention %q, got: %s", tc.wantSub, resultText(r))
			}

			// Task MUST NOT have been mutated (no linkage persisted, no status flip).
			got, err := s.gtd.GetTaskByID(context.Background(), id)
			if err != nil {
				t.Fatalf("GetTaskByID: %v", err)
			}
			if got.BranchName.Valid {
				t.Error("branch_name should not be persisted after validation failure")
			}
			if got.PRUrl.Valid {
				t.Error("pr_url should not be persisted after validation failure")
			}
			if got.Status != string(gtd.TaskStatusPending) {
				t.Errorf("status changed to %q despite validation failure", got.Status)
			}
		})
	}
}

// TestMCPBeginTask_NoLinkageArgs covers the legacy no-args call. seedTaskWithAssignee
// gives the task an existing owner so the p6-7 in_progress guard is satisfied
// without needing an assignee arg on this call (see
// TestMCPBeginTask_RequiresAssignee below for the no-owner rejection case).
func TestMCPBeginTask_NoLinkageArgs(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTaskWithAssignee(t, s, "claude")

	r := callBeginTask(t, s, map[string]any{"task_id": id.String()})
	if r.IsError {
		t.Fatalf("begin_task error: %s", resultText(r))
	}

	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
	if got.BranchName.Valid {
		t.Errorf("branch_name should remain NULL when not supplied: got %+v", got.BranchName)
	}
}

// TestMCPBeginTask_RequiresAssignee verifies begin_task rejects a task that
// has no assignee (neither already on the row nor supplied this call) — the
// p6-7 domain-layer gate applied to the begin_task path.
func TestMCPBeginTask_RequiresAssignee(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s) // no assignee set

	r := callBeginTask(t, s, map[string]any{"task_id": id.String()})
	if !r.IsError {
		t.Fatalf("begin_task on an unowned task with no assignee arg must error, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "assignee") {
		t.Errorf("error should mention assignee, got: %s", resultText(r))
	}

	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != taskStatusPending {
		t.Errorf("status changed to %q despite rejected begin_task", got.Status)
	}
}

// TestMCPBeginTask_AssigneeArgPersistsAndUnblocks verifies that supplying
// assignee on the begin_task call itself both persists the assignee and
// satisfies the in_progress guard for a task that had no prior owner.
func TestMCPBeginTask_AssigneeArgPersistsAndUnblocks(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s) // no assignee set

	r := callBeginTask(t, s, map[string]any{"task_id": id.String(), "assignee": "codex"})
	if r.IsError {
		t.Fatalf("begin_task with assignee arg on unowned task must succeed, got: %s", resultText(r))
	}

	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != statusInProgress {
		t.Errorf("status = %q, want in_progress", got.Status)
	}
	if !got.Assignee.Valid || got.Assignee.String != "codex" {
		t.Errorf("assignee persisted = %+v, want normalized \"codex\"", got.Assignee)
	}
}

// TestMCPBeginTask_InvalidAssignee verifies begin_task rejects an
// unrecognized assignee value via the same NormalizeActor allowlist used by
// add_task/update_task.
func TestMCPBeginTask_InvalidAssignee(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id := seedTask(t, s)

	r := callBeginTask(t, s, map[string]any{"task_id": id.String(), "assignee": "gemini"})
	if !r.IsError {
		t.Fatalf("begin_task with unrecognized assignee must error, got: %s", resultText(r))
	}
	for _, canonical := range gtd.CanonicalActors {
		if !strings.Contains(resultText(r), canonical) {
			t.Errorf("error should list canonical value %q, got: %s", canonical, resultText(r))
		}
	}

	got, err := s.gtd.GetTaskByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != taskStatusPending {
		t.Errorf("status changed to %q despite rejected assignee", got.Status)
	}
}
