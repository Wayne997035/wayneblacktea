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

// statusConfirmationRequired is the envelope status both delete tools use
// for their step-1 preview.
const statusConfirmationRequired = "confirmation_required"

// callDeleteProject invokes delete_project (seam + handleDeleteProject).
func callDeleteProject(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	return callTool(t, "delete_project", args, s.handleDeleteProject)
}

// seedProjectWithTasks creates a project with n tasks under it.
func seedProjectWithTasks(t *testing.T, s *Server, n int) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	name := "throwaway-" + uuid.NewString()
	p, err := s.gtd.CreateProject(ctx, gtd.CreateProjectParams{Name: name, Title: "Throwaway"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	for i := 0; i < n; i++ {
		if _, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{
			ProjectID: &p.ID, Title: "task " + uuid.NewString(),
		}); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}
	return p.ID, name
}

// projectStillExists reads through the store rather than trusting the tool's
// own answer — a handler that reported success without deleting would other-
// wise satisfy every assertion made on its return value alone.
func projectStillExists(t *testing.T, s *Server, id uuid.UUID) bool {
	t.Helper()
	p, err := s.gtd.GetProjectByID(context.Background(), id)
	return err == nil && p != nil
}

// TestDeleteProject_FirstCallPreviewsTheBlastRadius is the reason this tool
// is two-step rather than one. The count is the whole point: confirming
// destroys every task under the project, and a caller that cannot see how
// many that is has no way to notice it aimed at the wrong project.
func TestDeleteProject_FirstCallPreviewsTheBlastRadius(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id, name := seedProjectWithTasks(t, s, 4)

	r := callDeleteProject(t, s, map[string]any{"project_id": id.String()})
	if r.IsError {
		t.Fatalf("first call must succeed, got: %s", resultText(r))
	}

	var payload struct {
		Status        string `json:"status"`
		ProjectID     string `json:"project_id"`
		ProjectName   string `json:"project_name"`
		TasksToDelete int    `json:"tasks_to_delete"`
		DeletionToken string `json:"deletion_token"`
		ExpiresAt     string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &payload); err != nil {
		t.Fatalf("unmarshal preview: %v\nraw=%s", err, resultText(r))
	}
	if payload.Status != statusConfirmationRequired {
		t.Errorf("status = %q, want %s", payload.Status, statusConfirmationRequired)
	}
	if payload.TasksToDelete != 4 {
		t.Errorf("tasks_to_delete = %d, want 4", payload.TasksToDelete)
	}
	if payload.ProjectName != name {
		t.Errorf("project_name = %q, want %q — the caller cannot confirm what it cannot identify",
			payload.ProjectName, name)
	}
	if payload.DeletionToken == "" || payload.ExpiresAt == "" {
		t.Error("preview must carry a deletion_token and expires_at")
	}

	// The decisive assertion: a preview must not have deleted anything.
	if !projectStillExists(t, s, id) {
		t.Fatal("the first call deleted the project — it is supposed to be a preview")
	}
}

