package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	verified := seedBranchTask(t, ctx, store, "in repo", "feat/a", &proj.ID)
	unknown := seedBranchTask(t, ctx, store, "no project", "feat/a", nil)
	otherRepo := seedBranchTask(t, ctx, store, "in repo, other branch", "feat/c", &proj.ID)

	h := handler.NewReconcileHandler(store, candStore).WithWorkspaceStore(ws)
	rec := runReconcileRequest(t, h, mustJSON(t, map[string]any{"merged_prs": []map[string]any{
		{
			"url": "https://github.com/Wayne997035/wayneblacktea/pull/1", "head_ref": "feat/a",
			"merged_at": "2026-05-18T12:00:00Z", "title": "a", "repo": slug,
		},
		{
			"url": "https://github.com/someone/else/pull/2", "head_ref": "feat/c",
			"merged_at": "2026-05-18T12:00:00Z", "title": "c", "repo": "someone/else",
		},
	}}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	assertReconcileCounts(t, rec, 1, 1, 1)
	assertTaskStatuses(t, ctx, store, map[uuid.UUID]string{verified: "completed", unknown: "pending", otherRepo: "pending"})
	assertPendingUnverifiedCandidate(t, ctx, candStore, unknown)
}

// seedBranchTask creates a task with the given branch_name — extracted from
// TestReconcileMergedPRs_RepoAware's inline closure only to bring that
// test's cyclomatic complexity back under the gocyclo threshold.
func seedBranchTask(
	t *testing.T, ctx context.Context, store *sqlite.GTDStore, title, branch string, projectID *uuid.UUID,
) uuid.UUID {
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

// assertReconcileCounts decodes rec's JSON body and checks the
// applied/skipped/unverified counters — see seedBranchTask for why this is
// a top-level func.
func assertReconcileCounts(t *testing.T, rec *httptest.ResponseRecorder, wantApplied, wantSkipped, wantUnverified int) {
	t.Helper()
	var resp struct {
		Applied                  int `json:"applied"`
		SkippedRepoMismatch      int `json:"skipped_repo_mismatch"`
		UnverifiedRepoCandidates int `json:"unverified_repo_candidates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Applied != wantApplied || resp.SkippedRepoMismatch != wantSkipped || resp.UnverifiedRepoCandidates != wantUnverified {
		t.Errorf("response = %+v, want applied %d, skipped %d, unverified %d", resp, wantApplied, wantSkipped, wantUnverified)
	}
}

// assertTaskStatuses checks each task in want resolved to the expected
// status — see seedBranchTask for why this is a top-level func.
func assertTaskStatuses(t *testing.T, ctx context.Context, store *sqlite.GTDStore, want map[uuid.UUID]string) {
	t.Helper()
	for id, wantStatus := range want {
		got, err := store.GetTaskByID(ctx, id)
		if err != nil || got.Status != wantStatus {
			t.Errorf("task %s status = %v (err %v), want %s", id, got, err, wantStatus)
		}
	}
}

// assertPendingUnverifiedCandidate checks a pending
// pr_merged_repo_unverified candidate exists for taskID — see seedBranchTask
// for why this is a top-level func.
func assertPendingUnverifiedCandidate(
	t *testing.T, ctx context.Context, candStore *completioncandidate.SQLiteStore, taskID uuid.UUID,
) {
	t.Helper()
	pending, err := candStore.ListPendingCandidates(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingCandidates: %v", err)
	}
	found := false
	for _, c := range pending {
		if c.TaskID == taskID && c.Reason == completioncandidate.ReasonPRMergedRepoUnverified {
			found = true
		}
	}
	if !found {
		t.Errorf("no pending pr_merged_repo_unverified candidate for task %s: %+v", taskID, pending)
	}
}
