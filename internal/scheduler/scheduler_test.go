package scheduler

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
)

// --- learning.StoreIface stub ---

type stubLearningStore struct {
	forAIReviewConcepts []learning.ConceptForReview
	forAIReviewErr      error

	updatedIDs      []uuid.UUID
	updatedStatuses []string
	updateErr       error

	dueCount    int
	dueCountErr error
}

func (s *stubLearningStore) CreateConcept(_ context.Context, _, _ string, _ []string) (*db.Concept, error) {
	return &db.Concept{}, nil
}

func (s *stubLearningStore) DueReviews(_ context.Context, _ int) ([]learning.DueReview, error) {
	return nil, nil
}

func (s *stubLearningStore) SubmitReview(_ context.Context, _ uuid.UUID, _ learning.CardState, _ learning.Rating) error {
	return nil
}

func (s *stubLearningStore) CountDueReviews(_ context.Context) (int, error) {
	return s.dueCount, s.dueCountErr
}

func (s *stubLearningStore) ListForAIReview(_ context.Context, _ int) ([]learning.ConceptForReview, error) {
	return s.forAIReviewConcepts, s.forAIReviewErr
}

func (s *stubLearningStore) UpdateConceptStatus(_ context.Context, id uuid.UUID, status string) error {
	s.updatedIDs = append(s.updatedIDs, id)
	s.updatedStatuses = append(s.updatedStatuses, status)
	return s.updateErr
}

func (s *stubLearningStore) ReviewHistory(_ context.Context) ([]learning.ConceptHistoryRow, error) {
	return nil, nil
}

func (s *stubLearningStore) LearningStats(_ context.Context) (*learning.LearningStatsResult, error) {
	return &learning.LearningStatsResult{}, nil
}

func (s *stubLearningStore) ListConcepts(_ context.Context, _ int) ([]db.Concept, error) {
	return nil, nil
}

func (s *stubLearningStore) ReviewedSince(_ context.Context, _ time.Time, _ int) ([]learning.DueReview, error) {
	return nil, nil
}

// --- ai.ConceptReviewerIface stub ---

type stubReviewer struct {
	results []ai.ReviewResult
}

func (r *stubReviewer) ReviewConcepts(_ context.Context, _ []ai.ReviewInput) []ai.ReviewResult {
	return r.results
}

// --- helpers ---

func makeScheduler(t *testing.T, store learning.StoreIface, reviewer ai.ConceptReviewerIface) *Scheduler {
	t.Helper()
	sc, err := New(store, nil, nil, nil, reviewer, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return sc
}

// --- tests ---

func TestWeeklyAIConceptReview_NilReviewer_JobNotRegistered(t *testing.T) {
	store := &stubLearningStore{}
	// nil reviewer → New must succeed and NOT register the weekly AI job.
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() with nil reviewer error: %v", err)
	}
	// Scheduler should start/stop cleanly.
	sc.Start()
	time.Sleep(10 * time.Millisecond)
	sc.Stop()

	// UpdateConceptStatus must never have been called.
	if len(store.updatedIDs) != 0 {
		t.Errorf("expected no status updates when reviewer is nil, got %d", len(store.updatedIDs))
	}
}

func TestWeeklyAIConceptReview_ReviewerReturnsMastered(t *testing.T) {
	conceptID := uuid.New()
	store := &stubLearningStore{
		forAIReviewConcepts: []learning.ConceptForReview{
			{ID: conceptID, Title: "Goroutines", Content: "concurrent execution units", ReviewCount: 10, Stability: 9.5},
		},
	}
	reviewer := &stubReviewer{
		results: []ai.ReviewResult{
			{ID: conceptID, NewStatus: "mastered"},
		},
	}

	sc := makeScheduler(t, store, reviewer)

	// Run the job directly (synchronous call, no scheduler tick needed).
	sc.weeklyAIConceptReview()

	if len(store.updatedIDs) != 1 {
		t.Fatalf("expected 1 UpdateConceptStatus call, got %d", len(store.updatedIDs))
	}
	if store.updatedIDs[0] != conceptID {
		t.Errorf("expected concept %s to be updated, got %s", conceptID, store.updatedIDs[0])
	}
	if store.updatedStatuses[0] != "mastered" {
		t.Errorf("expected status %q, got %q", "mastered", store.updatedStatuses[0])
	}
}

