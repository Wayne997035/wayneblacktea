package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// SQLite parity for the reconcile flow (backend-security-design §6.3 dual-backend).
// Mirrors the PG tests in internal/gtd/reconcile_test.go.

const taskStatusCompleted = "completed"

// TestSQLiteReconcileExactMatch covers the basic happy path.
func TestSQLiteReconcileExactMatch(t *testing.T) {
	store := openMem(t, "")
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "do the thing", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/x"
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("UpdateTask seed branch: %v", err)
	}

	prURL := "https://github.com/owner/repo/pull/1"
	result, err := gtd.MatchMergedPRs(ctx, store, []gtd.MergedPR{{
		URL:      prURL,
		HeadRef:  branch,
		MergedAt: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatalf("MatchMergedPRs: %v", err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(result.Matches))
	}
	if result.Matches[0].Reason != gtd.MatchReasonBranchNameExact {
		t.Errorf("reason = %q, want branch_name_exact", result.Matches[0].Reason)
	}

	applied, err := store.BatchCompleteTasksByPRMatch(ctx, result.Matches)
	if err != nil {
		t.Fatalf("BatchComplete: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}

	got, err := store.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != taskStatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if !got.PRUrl.Valid || got.PRUrl.String != prURL {
		t.Errorf("pr_url = %+v, want %q", got.PRUrl, prURL)
	}
}

// TestSQLiteReconcileIdempotent verifies 2nd identical call is a no-op.
func TestSQLiteReconcileIdempotent(t *testing.T) {
	store := openMem(t, "")
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "once", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/idem-sqlite"
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	prs := []gtd.MergedPR{{
		URL: "https://github.com/owner/repo/pull/22", HeadRef: branch,
	}}
	r1, err := gtd.MatchMergedPRs(ctx, store, prs)
	if err != nil {
		t.Fatalf("first MatchMergedPRs: %v", err)
	}
	a1, err := store.BatchCompleteTasksByPRMatch(ctx, r1.Matches)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if a1 != 1 {
		t.Fatalf("first applied = %d, want 1", a1)
	}

	r2, err := gtd.MatchMergedPRs(ctx, store, prs)
	if err != nil {
		t.Fatalf("second MatchMergedPRs: %v", err)
	}
	if len(r2.Matches) != 0 {
		t.Errorf("second matches = %d, want 0", len(r2.Matches))
	}
	a2, err := store.BatchCompleteTasksByPRMatch(ctx, r2.Matches)
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if a2 != 0 {
		t.Errorf("second applied = %d, want 0", a2)
	}
}

// TestSQLiteReconcileNoMatch: PR head_ref doesn't match any task's branch_name.
func TestSQLiteReconcileNoMatch(t *testing.T) {
	store := openMem(t, "")
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "untouched", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	mine := "feature/y"
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &mine}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	result, err := gtd.MatchMergedPRs(ctx, store, []gtd.MergedPR{{
		URL:     "https://github.com/owner/repo/pull/33",
		HeadRef: "feature/x",
	}})
	if err != nil {
		t.Fatalf("MatchMergedPRs: %v", err)
	}
	if len(result.Matches) != 0 {
		t.Errorf("matches = %d, want 0", len(result.Matches))
	}
	if result.NoMatch != 1 {
		t.Errorf("no_match = %d, want 1", result.NoMatch)
	}
}

// TestSQLiteReconcileAmbiguousBranchPicksMostRecent covers the multi-task case.
func TestSQLiteReconcileAmbiguousBranchPicksMostRecent(t *testing.T) {
	store := openMem(t, "")
	ctx := context.Background()

	branch := "feature/dup-sqlite"
	older, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "older", Priority: 3})
	if err != nil {
		t.Fatalf("older CreateTask: %v", err)
	}
	if _, err := store.UpdateTask(ctx, older.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("older branch: %v", err)
	}

	// SQLite nowRFC3339 has millisecond resolution; 50ms gap is safe.
	time.Sleep(50 * time.Millisecond)
	newer, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "newer", Priority: 3})
	if err != nil {
		t.Fatalf("newer CreateTask: %v", err)
	}
	if _, err := store.UpdateTask(ctx, newer.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("newer branch: %v", err)
	}

	result, err := gtd.MatchMergedPRs(ctx, store, []gtd.MergedPR{{
		URL: "https://github.com/owner/repo/pull/44", HeadRef: branch,
	}})
	if err != nil {
		t.Fatalf("MatchMergedPRs: %v", err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(result.Matches))
	}
	if result.Matches[0].TaskID != newer.ID {
		t.Errorf("picked task = %s, want newer = %s", result.Matches[0].TaskID, newer.ID)
	}
	if len(result.Ambiguous) != 1 || result.Ambiguous[0].TaskID != older.ID {
		t.Errorf("ambiguous = %+v, want exactly older=%s", result.Ambiguous, older.ID)
	}

	// The newer task auto-closes; the older one remains pending.
	if _, err := store.BatchCompleteTasksByPRMatch(ctx, result.Matches); err != nil {
		t.Fatalf("BatchComplete: %v", err)
	}
	gotOld, err := store.GetTaskByID(ctx, older.ID)
	if err != nil {
		t.Fatalf("GetTaskByID older: %v", err)
	}
	if gotOld.Status != "pending" {
		t.Errorf("older.status = %q, want pending (ambiguous → unchanged)", gotOld.Status)
	}
}

// TestSQLiteReconcilePRURLMatchPriority covers pr_url winning over branch.
func TestSQLiteReconcilePRURLMatchPriority(t *testing.T) {
	store := openMem(t, "")
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "url-linked", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	prURL := "https://github.com/owner/repo/pull/55"
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{PRUrl: &prURL}); err != nil {
		t.Fatalf("seed pr_url: %v", err)
	}

	result, err := gtd.MatchMergedPRs(ctx, store, []gtd.MergedPR{{
		URL: prURL, HeadRef: "irrelevant-branch",
	}})
	if err != nil {
		t.Fatalf("MatchMergedPRs: %v", err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(result.Matches))
	}
	if result.Matches[0].Reason != gtd.MatchReasonPRURLExact {
		t.Errorf("reason = %q, want pr_url_exact", result.Matches[0].Reason)
	}
}
