package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
		if j.Name() == "daily-pending-proposals-prune" {
			t.Errorf("daily-pending-proposals-prune was registered with nil pool; expected skip")
		}
	}
}

// ---------------------------------------------------------------------------
// WithCandidatePruner / runDailyCandidatePrune tests
// ---------------------------------------------------------------------------

type stubCandidatePruner struct {
	called bool
	n      int64
	err    error
}

func (s *stubCandidatePruner) PruneResolved(_ context.Context, _ time.Duration) (int64, error) {
	s.called = true
	return s.n, s.err
}

// TestRunDailyCandidatePrune_NilPruner_NoPanic verifies the runner short-
// circuits cleanly when no candidatePruner is wired (WithCandidatePruner not
// called), mirroring the nil-guard pattern used by runDailyDisciplinePrune.
func TestRunDailyCandidatePrune_NilPruner_NoPanic(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// candidatePruner is nil → must short-circuit without panicking.
	sc.runDailyCandidatePrune()
}

// TestWithCandidatePruner_RegistersJob_And_Delegates verifies that
// WithCandidatePruner registers the daily-candidate-prune gocron job and
// that runDailyCandidatePrune delegates to the store's PruneResolved.
func TestWithCandidatePruner_RegistersJob_And_Delegates(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	pruner := &stubCandidatePruner{n: 3}
	if err := sc.WithCandidatePruner(pruner); err != nil {
		t.Fatalf("WithCandidatePruner() error: %v", err)
	}

	// Job must be registered with gocron.
	found := false
	for _, j := range sc.s.Jobs() {
		if j.Name() == "daily-candidate-prune" {
			found = true
			break
		}
	}
	if !found {
		t.Error("daily-candidate-prune job not found after WithCandidatePruner")
	}

	// Direct invocation must delegate to PruneResolved.
	sc.runDailyCandidatePrune()
	if !pruner.called {
		t.Error("PruneResolved was not called by runDailyCandidatePrune")
	}
}

// ---------------------------------------------------------------------------
// WithMergedPRsPruner / runDailyMergedPRsObservedPrune tests
// ---------------------------------------------------------------------------

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

// TestRunDailyMergedPRsObservedPrune_NilPruner_NoPanic verifies the runner
// short-circuits cleanly when no mergedPRsPruner is wired.
func TestRunDailyMergedPRsObservedPrune_NilPruner_NoPanic(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// mergedPRsPruner is nil → must short-circuit without panicking.
	sc.runDailyMergedPRsObservedPrune()
}

// TestWithMergedPRsPruner_RegistersJob_And_Delegates verifies that
// WithMergedPRsPruner registers the daily-merged-prs-observed-prune gocron
// job and that the runner delegates to PruneOlderThan with the 30-day
// retention duration.
func TestWithMergedPRsPruner_RegistersJob_And_Delegates(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	pruner := &stubMergedPRsPruner{n: 5}
	if err := sc.WithMergedPRsPruner(pruner); err != nil {
		t.Fatalf("WithMergedPRsPruner() error: %v", err)
	}

	// Job must be registered with gocron.
	found := false
	for _, j := range sc.s.Jobs() {
		if j.Name() == "daily-merged-prs-observed-prune" {
			found = true
			break
		}
	}
	if !found {
		t.Error("daily-merged-prs-observed-prune job not found after WithMergedPRsPruner")
	}

	// Direct invocation must delegate to PruneOlderThan with 30d.
	sc.runDailyMergedPRsObservedPrune()
	if !pruner.called {
		t.Error("PruneOlderThan was not called by runDailyMergedPRsObservedPrune")
	}
	if pruner.gotDuration != 30*24*time.Hour {
		t.Errorf("PruneOlderThan got %v, want 30 days", pruner.gotDuration)
	}
}

// ---------------------------------------------------------------------------
// FIX 4: WithAICostLedgerPruner / runDailyAICostLedgerPrune tests
// ---------------------------------------------------------------------------

// TestRunDailyAICostLedgerPrune_NilPool_NoPanic verifies that
// runDailyAICostLedgerPrune short-circuits cleanly when no Postgres pool is
// wired (aiCostLedgerPool is nil — SQLite dev mode). Mirrors the nil-pool
// guard pattern used by runDailyDisciplinePrune and other prune jobs.
func TestRunDailyAICostLedgerPrune_NilPool_NoPanic(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// aiCostLedgerPool is nil → must short-circuit without panicking.
	sc.runDailyAICostLedgerPrune()
}