func TestWeeklyAIConceptReview_ReviewerReturnsNotHelpful(t *testing.T) {
	conceptID := uuid.New()
	store := &stubLearningStore{
		forAIReviewConcepts: []learning.ConceptForReview{
			{ID: conceptID, Title: "Vague Concept", Content: "too vague to be useful", ReviewCount: 8, Stability: 1.2},
		},
	}
	reviewer := &stubReviewer{
		results: []ai.ReviewResult{
			{ID: conceptID, NewStatus: "not_helpful"},
		},
	}

	sc := makeScheduler(t, store, reviewer)
	sc.weeklyAIConceptReview()

	if len(store.updatedIDs) != 1 {
		t.Fatalf("expected 1 UpdateConceptStatus call, got %d", len(store.updatedIDs))
	}
	if store.updatedStatuses[0] != "not_helpful" {
		t.Errorf("expected status %q, got %q", "not_helpful", store.updatedStatuses[0])
	}
}

func TestWeeklyAIConceptReview_ReviewerReturnsActive_NoUpdate(t *testing.T) {
	conceptID := uuid.New()
	store := &stubLearningStore{
		forAIReviewConcepts: []learning.ConceptForReview{
			{ID: conceptID, Title: "Still learning", Content: "need more review", ReviewCount: 6, Stability: 3.0},
		},
	}
	reviewer := &stubReviewer{
		results: []ai.ReviewResult{
			{ID: conceptID, NewStatus: "active"},
		},
	}

	sc := makeScheduler(t, store, reviewer)
	sc.weeklyAIConceptReview()

	// Status "active" means no change → UpdateConceptStatus must NOT be called.
	if len(store.updatedIDs) != 0 {
		t.Errorf("expected 0 UpdateConceptStatus calls for active status, got %d", len(store.updatedIDs))
	}
}

func TestWeeklyAIConceptReview_EmptyResults_NoUpdate(t *testing.T) {
	conceptID := uuid.New()
	store := &stubLearningStore{
		forAIReviewConcepts: []learning.ConceptForReview{
			{ID: conceptID, Title: "Some Concept", Content: "content", ReviewCount: 7, Stability: 4.0},
		},
	}
	// Reviewer returns empty slice (simulates API error / parse failure).
	reviewer := &stubReviewer{results: nil}

	sc := makeScheduler(t, store, reviewer)
	sc.weeklyAIConceptReview()

	if len(store.updatedIDs) != 0 {
		t.Errorf("expected 0 UpdateConceptStatus calls on empty reviewer results, got %d", len(store.updatedIDs))
	}
}

func TestWeeklyAIConceptReview_NoEligibleConcepts_NoCallToReviewer(t *testing.T) {
	store := &stubLearningStore{
		forAIReviewConcepts: nil, // no concepts eligible
	}
	// reviewer would panic if called — but it must not be called.
	reviewer := &stubReviewer{results: []ai.ReviewResult{{ID: uuid.New(), NewStatus: "mastered"}}}

	sc := makeScheduler(t, store, reviewer)
	sc.weeklyAIConceptReview()

	if len(store.updatedIDs) != 0 {
		t.Errorf("expected 0 updates when no eligible concepts, got %d", len(store.updatedIDs))
	}
}

func TestWeeklyAIConceptReview_ListForAIReviewError_NoUpdate(t *testing.T) {
	store := &stubLearningStore{
		forAIReviewErr: errStoreFailure,
	}
	reviewer := &stubReviewer{}

	sc := makeScheduler(t, store, reviewer)
	sc.weeklyAIConceptReview() // must not panic

	if len(store.updatedIDs) != 0 {
		t.Errorf("expected 0 updates when ListForAIReview errors, got %d", len(store.updatedIDs))
	}
}

var errStoreFailure = &storeError{msg: "simulated store failure"}

type storeError struct{ msg string }

