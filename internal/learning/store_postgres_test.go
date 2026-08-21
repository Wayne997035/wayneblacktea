package learning_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// openTestPgPool returns the package-level singleton pool initialised in
// TestMain. Skip with -short: testcontainers requires Docker and adds ~5-10s.
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// TestStore_SubmitReview_HappyPath verifies that SubmitReview advances the
// review schedule: stability/difficulty move per the FSRS formula,
// review_count increments, and due_date moves into the future.
func TestStore_SubmitReview_HappyPath(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := learning.NewStore(pool, &wsID)
	ctx := context.Background()

	concept, err := store.CreateConcept(ctx, "spaced repetition", "review at increasing intervals", []string{"srs"})
	if err != nil {
		t.Fatalf("CreateConcept: %v", err)
	}

	due, err := store.DueReviews(ctx, 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	var scheduleID uuid.UUID
	for _, d := range due {
		if d.ConceptID == concept.ID {
			scheduleID = d.ScheduleID
		}
	}
	if scheduleID == uuid.Nil {
		t.Fatalf("expected a due review schedule for concept %s, found none in %+v", concept.ID, due)
	}

	initialState := learning.CardState{Stability: 1.0, Difficulty: 0.3, ReviewCount: 0}
	if err := store.SubmitReview(ctx, scheduleID, initialState, learning.Good); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}

	history, err := store.ReviewHistory(ctx)
	if err != nil {
		t.Fatalf("ReviewHistory: %v", err)
	}
	var found *learning.ConceptHistoryRow
	for i := range history {
		if history[i].ID == concept.ID {
			found = &history[i]
		}
	}
	if found == nil {
		t.Fatalf("expected concept %s in review history, found none in %d rows", concept.ID, len(history))
	}
	if found.ReviewCount != 1 {
		t.Errorf("ReviewCount: got %d, want 1", found.ReviewCount)
	}
	if found.LastReviewedAt == nil {
		t.Error("expected LastReviewedAt to be set after SubmitReview")
	}
}