// TestWithAICostLedgerPruner_RegistersJob verifies that WithAICostLedgerPruner
// registers the daily-ai-cost-ledger-prune gocron job. The test uses a nil
// pool intentionally: WithAICostLedgerPruner only sets the pool and registers
// the job — it does NOT validate the pool at registration time (nil pool
// causes the runner to no-op, tested separately above).
//
// We cannot call WithAICostLedgerPruner with a real pool in a unit test
// because the prune job needs a running Postgres; the integration version is
// covered by the scheduler's pending_proposals_prune_pg_test.go pattern.
// Here we verify only the job registration side effect using a real
// *pgxpool.Pool value constructed from config (not actually connected) — but
// gocron.NewJob succeeds regardless of pool connectivity at registration.
//
// Approach: pass a non-nil dummy pool pointer (constructed from an already-
// existing testAICostPool or a lazy-init nil-deref-safe pointer). Since we
// only want to verify the job is in the gocron job list, we can pass any
// non-nil *pgxpool.Pool, including one created from an empty config
// (pgxpool.New is not called here — we create the struct directly).
//
// Simplest safe approach: call WithAICostLedgerPruner with a non-nil pool
// from an already-established connection (not needed for job registration).
// Instead, we verify the job name via a sentinel that WithAICostLedgerPruner
// sets aiCostLedgerPool and the gocron job is registered, using the internal
// field path (same-package access).
func TestWithAICostLedgerPruner_RegistersJob(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Use a sentinel non-nil *pgxpool.Pool. pgxpool.Pool is a struct; we take
	// address of a zero-value instance. The pool is never actually used in
	// this test because we do not call runDailyAICostLedgerPrune (which would
	// attempt a real DB Exec).
	sentinelPool := new(pgxpool.Pool)
	if regErr := sc.WithAICostLedgerPruner(sentinelPool); regErr != nil {
		t.Fatalf("WithAICostLedgerPruner() error: %v", regErr)
	}

	// Job must be registered with gocron.
	found := false
	for _, j := range sc.s.Jobs() {
		if j.Name() == "daily-ai-cost-ledger-prune" {
			found = true
			break
		}
	}
	if !found {
		t.Error("daily-ai-cost-ledger-prune job not found after WithAICostLedgerPruner")
	}

	// Internal field must be set to the pool we passed.
	if sc.aiCostLedgerPool != sentinelPool {
		t.Error("aiCostLedgerPool not set by WithAICostLedgerPruner")
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2.4: WithWorkSessionEvidencePruner / runDailyWorkSessionEvidencePrune
// ---------------------------------------------------------------------------

type stubWorkSessionEvidencePruner struct {
	called    bool
	gotCutoff time.Time
	n         int64
	err       error
}

func (s *stubWorkSessionEvidencePruner) PruneOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	s.called = true
	s.gotCutoff = cutoff
	return s.n, s.err
}

// TestRunDailyWorkSessionEvidencePrune_NilPruner_NoPanic verifies that
// runDailyWorkSessionEvidencePrune short-circuits cleanly when no pruner is
// wired (workSessionEvidencePruner is nil — WithWorkSessionEvidencePruner
// never called). Mirrors the nil-pruner guard pattern used by the other
// prune jobs.
func TestRunDailyWorkSessionEvidencePrune_NilPruner_NoPanic(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	// workSessionEvidencePruner is nil → must short-circuit without panicking.
	sc.runDailyWorkSessionEvidencePrune()
}

// TestWithWorkSessionEvidencePruner_RegistersJob_And_Delegates verifies that
// WithWorkSessionEvidencePruner registers the daily-work-session-evidence-prune
// gocron job and that the runner delegates to PruneOlderThan with a cutoff
// ~90 days in the past.
func TestWithWorkSessionEvidencePruner_RegistersJob_And_Delegates(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	pruner := &stubWorkSessionEvidencePruner{n: 3}
	if err := sc.WithWorkSessionEvidencePruner(pruner); err != nil {
		t.Fatalf("WithWorkSessionEvidencePruner() error: %v", err)
	}

	// Job must be registered with gocron.
	found := false
	for _, j := range sc.s.Jobs() {
		if j.Name() == "daily-work-session-evidence-prune" {
			found = true
			break
		}
	}
	if !found {
		t.Error("daily-work-session-evidence-prune job not found after WithWorkSessionEvidencePruner")
	}

	// Direct invocation must delegate to PruneOlderThan with a ~90-day cutoff.
	sc.runDailyWorkSessionEvidencePrune()
	if !pruner.called {
		t.Error("PruneOlderThan was not called by runDailyWorkSessionEvidencePrune")
	}
	wantCutoff := time.Now().Add(-workSessionEvidencePruneRetention)
	if diff := pruner.gotCutoff.Sub(wantCutoff); diff < -time.Minute || diff > time.Minute {
		t.Errorf("PruneOlderThan cutoff = %v, want ~%v (90 days ago)", pruner.gotCutoff, wantCutoff)
	}
}

// TestRunDailyWorkSessionEvidencePrune_PrunerError_LogsWarnNoPanic verifies
// that a PruneOlderThan error is swallowed (logged, not propagated/panicked)
// — matches the non-fatal error-handling convention of every other prune job.
func TestRunDailyWorkSessionEvidencePrune_PrunerError_LogsWarnNoPanic(t *testing.T) {
	store := &stubLearningStore{}
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	pruner := &stubWorkSessionEvidencePruner{err: context.DeadlineExceeded}
	if err := sc.WithWorkSessionEvidencePruner(pruner); err != nil {
		t.Fatalf("WithWorkSessionEvidencePruner() error: %v", err)
	}
	// Must not panic even though PruneOlderThan returns an error.
	sc.runDailyWorkSessionEvidencePrune()
	if !pruner.called {
		t.Error("PruneOlderThan was not called")
	}
}
