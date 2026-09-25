package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

// seedRepoAwareTask creates a task with the given branch_name and (optional)
// project — a project-scoped sibling of seedBranchedTask above, needed here
// because TestMCPReconcileMergedPRs_RepoAware exercises repo resolution,
// which depends on a task's project (project.repo_name -> workspace repo).
func seedRepoAwareTask(t *testing.T, s *Server, title, branch string, projectID *uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{Title: title, Priority: 3, ProjectID: projectID})
	if err != nil {
		t.Fatalf("CreateTask %q: %v", title, err)
	}
	if _, err := s.gtd.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch for %q: %v", title, err)
	}
	return task.ID
}

// TestMCPReconcileMergedPRs_RepoAware pins [F0925-31] on the MCP surface
// using the REAL reconcileRepoResolver path (reconcileResolverOverride
// cleared). Every other reconcile_merged_prs test in this package runs
// through newTestWorkSessionServer, which sets reconcileResolverOverride =
// gtd.AssumeSameRepo — none of them exercise repo-based branch_name
// filtering at all. Mirrors the fixture shape of TestMatchMergedPRs_RepoAware
// (internal/gtd/reconcile_repo_test.go) and TestReconcileMergedPRs_RepoAware
// (internal/handler/reconcile_repo_test.go): a branch hit only auto-closes
// the task in the PR's own repo; another repo's same-named-elsewhere branch
// is skipped and counted; a task whose repo cannot be resolved becomes an
// unverified candidate, never a Match.
func TestMCPReconcileMergedPRs_RepoAware(t *testing.T) {
	// Not parallel: this file's :memory: SQLite pool has no SetMaxOpenConns(1); a second connection sees an empty DB.
	s := withReconcileCandidates(t, newTestWorkSessionServer(t))
	s.reconcileResolverOverride = nil // force the real reconcileRepoResolver path
	ctx := context.Background()

	slug := "Wayne997035/wayneblacktea"
	if _, err := s.workspace.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wbt-dir", GitHubSlug: &slug}); err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	proj, err := s.gtd.CreateProject(ctx, gtd.CreateProjectParams{Name: "wbt", Title: "WBT", RepoName: "wbt-dir"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	verified := seedRepoAwareTask(t, s, "in repo", "feat/repo-aware-a", &proj.ID)
	unknown := seedRepoAwareTask(t, s, "no project", "feat/repo-aware-a", nil)
	otherRepo := seedRepoAwareTask(t, s, "in repo, other branch", "feat/repo-aware-c", &proj.ID)

	previewRes := callReconcile(t, s, map[string]any{
		"merged_prs": []map[string]any{
			{
				"url": "https://github.com/Wayne997035/wayneblacktea/pull/1", "head_ref": "feat/repo-aware-a",
				"merged_at": "2026-05-18T12:00:00Z", "title": "a", "repo": slug,
			},
			{
				"url": "https://github.com/someone/else/pull/2", "head_ref": "feat/repo-aware-c",
				"merged_at": "2026-05-18T12:00:00Z", "title": "c", "repo": "someone/else",
			},
		},
	})
	preview := extractReconcileToken(t, previewRes)
	if len(preview.Matches) != 1 || preview.Matches[0].TaskID != verified.String() {
		t.Fatalf("preview matches = %+v, want exactly the in-repo task", preview.Matches)
	}

	body := resultText(previewRes)
	if !strings.Contains(body, `"skipped_repo_mismatch":1`) {
		t.Errorf("preview body must report skipped_repo_mismatch:1, got: %s", body)
	}
	if !strings.Contains(body, `"unverified_repo_candidates":1`) {
		t.Errorf("preview body must report unverified_repo_candidates:1, got: %s", body)
	}

	confirmRes := callReconcileWithArgs(t, s, nil, map[string]any{
		"confirm":         true,
		"reconcile_token": preview.ReconcileToken,
	})
	if confirmRes.IsError {
		t.Fatalf("confirm error: %s", resultText(confirmRes))
	}
	var applied reconcileAppliedEnvelope
	if err := json.Unmarshal([]byte(resultText(confirmRes)), &applied); err != nil {
		t.Fatalf("unmarshal confirm envelope: %v\nbody=%s", err, resultText(confirmRes))
	}
	if applied.Applied != 1 || len(applied.Matches) != 1 || applied.Matches[0].TaskID != verified.String() {
		t.Fatalf("confirm must apply ONLY the in-repo task, got: %+v", applied)
	}

	assertTaskCompletionStatus(t, ctx, s, verified, true, "in-repo")
	assertTaskCompletionStatus(t, ctx, s, unknown, false, "unverified-repo")
	assertTaskCompletionStatus(t, ctx, s, otherRepo, false, "mismatched-repo")
}

// assertTaskCompletionStatus fetches taskID and checks whether it ended up
// completed against wantCompleted — extracted only to bring
// TestMCPReconcileMergedPRs_RepoAware's cyclomatic complexity back under the
// gocyclo threshold; the three checks and their messages are unchanged, just
// relocated.
func assertTaskCompletionStatus(
	t *testing.T, ctx context.Context, s *Server, taskID uuid.UUID, wantCompleted bool, label string,
) {
	t.Helper()
	got, err := s.gtd.GetTaskByID(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTaskByID %s: %v", label, err)
	}
	isCompleted := got.Status == taskStatusCompleted
	if isCompleted != wantCompleted {
		t.Errorf("%s task status = %q, want completed=%v", label, got.Status, wantCompleted)
	}
}