// TestStore_GetScheduleState_MatchesDBRow verifies GetScheduleState reads
// back exactly what SubmitReview wrote (Ω7 fix — the MCP handler now uses
// this instead of trusting caller-supplied stability/difficulty/
// review_count).
func TestStore_GetScheduleState_MatchesDBRow(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := learning.NewStore(pool, &wsID)
	ctx := context.Background()

	concept, err := store.CreateConcept(ctx, "get schedule state", "verify read-back", []string{"srs"})
	if err != nil {
		t.Fatalf("CreateConcept: %v", err)
	}
	due, err := store.DueReviews(ctx, 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	var scheduleID uuid.UUID
	for _, d := range due {
		if d.ConceptID == concept.ID {
			scheduleID = d.ScheduleID
		}
	}
	if scheduleID == uuid.Nil {
		t.Fatalf("expected a due review schedule for concept %s", concept.ID)
	}

	// Advance the schedule several times so review_count > 0 and stability
	// has moved off its 1.0 default — a "mature" schedule.
	state := learning.CardState{Stability: 1.0, Difficulty: 0.3, ReviewCount: 0}
	for range 3 {
		if err := store.SubmitReview(ctx, scheduleID, state, learning.Good); err != nil {
			t.Fatalf("SubmitReview: %v", err)
		}
		got, err := store.GetScheduleState(ctx, scheduleID)
		if err != nil {
			t.Fatalf("GetScheduleState: %v", err)
		}
		state = got
	}

	if state.ReviewCount != 3 {
		t.Errorf("ReviewCount after 3 reviews: got %d, want 3", state.ReviewCount)
	}
	if state.Stability <= 0 {
		t.Errorf("Stability: got %v, want > 0", state.Stability)
	}
}

// TestStore_GetScheduleState_NotFound verifies ErrNotFound for an unknown
// schedule ID.
func TestStore_GetScheduleState_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := learning.NewStore(pool, &wsID)
	ctx := context.Background()

	_, err := store.GetScheduleState(ctx, uuid.New())
	if err != learning.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestStore_DueReviews_EmptyReturnsEmptyArrayNotNull is the Postgres-side
// regression lock for the empty-list-must-be-[]-not-null contract: DueReviews
// already builds reviews := make([]DueReview, 0, len(rows)) (store.go:93), so
// this test should always pass — it exists so a future edit that swaps that
// make() call for a bare `var reviews []DueReview` declaration is caught here
// instead of only being caught on the SQLite side (which had exactly that bug
// until this same change — see internal/storage/sqlite/learning_test.go's
// TestLearningStore_DueReviews_EmptyReturnsEmptyArrayNotNull). Asserts the raw
// marshaled JSON text, not len(), because json.Unmarshal of "null" and "[]"
// both produce a zero-length slice and are therefore indistinguishable via len().
func TestStore_DueReviews_EmptyReturnsEmptyArrayNotNull(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New() // fresh, isolated workspace — guaranteed zero rows
	store := learning.NewStore(pool, &wsID)
	ctx := context.Background()

	reviews, err := store.DueReviews(ctx, 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	raw, err := json.Marshal(reviews)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != "[]" {
		t.Errorf("raw JSON = %q, want exactly %q (nil slice must not serialize to JSON null)", got, "[]")
	}
}

// TestStore_SubmitReview_NotFound verifies ErrNotFound for an unknown
// schedule ID.
func TestStore_SubmitReview_NotFound(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := learning.NewStore(pool, &wsID)
	ctx := context.Background()

	err := store.SubmitReview(ctx, uuid.New(), learning.CardState{Stability: 1.0, Difficulty: 0.3}, learning.Good)
	if err == nil {
		t.Fatal("expected error for unknown schedule ID, got nil")
	}
	if err != learning.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestStore_SubmitReview_WorkspaceIsolation verifies that a store scoped to
// workspace B cannot advance a review schedule owned by workspace A.
func TestStore_SubmitReview_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA, wsB := uuid.New(), uuid.New()
	storeA := learning.NewStore(pool, &wsA)
	storeB := learning.NewStore(pool, &wsB)
	ctx := context.Background()

	concept, err := storeA.CreateConcept(ctx, "wsA concept", "content", nil)
	if err != nil {
		t.Fatalf("CreateConcept in wsA: %v", err)
	}
	due, err := storeA.DueReviews(ctx, 10)
	if err != nil {
		t.Fatalf("DueReviews in wsA: %v", err)
	}
	var scheduleID uuid.UUID
	for _, d := range due {
		if d.ConceptID == concept.ID {
			scheduleID = d.ScheduleID
		}
	}
	if scheduleID == uuid.Nil {
		t.Fatalf("expected due review for wsA concept %s", concept.ID)
	}

	err = storeB.SubmitReview(ctx, scheduleID, learning.CardState{Stability: 1.0, Difficulty: 0.3}, learning.Good)
	if err != learning.ErrNotFound {
		t.Errorf("expected ErrNotFound for cross-workspace SubmitReview, got %v", err)
	}
}

// TestStore_ReviewHistory_WorkspaceIsolation verifies that ReviewHistory
// scoped to workspace B does not return concepts created under workspace A.
func TestStore_ReviewHistory_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA, wsB := uuid.New(), uuid.New()
	storeA := learning.NewStore(pool, &wsA)
	storeB := learning.NewStore(pool, &wsB)
	ctx := context.Background()

	concept, err := storeA.CreateConcept(ctx, "wsA-only concept", "content", nil)
	if err != nil {
		t.Fatalf("CreateConcept in wsA: %v", err)
	}

	historyB, err := storeB.ReviewHistory(ctx)
	if err != nil {
		t.Fatalf("ReviewHistory (wsB): %v", err)
	}
	for _, row := range historyB {
		if row.ID == concept.ID {
			t.Errorf("SECURITY: wsB's ReviewHistory leaked a concept owned by wsA: %s", concept.ID)
		}
	}

	historyA, err := storeA.ReviewHistory(ctx)
	if err != nil {
		t.Fatalf("ReviewHistory (wsA): %v", err)
	}
	var foundInA bool
	for _, row := range historyA {
		if row.ID == concept.ID {
			foundInA = true
		}
	}
	if !foundInA {
		t.Errorf("expected wsA's own ReviewHistory to include concept %s", concept.ID)
	}
}

// TestStore_LearningStats_HappyPath verifies that LearningStats reflects a
// freshly created concept (total_concepts >= 1) and a submitted review
// (total_reviews >= 1), scoped to the store's workspace.
func TestStore_LearningStats_HappyPath(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := learning.NewStore(pool, &wsID)
	ctx := context.Background()

	concept, err := store.CreateConcept(ctx, "stats concept", "content", nil)
	if err != nil {
		t.Fatalf("CreateConcept: %v", err)
	}
	due, err := store.DueReviews(ctx, 10)
	if err != nil {
		t.Fatalf("DueReviews: %v", err)
	}
	var scheduleID uuid.UUID
	for _, d := range due {
		if d.ConceptID == concept.ID {
			scheduleID = d.ScheduleID
		}
	}
	if err := store.SubmitReview(ctx, scheduleID, learning.CardState{Stability: 1.0, Difficulty: 0.3}, learning.Good); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}

	stats, err := store.LearningStats(ctx)
	if err != nil {
		t.Fatalf("LearningStats: %v", err)
	}
	if stats.TotalConcepts < 1 {
		t.Errorf("TotalConcepts: got %d, want >= 1", stats.TotalConcepts)
	}
	if stats.TotalReviews < 1 {
		t.Errorf("TotalReviews: got %d, want >= 1", stats.TotalReviews)
	}
	if stats.Reviews7d < 1 {
		t.Errorf("Reviews7d: got %d, want >= 1 (review just submitted)", stats.Reviews7d)
	}
}

// TestStore_LearningStats_WorkspaceIsolation verifies that LearningStats for
// a fresh, isolated workspace does not count concepts/reviews created under a
// different workspace.
func TestStore_LearningStats_WorkspaceIsolation(t *testing.T) {
	pool := openTestPgPool(t)
	wsA, wsB := uuid.New(), uuid.New()
	storeA := learning.NewStore(pool, &wsA)
	storeB := learning.NewStore(pool, &wsB)
	ctx := context.Background()

	if _, err := storeA.CreateConcept(ctx, "wsA stats concept", "content", nil); err != nil {
		t.Fatalf("CreateConcept in wsA: %v", err)
	}

	statsB, err := storeB.LearningStats(ctx)
	if err != nil {
		t.Fatalf("LearningStats (wsB): %v", err)
	}
	if statsB.TotalConcepts != 0 {
		t.Errorf("SECURITY: wsB's LearningStats counted wsA's concept: TotalConcepts=%d, want 0", statsB.TotalConcepts)
	}
}
