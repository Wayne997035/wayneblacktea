package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openLearningStore(t *testing.T, dsn, workspaceID string) *sqlite.LearningStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), dsn, workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewLearningStore(d)
}

func TestLearningStore_CreateConceptAndDueReviewRoundTrip(t *testing.T) {
	s := openLearningStore(t, ":memory:", "")
	concept, err := s.CreateConcept(context.Background(), "FSRS", "spaced repetition", []string{"memory", "cards"})
	if err != nil {
		t.Fatalf("CreateConcept: %v", err)
	}
	if concept.Title != "FSRS" || len(concept.Tags) != 2 {
		t.Fatalf("unexpected concept: %+v", concept)
	}

	reviews, err := s.DueReviews(context.Background(), 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	if len(reviews) != 1 || reviews[0].ConceptID != concept.ID || reviews[0].ScheduleID == uuid.Nil {
		t.Fatalf("unexpected due reviews: %+v", reviews)
	}
}

func TestLearningStore_NilTagsBecomeEmptySlice(t *testing.T) {
	s := openLearningStore(t, ":memory:", "")
	concept, err := s.CreateConcept(context.Background(), "No tags", "content", nil)
	if err != nil {
		t.Fatalf("CreateConcept: %v", err)
	}
	if concept.Tags == nil || len(concept.Tags) != 0 {
		t.Fatalf("expected empty tags slice, got %#v", concept.Tags)
	}
}

func TestLearningStore_EmptyTable(t *testing.T) {
	s := openLearningStore(t, ":memory:", "")
	reviews, err := s.DueReviews(context.Background(), 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	if len(reviews) != 0 {
		t.Fatalf("expected no due reviews, got %+v", reviews)
	}
	count, err := s.CountDueReviews(context.Background())
	if err != nil {
		t.Fatalf("CountDueReviews: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected count=0, got %d", count)
	}
}

func TestLearningStore_DueReviewPastVsFuture(t *testing.T) {
	s := openLearningStore(t, ":memory:", "")
	if _, err := s.CreateConcept(context.Background(), "past", "due now", nil); err != nil {
		t.Fatalf("CreateConcept past: %v", err)
	}
	futureConcept, err := s.CreateConcept(context.Background(), "future", "review later", nil)
	if err != nil {
		t.Fatalf("CreateConcept future: %v", err)
	}
	reviews, err := s.DueReviews(context.Background(), 10)
	if err != nil {
		t.Fatalf("DueReviews initial: %v", err)
	}
	futureReview := findDueReview(t, reviews, futureConcept.ID)
	if err := s.SubmitReview(context.Background(), futureReview.ScheduleID, learning.CardState{
		Stability: futureReview.Stability, Difficulty: futureReview.Difficulty, ReviewCount: futureReview.ReviewCount,
	}, learning.Good); err != nil {
		t.Fatalf("SubmitReview future: %v", err)
	}

	reviews, err = s.DueReviews(context.Background(), 10)
	if err != nil {
		t.Fatalf("DueReviews after submit: %v", err)
	}
	if len(reviews) != 1 || reviews[0].Title != "past" {
		t.Fatalf("expected only past review to remain due, got %+v", reviews)
	}
	if !reviews[0].DueDate.Before(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("remaining review should be due now/past, got %s", reviews[0].DueDate)
	}
}

func TestLearningStore_SubmitReviewUpdatesCountAndNotFound(t *testing.T) {
	s := openLearningStore(t, ":memory:", "")
	if _, err := s.CreateConcept(context.Background(), "submit", "content", nil); err != nil {
		t.Fatalf("CreateConcept: %v", err)
	}
	reviews, err := s.DueReviews(context.Background(), 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	r := reviews[0]
	if err := s.SubmitReview(context.Background(), r.ScheduleID, learning.CardState{
		Stability: r.Stability, Difficulty: r.Difficulty, ReviewCount: r.ReviewCount,
	}, learning.Easy); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	count, err := s.CountDueReviews(context.Background())
	if err != nil {
		t.Fatalf("CountDueReviews: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no due reviews after submit, got %d", count)
	}
	if err := s.SubmitReview(context.Background(), uuid.New(), learning.CardState{}, learning.Good); !errors.Is(err, learning.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing schedule, got %v", err)
	}
}

func TestLearningStore_WorkspaceIsolation(t *testing.T) {
	wsA, wsB := uuid.New().String(), uuid.New().String()
	dsn := "file:learning-" + uuid.New().String() + "?mode=memory&cache=shared"
	storeA := openLearningStore(t, dsn, wsA)
	storeB := openLearningStore(t, dsn, wsB)

	if _, err := storeA.CreateConcept(context.Background(), "Only A", "content", nil); err != nil {
		t.Fatalf("CreateConcept A: %v", err)
	}
	reviewsB, err := storeB.DueReviews(context.Background(), 10)
	if err != nil {
		t.Fatalf("DueReviews B: %v", err)
	}
	if len(reviewsB) != 0 {
		t.Fatalf("workspace B should not see A reviews: %+v", reviewsB)
	}
}

func TestLearningStore_ContextCanceled(t *testing.T) {
	s := openLearningStore(t, ":memory:", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.CreateConcept(ctx, "cancel", "content", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func findDueReview(t *testing.T, reviews []learning.DueReview, conceptID uuid.UUID) learning.DueReview {
	t.Helper()
	for _, r := range reviews {
		if r.ConceptID == conceptID {
			return r
		}
	}
	t.Fatalf("missing due review for concept %s in %+v", conceptID, reviews)
	return learning.DueReview{}
}
