package scheduler

// Unit coverage for the SQLite dispatch path of daily-pending-proposals-prune
// (GTD 80cf80b6, PR #152 2nd-army finding). Complements the storage-layer
// tests in internal/storage/sqlite/proposal_prune_test.go, which exercise
// MarkAndDeleteStaleProposals's actual SQL against a real :memory: DB — these
// tests instead pin the scheduler-level wiring/dispatch contract with a
// stub, without needing a DB at all.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// stubPendingProposalsPruneStore records every MarkAndDeleteStaleProposals
// call so tests can assert the scheduler passed the correct retention
// durations and reason string.
type stubPendingProposalsPruneStore struct {
	calls                             int
	gotTask, gotDecision, gotResolved time.Duration
	gotReason                         string
	marked, deleted                   int64
	err                               error
}

func (s *stubPendingProposalsPruneStore) MarkAndDeleteStaleProposals(
	_ context.Context, taskRetention, decisionRetention, resolvedRetention time.Duration, markReason string,
) (int64, int64, error) {
	s.calls++
	s.gotTask, s.gotDecision, s.gotResolved = taskRetention, decisionRetention, resolvedRetention
	s.gotReason = markReason
	return s.marked, s.deleted, s.err
}

// TestRunDailyPendingProposalsPrune_SQLite_Dispatches verifies that when
// disciplinePool is nil and pendingProposalsPruneSQLite is set, the run
// method dispatches to the SQLite store with the same retention windows
// documented on the PG constants (pendingProposalsPending{Task,Decision}
// Retention / pendingProposalsResolvedRetention), not ad-hoc values.
func TestRunDailyPendingProposalsPrune_SQLite_Dispatches(t *testing.T) {
	store := &stubPendingProposalsPruneStore{marked: 2, deleted: 5}
	sc := &Scheduler{pendingProposalsPruneSQLite: store}
	sc.runDailyPendingProposalsPrune()

	if store.calls != 1 {
		t.Fatalf("expected exactly 1 MarkAndDeleteStaleProposals call, got %d", store.calls)
	}
	if store.gotTask != pendingProposalsPendingTaskRetentionDuration {
		t.Errorf("task retention = %v, want %v", store.gotTask, pendingProposalsPendingTaskRetentionDuration)
	}
	if store.gotDecision != pendingProposalsPendingDecisionRetentionDuration {
		t.Errorf("decision retention = %v, want %v", store.gotDecision, pendingProposalsPendingDecisionRetentionDuration)
	}
	if store.gotResolved != pendingProposalsResolvedRetentionDuration {
		t.Errorf("resolved retention = %v, want %v", store.gotResolved, pendingProposalsResolvedRetentionDuration)
	}
	if store.gotReason != pendingProposalsTaskTTLReason {
		t.Errorf("mark reason = %q, want %q", store.gotReason, pendingProposalsTaskTTLReason)
	}
}

// TestRunDailyPendingProposalsPrune_SQLite_MarkErrors_LogsRowsDeleted is
// item 3's regression reproduction: MarkAndDeleteStaleProposals's own doc
// comment (internal/storage/sqlite/proposal.go) says a mark-step failure
// does NOT gate the delete step — the DELETE still runs and can genuinely
// remove rows even when the function's overall err is non-nil. Before this
// fix, the caller's `if err != nil` branch logged only marked_rows, so a
// destructive DELETE that actually ran left no record of how much it
// destroyed. The stub below returns a non-nil err together with a non-zero
// deleted count — exactly the shape MarkAndDeleteStaleProposals produces
// when mark fails but delete succeeds — and asserts rows_deleted appears in
// the resulting slog.Warn.
func TestRunDailyPendingProposalsPrune_SQLite_MarkErrors_LogsRowsDeleted(t *testing.T) {
	buf := captureSlog(t)
	store := &stubPendingProposalsPruneStore{
		marked: 0, deleted: 9,
		err: errors.New("stub mark step failure"),
	}
	sc := &Scheduler{pendingProposalsPruneSQLite: store}
	sc.runDailyPendingProposalsPrune()

	logOut := buf.String()
	if !strings.Contains(logOut, "mark/delete failed") {
		t.Fatalf("expected mark/delete failed warn, got log:\n%s", logOut)
	}
	if !strings.Contains(logOut, "rows_deleted=9") {
		t.Errorf("expected rows_deleted=9 in warn (delete succeeded despite mark error), got log:\n%s", logOut)
	}
}

// TestRunDailyPendingProposalsPrune_PGTakesPriorityOverSQLite verifies that
// when BOTH disciplinePool and pendingProposalsPruneSQLite happen to be set
// (should never happen in production — storage.ResolveFromEnv wires exactly
// one backend — but WithPendingProposalsPruneSQLite's own nil-store guard
// only protects the *registration* path, not this dispatch switch), the PG
// branch wins and the SQLite store is NOT called. Postgres is listed first
// in the switch specifically so a future accidental double-wire fails safe
// toward the more heavily-tested backend rather than silently going SQLite.
func TestRunDailyPendingProposalsPrune_PGTakesPriorityOverSQLite(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	store := &stubPendingProposalsPruneStore{}
	sc := &Scheduler{disciplinePool: pool, pendingProposalsPruneSQLite: store}
	sc.runDailyPendingProposalsPrune()

	if store.calls != 0 {
		t.Errorf("expected SQLite store NOT called when disciplinePool is set, got %d calls", store.calls)
	}
}

