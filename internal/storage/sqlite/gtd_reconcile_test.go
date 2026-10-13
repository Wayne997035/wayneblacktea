package sqlite_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// SQLite parity for the reconcile flow (dual-backend).
// Mirrors the PG tests in internal/gtd/reconcile_test.go.

const (
	taskStatusCompleted = "completed"
	taskStatusPending   = "pending"
)

// TestSQLiteReconcileExactMatch covers the basic happy path.
func TestSQLiteReconcileExactMatch(t *testing.T) {
	t.Parallel() // [F0925-10]
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
	}}, gtd.AssumeSameRepo)
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
	if len(applied) != 1 {
		t.Errorf("applied = %d, want 1", len(applied))
	}
	if !applied[task.ID] {
		t.Errorf("applied map missing task %s", task.ID)
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
	t.Parallel() // [F0925-10]
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
	r1, err := gtd.MatchMergedPRs(ctx, store, prs, gtd.AssumeSameRepo)
	if err != nil {
		t.Fatalf("first MatchMergedPRs: %v", err)
	}
	a1, err := store.BatchCompleteTasksByPRMatch(ctx, r1.Matches)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if len(a1) != 1 {
		t.Fatalf("first applied = %d, want 1", len(a1))
	}

	r2, err := gtd.MatchMergedPRs(ctx, store, prs, gtd.AssumeSameRepo)
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
	if len(a2) != 0 {
		t.Errorf("second applied = %d, want 0", len(a2))
	}
}

// TestSQLiteReconcileNoMatch: PR head_ref doesn't match any task's branch_name.
func TestSQLiteReconcileNoMatch(t *testing.T) {
	t.Parallel() // [F0925-10]
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
	}}, gtd.AssumeSameRepo)
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
	t.Parallel() // [F0925-10]
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
	}}, gtd.AssumeSameRepo)
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
	if gotOld.Status != taskStatusPending {
		t.Errorf("older.status = %q, want pending (ambiguous → unchanged)", gotOld.Status)
	}
}

// TestSQLiteReconcilePRURLMatchPriority covers pr_url winning over branch.
func TestSQLiteReconcilePRURLMatchPriority(t *testing.T) {
	t.Parallel() // [F0925-10]
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
	}}, gtd.AssumeSameRepo)
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

// TestSQLiteBatchCompleteTasksByPRMatch_ActivityLogFailureRollsBackTaskUpdate
// [F203-03] pins "mark-complete and its audit row move together": when the
// activity_log INSERT of a later match fails, the earlier match's task UPDATE
// must roll back with it. It is the SQLite half of the PG probe F203-02
// (dual-backend parity); SQLite already runs the whole batch in one *sql.Tx,
// so this probe is green today and goes red if anyone splits the batch into
// per-match commits.
//
// The failure is injected with a BEFORE INSERT trigger keyed on a marker in
// activity_log.notes (the notes carry head_ref), so only the second match's
// audit INSERT fails. Every assertion uses t.Errorf so one run reports all
// violated invariants; the fixed [F203-03/N] prefixes are what the
// acceptance counts grep for.
func TestSQLiteBatchCompleteTasksByPRMatch_ActivityLogFailureRollsBackTaskUpdate(t *testing.T) {
	t.Parallel() // [F203-03]
	store, d := openMemWithDB(t, "")
	ctx := context.Background()

	const (
		injectedMsg = "f203 injected failure"
		failMarker  = "F203FAILMARKER"
		prURLA      = "https://github.com/owner/repo/pull/2031"
		prURLB      = "https://github.com/owner/repo/pull/2032"
	)

	if err := d.ExecContext(ctx, `CREATE TRIGGER f203_fail BEFORE INSERT ON activity_log
		WHEN NEW.notes LIKE '%`+failMarker+`%'
		BEGIN SELECT RAISE(ABORT, '`+injectedMsg+`'); END`); err != nil {
		t.Fatalf("create failure-injection trigger: %v", err)
	}

	taskA, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "f203 task A", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask A: %v", err)
	}
	taskB, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "f203 task B", Priority: 3})
	if err != nil {
		t.Fatalf("CreateTask B: %v", err)
	}

	// A is applied first and succeeds; B's audit INSERT then trips the trigger.
	matches := []gtd.Match{
		{TaskID: taskA.ID, Reason: gtd.MatchReasonBranchNameExact, PRUrl: prURLA, PRHeadRef: "feature/f203-a"},
		{TaskID: taskB.ID, Reason: gtd.MatchReasonBranchNameExact, PRUrl: prURLB, PRHeadRef: "feature/" + failMarker},
	}
	_, batchErr := store.BatchCompleteTasksByPRMatch(ctx, matches)

	switch {
	case batchErr == nil:
		t.Errorf("[F203-03/1] BatchCompleteTasksByPRMatch error = nil, want error containing %q", injectedMsg)
	case !strings.Contains(batchErr.Error(), injectedMsg):
		t.Errorf("[F203-03/1] BatchCompleteTasksByPRMatch error = %v, want it to contain %q", batchErr, injectedMsg)
	}

	gotA, err := store.GetTaskByID(ctx, taskA.ID)
	if err != nil {
		t.Fatalf("GetTaskByID A: %v", err)
	}
	if gotA.Status != taskStatusPending || gotA.PRUrl.Valid {
		t.Errorf("[F203-03/2] task A status = %q pr_url = %+v, want pending with pr_url unset (rolled back with the failed batch)",
			gotA.Status, gotA.PRUrl)
	}

	gotB, err := store.GetTaskByID(ctx, taskB.ID)
	if err != nil {
		t.Fatalf("GetTaskByID B: %v", err)
	}
	if gotB.Status != taskStatusPending {
		t.Errorf("[F203-03/3] task B status = %q, want pending (its audit INSERT failed)", gotB.Status)
	}

	var auditRowsA int
	const countA = `SELECT COUNT(*) FROM activity_log WHERE action = 'pr_auto_close' AND notes LIKE '%' || ?1 || '%'`
	if err := d.QueryRowContext(ctx, countA, prURLA).Scan(&auditRowsA); err != nil {
		t.Fatalf("count pr_auto_close rows for A: %v", err)
	}
	if auditRowsA != 0 {
		t.Errorf("[F203-03/4] activity_log has %d pr_auto_close row(s) for task A's PR, want 0 (rolled back with the failed batch)", auditRowsA)
	}
}
