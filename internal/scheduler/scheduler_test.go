package scheduler

import (
	"context"
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
	return 0, nil
}

func (s *stubLearningStore) ListForAIReview(_ context.Context, _ int) ([]learning.ConceptForReview, error) {
	return s.forAIReviewConcepts, s.forAIReviewErr
}

func (s *stubLearningStore) UpdateConceptStatus(_ context.Context, id uuid.UUID, status string) error {
	s.updatedIDs = append(s.updatedIDs, id)
	s.updatedStatuses = append(s.updatedStatuses, status)
	return s.updateErr
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
	sc, err := New(store, nil, nil, nil, reviewer, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return sc
}

// --- tests ---

func TestWeeklyAIConceptReview_NilReviewer_JobNotRegistered(t *testing.T) {
	store := &stubLearningStore{}
	// nil reviewer → New must succeed and NOT register the weekly AI job.
	sc, err := New(store, nil, nil, nil, nil, nil, nil, nil, nil)
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
