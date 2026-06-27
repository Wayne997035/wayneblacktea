package gtd_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

const (
	statusCompleted = "completed"
	statusPending   = "pending"
)

// TestReconcileMergedPRs_ExactMatch_PG_AutoApplies covers the happy path on
// the Postgres backend: seed a task with branch_name="feature/x", call match
// with HeadRef="feature/x" → match returned + BatchCompleteTasksByPRMatch
// actually flips the row.
func TestReconcileMergedPRs_ExactMatch_PG_AutoApplies(t *testing.T) {
	pool := openTestPgPool(t)
	store := newPgGTDStore(pool, nil)
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
		Title:    "feat: x",
		Body:     "closes the task",
		Repo:     "owner/repo",
	}})
	if err != nil {
		t.Fatalf("MatchMergedPRs: %v", err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(result.Matches))
	}
	m := result.Matches[0]
	if m.TaskID != task.ID {
		t.Errorf("matched task = %s, want %s", m.TaskID, task.ID)
	}
	if m.Reason != gtd.MatchReasonBranchNameExact {
		t.Errorf("reason = %q, want branch_name_exact", m.Reason)
	}
	if m.PRUrl != prURL {
		t.Errorf("PRUrl = %q, want %q", m.PRUrl, prURL)
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
	if got.Status != statusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if !got.PRUrl.Valid || got.PRUrl.String != prURL {
		t.Errorf("pr_url = %+v, want %q", got.PRUrl, prURL)
	}
}

// TestReconcileMergedPRs_PG_Idempotent: 2nd identical call returns 0 changes.
func TestReconcileMergedPRs_PG_Idempotent(t *testing.T) {
	pool := openTestPgPool(t)
	store := newPgGTDStore(pool, nil)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "do once", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/idem"
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	prURL := "https://github.com/owner/repo/pull/2"
	prs := []gtd.MergedPR{{URL: prURL, HeadRef: branch, MergedAt: time.Now().UTC()}}

	// First call.
	r1, err := gtd.MatchMergedPRs(ctx, store, prs)
	if err != nil {
		t.Fatalf("first MatchMergedPRs: %v", err)
	}
	applied1, err := store.BatchCompleteTasksByPRMatch(ctx, r1.Matches)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if applied1 != 1 {
		t.Fatalf("first applied = %d, want 1", applied1)
	}

	// Second call with same payload — task already 'completed', matcher should
	// skip it (filtered out of pending/in_progress in matchSingle).
	r2, err := gtd.MatchMergedPRs(ctx, store, prs)
	if err != nil {
		t.Fatalf("second MatchMergedPRs: %v", err)
	}
	if len(r2.Matches) != 0 {
		t.Errorf("second match count = %d, want 0 (matcher should skip completed)", len(r2.Matches))
	}
	applied2, err := store.BatchCompleteTasksByPRMatch(ctx, r2.Matches)
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}
	if applied2 != 0 {
		t.Errorf("second applied = %d, want 0", applied2)
	}
}

// TestReconcileMergedPRs_PG_NoMatch: task w/ branch_name="feature/y" not
// touched by PR head_ref="feature/x".
func TestReconcileMergedPRs_PG_NoMatch(t *testing.T) {
	pool := openTestPgPool(t)
	store := newPgGTDStore(pool, nil)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "untouched", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	branch := "feature/y"
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	result, err := gtd.MatchMergedPRs(ctx, store, []gtd.MergedPR{{
		URL:     "https://github.com/owner/repo/pull/3",
		HeadRef: "feature/x", // different
	}})
	if err != nil {
		t.Fatalf("MatchMergedPRs: %v", err)
	}
	if len(result.Matches) != 0 {
		t.Errorf("matches = %d, want 0", len(result.Matches))
	}
	if result.NoMatch != 1 {
		t.Errorf("no_match count = %d, want 1", result.NoMatch)
	}

	// Confirm the task is still pending.
	got, err := store.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != statusPending {
		t.Errorf("status changed despite no match: got %q", got.Status)
	}
}