func (e *storeError) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// sendDailyReviewReminder tests
// ---------------------------------------------------------------------------

type stubDiscordSender struct {
	called   bool
	message  string
	sendErr  error
	onSendFn func()
}

func (s *stubDiscordSender) Send(_ context.Context, message string) error {
	s.called = true
	s.message = message
	if s.onSendFn != nil {
		s.onSendFn()
	}
	return s.sendErr
}

// TestSendDailyReviewReminder_SendsCorrectCount verifies that the reminder
// message includes the number returned by CountDueReviews.
func TestSendDailyReviewReminder_SendsCorrectCount(t *testing.T) {
	store := &stubLearningStore{dueCount: 7}
	dc := &stubDiscordSender{}

	sc, err := New(store, dc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	sc.sendDailyReviewReminder()

	if !strings.Contains(dc.message, "7") {
		t.Errorf("reminder message should contain due count 7, message: %q", dc.message)
	}
}

// TestSendDailyReviewReminder_NilDiscord verifies that when the discord client
// is nil (DISCORD_WEBHOOK_URL unset), the function returns without panicking.
func TestSendDailyReviewReminder_NilDiscord(t *testing.T) {
	store := &stubLearningStore{dueCount: 3}
	// Pass nil discord client — must not panic.
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	sc.sendDailyReviewReminder() // must not panic
}

// TestSendDailyReviewReminder_CountError verifies that a CountDueReviews failure
// is handled gracefully — no Discord call is made and the function does not panic.
func TestSendDailyReviewReminder_CountError(t *testing.T) {
	store := &stubLearningStore{dueCountErr: errStoreFailure}
	dc := &stubDiscordSender{}

	sc, err := New(store, dc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	sc.sendDailyReviewReminder() // must not panic

	if dc.called {
		t.Error("discord should NOT be called when CountDueReviews returns an error")
	}
}

// TestSendDailyReviewReminder_ZeroDue verifies that when there are zero due
// reviews the message is still sent (informational ping, not just when non-zero).
func TestSendDailyReviewReminder_ZeroDue(t *testing.T) {
	store := &stubLearningStore{dueCount: 0}
	dc := &stubDiscordSender{}

	sc, err := New(store, dc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	sc.sendDailyReviewReminder()

	if !dc.called {
		t.Error("discord should be called even when due count is 0")
	}
}

// ---------------------------------------------------------------------------
// runDailyDisciplinePrune tests
// ---------------------------------------------------------------------------

// TestRunDailyDisciplinePrune_NilPool_NoPanic verifies the runner short-
// circuits cleanly when no Postgres pool is wired in (SQLite mode), without
// touching the DB or panicking. Mirrors runDailyDecayPrune's nil-pruner
// guard.
func TestRunDailyDisciplinePrune_NilPool_NoPanic(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// Pool is nil → must short-circuit without panicking.
	sc.runDailyDisciplinePrune()
}

// TestRegisterDailyDisciplinePrune_NilPool_JobNotRegistered verifies that
// when no Postgres pool is provided the daily-discipline-prune job is NOT
// registered with gocron (matching the daily-decay-prune skip pattern).
// gocron.Jobs() returns the registered jobs; we look for our name and assert
// absence.
func TestRegisterDailyDisciplinePrune_NilPool_JobNotRegistered(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	for _, j := range sc.s.Jobs() {
		if j.Name() == "daily-discipline-prune" {
			t.Errorf("daily-discipline-prune was registered with nil pool; expected skip")
		}
	}
}

// ---------------------------------------------------------------------------
// runDailyPendingProposalsPrune tests
// ---------------------------------------------------------------------------

// TestRunDailyPendingProposalsPrune_NilPool_NoPanic verifies the runner short-
// circuits cleanly when no Postgres pool is wired in (SQLite mode), without
// touching the DB or panicking. Mirrors the discipline-prune nil-pool guard
// (backend-security-design.md §1.3 — TTL is Postgres-only because SQLite is
// dev-local single-tenant).
func TestRunDailyPendingProposalsPrune_NilPool_NoPanic(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// Pool is nil → must short-circuit without panicking.
	sc.runDailyPendingProposalsPrune()
}

// TestRegisterDailyPendingProposalsPrune_NilPool_JobNotRegistered verifies
// that when no Postgres pool is provided the daily-pending-proposals-prune
// job is NOT registered with gocron — same skip semantics as the discipline /
// decay prune jobs.
func TestRegisterDailyPendingProposalsPrune_NilPool_JobNotRegistered(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	for _, j := range sc.s.Jobs() {
		if j.Name() == dailyPendingProposalsPruneJobName {
			t.Errorf("daily-pending-proposals-prune was registered with nil pool; expected skip")
		}
	}
}

// ---------------------------------------------------------------------------
// PrunerSpec / WithPruner / runPrune tests (table-driven registry factory
// replacing the 11 bespoke With*Pruner methods + 9 narrow interfaces).
// ---------------------------------------------------------------------------

// stubPrunerStore is a generic PrunerStore double shared by every WithPruner
// / runPrune test below — the whole point of the registry factory is that
// every table's runtime behavior funnels through the same code path, so one
// stub suffices instead of one per table.
type stubPrunerStore struct {
	called    bool
	gotCutoff time.Time
	n         int64
	err       error
}

func (s *stubPrunerStore) PruneOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	s.called = true
	s.gotCutoff = cutoff
	return s.n, s.err
}

// captureSlog swaps the process-global slog default logger for a text
// handler writing to a buffer, restoring the original on test cleanup. Not
// safe for use by t.Parallel() tests (slog.SetDefault is process-global);
// none of the tests in this file opt into parallelism.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// TestWithPruner_RegistersJob verifies WithPruner registers a gocron job
// named "daily-<Name>-prune" for an arbitrary spec — the generalized
// replacement for the 11 per-table "daily-<table>-prune job found" checks.
func TestWithPruner_RegistersJob(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	spec := PrunerSpec{
		Name:      "widget",
		Store:     &stubPrunerStore{n: 4},
		Retention: 42 * 24 * time.Hour,
		Hour:      5,
		Minute:    15,
	}
	if err := sc.WithPruner(spec); err != nil {
		t.Fatalf("WithPruner() error: %v", err)
	}

	found := false
	for _, j := range sc.s.Jobs() {
		if j.Name() == "daily-widget-prune" {
			found = true
			break
		}
	}
	if !found {
		t.Error("daily-widget-prune job not found after WithPruner")
	}
}

// TestWithPruner_NotCalled_JobNotRegistered verifies the "store not
// configured -> skip" contract every daily prune job has always had. Under
// the registry factory the nil-guard lives entirely in the caller (mirroring
// cmd/server/main.go's `if store != nil { sched.WithPruner(...) }` pattern —
// see acceptance criterion (a) "nil store 時 no-op"): when WithPruner is
// never invoked for a table, its job is simply absent, and nothing panics.
func TestWithPruner_NotCalled_JobNotRegistered(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	for _, j := range sc.s.Jobs() {
		if j.Name() == "daily-widget-prune" {
			t.Error("daily-widget-prune should not be registered when WithPruner was never called")
		}
	}
}

// TestRunPrune_Success_DelegatesAndLogsRowsDeleted verifies the (b) success
// path: runPrune calls Store.PruneOlderThan with a cutoff derived from
// spec.Retention and logs rows_deleted + retention_days.
func TestRunPrune_Success_DelegatesAndLogsRowsDeleted(t *testing.T) {
	buf := captureSlog(t)
	pruner := &stubPrunerStore{n: 7}
	spec := PrunerSpec{
		Name:      "widget",
		Store:     pruner,
		Retention: 30 * 24 * time.Hour,
	}

	runPrune(spec)

	if !pruner.called {
		t.Fatal("PruneOlderThan was not called")
	}
	wantCutoff := time.Now().Add(-spec.Retention)
	if diff := pruner.gotCutoff.Sub(wantCutoff); diff < -time.Minute || diff > time.Minute {
		t.Errorf("PruneOlderThan cutoff = %v, want ~%v", pruner.gotCutoff, wantCutoff)
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "daily widget prune: completed") {
		t.Errorf("log output missing completion message, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "rows_deleted=7") {
		t.Errorf("log output missing rows_deleted=7, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "retention_days=30") {
		t.Errorf("log output missing retention_days=30, got: %s", logOutput)
	}
}

// TestRunPrune_Error_LogsWarnNoPanic verifies (c): a PruneOlderThan error is
// swallowed (logged at warn level, not propagated or panicked) — matches the
// non-fatal error-handling convention every prune job had before the
// refactor.
func TestRunPrune_Error_LogsWarnNoPanic(t *testing.T) {
	buf := captureSlog(t)
	pruner := &stubPrunerStore{err: context.DeadlineExceeded}
	spec := PrunerSpec{
		Name:      "widget",
		Store:     pruner,
		Retention: 30 * 24 * time.Hour,
	}

	runPrune(spec) // must not panic

	if !pruner.called {
		t.Fatal("PruneOlderThan was not called")
	}
	logOutput := buf.String()
	if !strings.Contains(logOutput, "level=WARN") {
		t.Errorf("expected a WARN-level log line, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, "daily widget prune: PruneOlderThan failed") {
		t.Errorf("log output missing failure message, got: %s", logOutput)
	}
}

// ---------------------------------------------------------------------------
// candidatePrunerAdapter / mergedPRsPrunerAdapter tests — the two stores
// whose native prune method takes a retention time.Duration rather than a
// cutoff time.Time, bridged to the unified PrunerStore contract.
// ---------------------------------------------------------------------------

type stubCandidatePruner struct {
	called      bool
	gotDuration time.Duration
	n           int64
	err         error
}

func (s *stubCandidatePruner) PruneResolved(_ context.Context, d time.Duration) (int64, error) {
	s.called = true
	s.gotDuration = d
	return s.n, s.err
}

// TestCandidatePrunerAdapter_DelegatesToPruneResolvedWithDuration verifies
// NewCandidatePrunerAdapter converts a cutoff back into a retention duration
// and delegates to PruneResolved.
func TestCandidatePrunerAdapter_DelegatesToPruneResolvedWithDuration(t *testing.T) {
	pruner := &stubCandidatePruner{n: 3}
	adapter := NewCandidatePrunerAdapter(pruner)

	retention := 30 * 24 * time.Hour
	cutoff := time.Now().Add(-retention)
	n, err := adapter.PruneOlderThan(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan() error: %v", err)
	}
	if n != 3 {
		t.Errorf("PruneOlderThan() n = %d, want 3", n)
	}
	if !pruner.called {
		t.Fatal("PruneResolved was not called")
	}
	if diff := pruner.gotDuration - retention; diff < -time.Second || diff > time.Second {
		t.Errorf("PruneResolved got duration %v, want ~%v", pruner.gotDuration, retention)
	}
}

type stubMergedPRsPruner struct {
	called      bool
	gotDuration time.Duration
	n           int64
	err         error
}

func (s *stubMergedPRsPruner) PruneOlderThan(_ context.Context, d time.Duration) (int64, error) {
	s.called = true
	s.gotDuration = d
	return s.n, s.err
}

// TestMergedPRsPrunerAdapter_DelegatesToPruneOlderThanWithDuration verifies
// NewMergedPRsPrunerAdapter converts a cutoff back into a retention duration
// and delegates to the store's duration-based PruneOlderThan.
func TestMergedPRsPrunerAdapter_DelegatesToPruneOlderThanWithDuration(t *testing.T) {
	pruner := &stubMergedPRsPruner{n: 5}
	adapter := NewMergedPRsPrunerAdapter(pruner)

	retention := 30 * 24 * time.Hour
	cutoff := time.Now().Add(-retention)
	n, err := adapter.PruneOlderThan(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan() error: %v", err)
	}
	if n != 5 {
		t.Errorf("PruneOlderThan() n = %d, want 5", n)
	}
	if !pruner.called {
		t.Fatal("PruneOlderThan was not called")
	}
	if diff := pruner.gotDuration - retention; diff < -time.Second || diff > time.Second {
		t.Errorf("PruneOlderThan got duration %v, want ~%v", pruner.gotDuration, retention)
	}
}
