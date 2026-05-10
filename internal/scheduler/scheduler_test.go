package scheduler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/discord"
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
	sc, err := New(store, nil, nil, nil, reviewer, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return sc
}

// --- tests ---

func TestWeeklyAIConceptReview_NilReviewer_JobNotRegistered(t *testing.T) {
	store := &stubLearningStore{}
	// nil reviewer → New must succeed and NOT register the weekly AI job.
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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

// newTestDiscordClient builds a *discord.Client pointed at the given httptest
// server URL by temporarily setting DISCORD_WEBHOOK_URL in the environment.
// The t.Setenv call restores the original value when the test ends.
func newTestDiscordClient(t *testing.T, url string) *discord.Client {
	t.Helper()
	t.Setenv("DISCORD_WEBHOOK_URL", url)
	c := discord.NewClient()
	if c == nil {
		t.Fatal("discord.NewClient returned nil with DISCORD_WEBHOOK_URL set")
	}
	return c
}

// TestSendDailyReviewReminder_SendsCorrectCount verifies that the reminder
// message includes the number returned by CountDueReviews.
func TestSendDailyReviewReminder_SendsCorrectCount(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1024))
		receivedBody = string(b)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	store := &stubLearningStore{dueCount: 7}
	dc := newTestDiscordClient(t, srv.URL)

	sc, err := New(store, dc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	sc.sendDailyReviewReminder()

	if !strings.Contains(receivedBody, "7") {
		t.Errorf("reminder message should contain due count 7, body: %q", receivedBody)
	}
}

// TestSendDailyReviewReminder_NilDiscord verifies that when the discord client
// is nil (DISCORD_WEBHOOK_URL unset), the function returns without panicking.
func TestSendDailyReviewReminder_NilDiscord(t *testing.T) {
	store := &stubLearningStore{dueCount: 3}
	// Pass nil discord client — must not panic.
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	sc.sendDailyReviewReminder() // must not panic
}

// TestSendDailyReviewReminder_CountError verifies that a CountDueReviews failure
// is handled gracefully — no Discord call is made and the function does not panic.
func TestSendDailyReviewReminder_CountError(t *testing.T) {
	discordCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		discordCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	store := &stubLearningStore{dueCountErr: errStoreFailure}
	dc := newTestDiscordClient(t, srv.URL)

	sc, err := New(store, dc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	sc.sendDailyReviewReminder() // must not panic

	if discordCalled {
		t.Error("discord should NOT be called when CountDueReviews returns an error")
	}
}

// TestSendDailyReviewReminder_ZeroDue verifies that when there are zero due
// reviews the message is still sent (informational ping, not just when non-zero).
func TestSendDailyReviewReminder_ZeroDue(t *testing.T) {
	discordCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		discordCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	store := &stubLearningStore{dueCount: 0}
	dc := newTestDiscordClient(t, srv.URL)

	sc, err := New(store, dc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	sc.sendDailyReviewReminder()

	if !discordCalled {
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
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	for _, j := range sc.s.Jobs() {
		if j.Name() == "daily-pending-proposals-prune" {
			t.Errorf("daily-pending-proposals-prune was registered with nil pool; expected skip")
		}
	}
}