// TestWithPendingProposalsPruneSQLite_NoopWhenPGAlreadyWired verifies the
// registration-time guard: calling WithPendingProposalsPruneSQLite on a
// Scheduler whose disciplinePool is already set must NOT overwrite
// pendingProposalsPruneSQLite (registerDailyJobs already owns that job's PG
// registration; a second registration under the same gocron job name would
// be a caller bug).
func TestWithPendingProposalsPruneSQLite_NoopWhenPGAlreadyWired(t *testing.T) {
	pool := openSchedulerTestPgPool(t)
	sc := &Scheduler{disciplinePool: pool}
	store := &stubPendingProposalsPruneStore{}
	if err := sc.WithPendingProposalsPruneSQLite(store); err != nil {
		t.Fatalf("WithPendingProposalsPruneSQLite: %v", err)
	}
	if sc.pendingProposalsPruneSQLite != nil {
		t.Error("expected pendingProposalsPruneSQLite to stay nil when disciplinePool is already set")
	}
}

// TestWithPendingProposalsPruneSQLite_RegistersJob verifies the job actually
// gets registered under the same gocron name Postgres uses
// ("daily-pending-proposals-prune") when wired against a SQLite-only
// (disciplinePool-nil) Scheduler built via New().
func TestWithPendingProposalsPruneSQLite_RegistersJob(t *testing.T) {
	learningStore := &stubLearningStore{}
	sc, err := New(learningStore, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	store := &stubPendingProposalsPruneStore{}
	if err := sc.WithPendingProposalsPruneSQLite(store); err != nil {
		t.Fatalf("WithPendingProposalsPruneSQLite: %v", err)
	}
	if sc.pendingProposalsPruneSQLite == nil {
		t.Fatal("expected pendingProposalsPruneSQLite to be wired")
	}

	found := false
	for _, j := range sc.s.Jobs() {
		if j.Name() == dailyPendingProposalsPruneJobName {
			found = true
		}
	}
	if !found {
		t.Error("expected \"daily-pending-proposals-prune\" job to be registered")
	}
}

// TestRegisterDailyJobs_PendingProposalsPrune_SkipLogDoesNotClaimNoPrune is
// item 4's regression reproduction: with disciplinePool nil,
// registerDailyJobs's else-branch used to log "DailyPendingProposalsPrune
// skipped (postgres pool not configured; SQLite has no growth concern)" —
// an operator grepping for "skipped" would conclude no prune runs at all,
// contradicting the "scheduled at 03:00" line WithPendingProposalsPruneSQLite
// logs moments later once the SQLite store is wired. The "no growth concern"
// claim was also stale: this PR (pending_proposals's own TTL retention
// feature) is what gave SQLite pending_proposals a growth problem in the
// first place. The reworded message must name what was actually skipped
// (the Postgres registration only) and point at the SQLite counterpart.
func TestRegisterDailyJobs_PendingProposalsPrune_SkipLogDoesNotClaimNoPrune(t *testing.T) {
	buf := captureSlog(t)
	learningStore := &stubLearningStore{}
	if _, err := New(learningStore, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("New() error: %v", err)
	}

	logOut := buf.String()
	if !strings.Contains(logOut, "DailyPendingProposalsPrune Postgres registration skipped") {
		t.Errorf("expected the reworded Postgres-only skip message, got log:\n%s", logOut)
	}
	if !strings.Contains(logOut, "WithPendingProposalsPruneSQLite") {
		t.Errorf("expected the skip message to point at the SQLite counterpart, got log:\n%s", logOut)
	}
	if strings.Contains(logOut, "DailyPendingProposalsPrune skipped (postgres pool not configured; SQLite has no growth concern)") {
		t.Errorf("stale message must not appear: it falsely implies no prune runs at all, got log:\n%s", logOut)
	}
}

// TestWithPendingProposalsPruneSQLite_NilStoreSkips verifies the nil-store
// guard: passing a nil PendingProposalsPruneStore (the "SQLite DB not
// wired" case main.go's stores.SqliteDB() nil branch produces) must not
// register a job or panic.
func TestWithPendingProposalsPruneSQLite_NilStoreSkips(t *testing.T) {
	learningStore := &stubLearningStore{}
	sc, err := New(learningStore, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if err := sc.WithPendingProposalsPruneSQLite(nil); err != nil {
		t.Fatalf("WithPendingProposalsPruneSQLite(nil): %v", err)
	}
	if sc.pendingProposalsPruneSQLite != nil {
		t.Error("expected pendingProposalsPruneSQLite to stay nil")
	}
	for _, j := range sc.s.Jobs() {
		if j.Name() == dailyPendingProposalsPruneJobName {
			t.Error("expected no job registered when store is nil")
		}
	}
}