// TestReconcileMergedPRs_PG_MultipleSameBranch_PicksMostRecent: 2 pending
// tasks with branch_name="feature/x" → reconcile picks the most-recent
// (highest updated_at), the other surfaces as ambiguous.
func TestReconcileMergedPRs_PG_MultipleSameBranch_PicksMostRecent(t *testing.T) {
	pool := openTestPgPool(t)
	store := newPgGTDStore(pool, nil)
	ctx := context.Background()

	branch := "feature/dup"

	older, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "older", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask older: %v", err)
	}
	if _, err := store.UpdateTask(ctx, older.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed older branch: %v", err)
	}

	// Ensure a clock gap so updated_at differs.
	time.Sleep(20 * time.Millisecond)
	newer, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "newer", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask newer: %v", err)
	}
	if _, err := store.UpdateTask(ctx, newer.ID, gtd.UpdateTaskParams{BranchName: &branch}); err != nil {
		t.Fatalf("seed newer branch: %v", err)
	}

	result, err := gtd.MatchMergedPRs(ctx, store, []gtd.MergedPR{{
		URL:     "https://github.com/owner/repo/pull/4",
		HeadRef: branch,
	}})
	if err != nil {
		t.Fatalf("MatchMergedPRs: %v", err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want 1", len(result.Matches))
	}
	if result.Matches[0].TaskID != newer.ID {
		t.Errorf("picked = %s, want newer = %s", result.Matches[0].TaskID, newer.ID)
	}
	if len(result.Ambiguous) != 1 {
		t.Fatalf("ambiguous count = %d, want 1", len(result.Ambiguous))
	}
	if result.Ambiguous[0].TaskID != older.ID {
		t.Errorf("ambiguous = %s, want older = %s", result.Ambiguous[0].TaskID, older.ID)
	}
}

// TestReconcileMergedPRs_PG_PRURLMatch: task w/ pr_url already set, reconcile
// w/ same URL auto-closes.
func TestReconcileMergedPRs_PG_PRURLMatch(t *testing.T) {
	pool := openTestPgPool(t)
	store := newPgGTDStore(pool, nil)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "url-linked", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	prURL := "https://github.com/owner/repo/pull/5"
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{PRUrl: &prURL}); err != nil {
		t.Fatalf("seed pr_url: %v", err)
	}

	// Note: pass HeadRef as something that does NOT match any branch — proves
	// the match came from pr_url, not branch.
	result, err := gtd.MatchMergedPRs(ctx, store, []gtd.MergedPR{{
		URL:     prURL,
		HeadRef: "no-such-branch",
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

	if _, err := store.BatchCompleteTasksByPRMatch(ctx, result.Matches); err != nil {
		t.Fatalf("BatchComplete: %v", err)
	}
	got, err := store.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if got.Status != statusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
}

// TestReconcileMergedPRs_PG_CaseInsensitivePRURL: PR URL match is
// case-insensitive (GitHub allows mixed case in the path segments).
func TestReconcileMergedPRs_PG_CaseInsensitivePRURL(t *testing.T) {
	pool := openTestPgPool(t)
	store := newPgGTDStore(pool, nil)
	ctx := context.Background()

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "case-test", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	stored := "https://github.com/Wayne997035/wayneblacktea/pull/7"
	if _, err := store.UpdateTask(ctx, task.ID, gtd.UpdateTaskParams{PRUrl: &stored}); err != nil {
		t.Fatalf("seed pr_url: %v", err)
	}

	// Different case in the org segment.
	supplied := "https://github.com/wayne997035/wayneblacktea/pull/7"
	result, err := gtd.MatchMergedPRs(ctx, store, []gtd.MergedPR{{
		URL: supplied, HeadRef: "irrelevant",
	}})
	if err != nil {
		t.Fatalf("MatchMergedPRs: %v", err)
	}
	if len(result.Matches) != 1 {
		t.Fatalf("matches = %d, want 1 (case-insensitive)", len(result.Matches))
	}
}
