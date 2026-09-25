package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/completioncandidate"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

// TestReconcileMergedPRs_RepoAware is the end-to-end [F0925-31] check on
// real SQLite stores with the production resolver (no override): a branch
// hit closes only the task in the PR's repo, a task whose repo is unknown
// becomes a pending pr_merged_repo_unverified candidate, and another repo's
// same-named branch is skipped and counted.
func TestReconcileMergedPRs_RepoAware(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, err := sqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := sqlite.NewGTDStore(d)
	candStore := completioncandidate.NewSQLiteStore(d.SqlConn(), "")
	ws := sqlite.NewWorkspaceStore(d)

	slug := "Wayne997035/wayneblacktea"
	if _, err := ws.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wbt-dir", GitHubSlug: &slug}); err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "wbt", Title: "WBT", RepoName: "wbt-dir"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	newBranchTask := func(title, branch string, projectID *uuid.UUID) uuid.UUID {
		t.Helper()
		task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: title, Priority: 3, ProjectID: projectID})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
			t.Fatalf("seed branch: %v", err)
		}
		return task.ID
	}
	verified := newBranchTask("in repo", "feat/a", &proj.ID)
	unknown := newBranchTask("no project", "feat/a", nil)
	otherRepo := newBranchTask("in repo, other branch", "feat/c", &proj.ID)

	h := handler.NewReconcileHandler(store, candStore).WithWorkspaceStore(ws)
	rec := runReconcileRequest(t, h, mustJSON(t, map[string]any{"merged_prs": []map[string]any{
		{"url": "https://github.com/Wayne997035/wayneblacktea/pull/1", "head_ref": "feat/a",
			"merged_at": "2026-05-18T12:00:00Z", "title": "a", "repo": slug},
		{"url": "https://github.com/someone/else/pull/2", "head_ref": "feat/c",
			"merged_at": "2026-05-18T12:00:00Z", "title": "c", "repo": "someone/else"},
	}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Applied                  int `json:"applied"`
		SkippedRepoMismatch      int `json:"skipped_repo_mismatch"`
		UnverifiedRepoCandidates int `json:"unverified_repo_candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Applied != 1 || resp.SkippedRepoMismatch != 1 || resp.UnverifiedRepoCandidates != 1 {
		t.Errorf("response = %+v, want applied 1, skipped 1, unverified 1", resp)
	}
	for id, want := range map[uuid.UUID]string{verified: "completed", unknown: "pending", otherRepo: "pending"} {
		got, err := store.GetTaskByID(ctx, id)
		if err != nil || got.Status != want {
			t.Errorf("task %s status = %v (err %v), want %s", id, got, err, want)
		}
	}
	pending, err := candStore.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates: %v", err)
	}
	found := false
	for _, c := range pending {
		if c.TaskID == unknown && c.Reason == completioncandidate.ReasonPRMergedRepoUnverified {
			found = true
		}
	}
	if !found {
		t.Errorf("no pending pr_merged_repo_unverified candidate for task %s: %+v", unknown, pending)
	}
}