// TestDeleteProject_ConfirmDeletesProjectAndTasks is the positive control for
// every refusal test below: without it, a delete_project that refused
// everything unconditionally would pass them all.
func TestDeleteProject_ConfirmDeletesProjectAndTasks(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id, _ := seedProjectWithTasks(t, s, 3)

	token := extractToken(t, callDeleteProject(t, s, map[string]any{"project_id": id.String()}))
	r := callDeleteProject(t, s, map[string]any{
		"project_id": id.String(), "confirm": true, "deletion_token": token,
	})
	if r.IsError {
		t.Fatalf("confirm must succeed, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "3 task(s) removed") {
		t.Errorf("result should report how many tasks went with the project, got: %s", resultText(r))
	}
	if projectStillExists(t, s, id) {
		t.Error("confirm reported success but the project is still there")
	}
	tasks, err := s.gtd.TasksByProjectAllStatuses(context.Background(), id)
	if err != nil {
		t.Fatalf("TasksByProjectAllStatuses: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("%d task(s) survived the project delete", len(tasks))
	}
}

// TestDeleteProject_UnknownProjectIsRejectedBeforeAnyTokenIsIssued: handing
// back a token for an id that does not exist would produce a confirmable
// no-op, and "deleted (0 task(s) removed)" reads exactly like success.
func TestDeleteProject_UnknownProjectIsRejectedBeforeAnyTokenIsIssued(t *testing.T) {
	s := newTestWorkSessionServer(t)

	missing := uuid.New()
	r := callDeleteProject(t, s, map[string]any{"project_id": missing.String()})
	if !r.IsError {
		t.Fatalf("expected an error for an unknown project, got: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "project not found") {
		t.Errorf("want 'project not found', got: %s", resultText(r))
	}
	if _, ok := s.deleteTokens.Load(projectDeletionKey(missing.String())); ok {
		t.Error("a token was issued for a project that does not exist")
	}
}

// TestDeleteProject_ConfirmRefusals covers the confirm-step guards, all of
// which live in spendPendingDeletion. The assertion that matters in every
// case is the same one: the project is still there afterwards.
func TestDeleteProject_ConfirmRefusals(t *testing.T) {
	tests := []struct {
		name    string
		args    func(id uuid.UUID, token string) map[string]any
		wantMsg string
	}{
		{
			name: "no token supplied",
			args: func(id uuid.UUID, _ string) map[string]any {
				return map[string]any{"project_id": id.String(), "confirm": true}
			},
			wantMsg: "deletion_token is required",
		},
		{
			name: "wrong token",
			args: func(id uuid.UUID, _ string) map[string]any {
				return map[string]any{
					"project_id": id.String(), "confirm": true, "deletion_token": uuid.NewString(),
				}
			},
			wantMsg: "deletion_token mismatch",
		},
		{
			name: "confirm without ever previewing",
			args: func(_ uuid.UUID, _ string) map[string]any {
				return map[string]any{
					"project_id": uuid.NewString(), "confirm": true, "deletion_token": uuid.NewString(),
				}
			},
			wantMsg: "no pending deletion for this project_id",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestWorkSessionServer(t)
			id, _ := seedProjectWithTasks(t, s, 2)
			token := extractToken(t, callDeleteProject(t, s, map[string]any{"project_id": id.String()}))

			r := callDeleteProject(t, s, tc.args(id, token))
			if !r.IsError {
				t.Fatalf("expected refusal, got success: %s", resultText(r))
			}
			if !strings.Contains(resultText(r), tc.wantMsg) {
				t.Errorf("want %q in the refusal, got: %s", tc.wantMsg, resultText(r))
			}
			if !projectStillExists(t, s, id) {
				t.Error("a refused confirm deleted the project anyway")
			}
		})
	}
}

// TestDeleteProject_CannotDestroyAPendingTaskDeletion is why the
// pending-deletion map is namespaced by entity kind. Both tools key the same
// sync.Map with a bare UUID, and the confirm step does NOT check that the id
// names an existing entity — that check lives in step 1 only.
//
// So without the "project:" prefix, calling delete_project with a TASK's id
// and that task's token finds the task's record, validates it, consumes it,
// and then deletes a project that does not exist: a caller who merely knows a
// task id can destroy someone else's pending confirmation, and the victim's
// retry reports "no pending deletion" with no indication of why.
//
// This is the whole of what the prefix buys. Two entities never share a UUID,
// so a test that simply pairs a project and a task with (as always) different
// ids passes with or without the prefix — measured, not assumed. The probe
// below therefore aims delete_project at the task's own id, which is the only
// input that reaches the shared key.
func TestDeleteProject_CannotDestroyAPendingTaskDeletion(t *testing.T) {
	s := newTestWorkSessionServer(t)
	taskID := seedTask(t, s)

	taskToken := extractToken(t, callDeleteTask(t, s, map[string]any{"task_id": taskID.String()}))

	// The attack: delete_project aimed at the task's id, carrying the task's
	// own token. It must find nothing, and above all must not consume.
	r := callDeleteProject(t, s, map[string]any{
		"project_id": taskID.String(), "confirm": true, "deletion_token": taskToken,
	})
	if !r.IsError {
		t.Fatalf("delete_project spent a token from delete_task's key space: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "no pending deletion for this project_id") {
		t.Errorf("want a no-pending-deletion refusal, got: %s", resultText(r))
	}

	// The property that actually matters: the victim's token is still live.
	r = callDeleteTask(t, s, map[string]any{
		"task_id": taskID.String(), "confirm": true, "deletion_token": taskToken,
	})
	if r.IsError {
		t.Fatalf("the pending delete_task confirmation was destroyed by a delete_project call: %s",
			resultText(r))
	}

	// Positive control: delete_project does spend tokens from its OWN key
	// space. Without it, both assertions above would also pass if
	// delete_project refused every token it was ever handed.
	projectID, _ := seedProjectWithTasks(t, s, 1)
	own := extractToken(t, callDeleteProject(t, s, map[string]any{"project_id": projectID.String()}))
	r = callDeleteProject(t, s, map[string]any{
		"project_id": projectID.String(), "confirm": true, "deletion_token": own,
	})
	if r.IsError {
		t.Fatalf("delete_project refused a token it issued itself: %s", resultText(r))
	}
	if projectStillExists(t, s, projectID) {
		t.Error("the control reported success but the project is still there")
	}
}

// TestDeleteProject_TokenIsSingleUse: a spent token must not delete a second
// project, and re-confirming must not silently succeed.
func TestDeleteProject_TokenIsSingleUse(t *testing.T) {
	s := newTestWorkSessionServer(t)
	id, _ := seedProjectWithTasks(t, s, 1)

	token := extractToken(t, callDeleteProject(t, s, map[string]any{"project_id": id.String()}))
	args := map[string]any{"project_id": id.String(), "confirm": true, "deletion_token": token}

	if r := callDeleteProject(t, s, args); r.IsError {
		t.Fatalf("first confirm must succeed: %s", resultText(r))
	}
	r := callDeleteProject(t, s, args)
	if !r.IsError {
		t.Fatalf("the same token confirmed a second delete: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "no pending deletion") {
		t.Errorf("want 'no pending deletion' on replay, got: %s", resultText(r))
	}
}
