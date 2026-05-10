package learning

import (
	"context"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// ConceptHistoryRow is returned by ReviewHistory. It combines a concept
// record with its associated review_schedule statistics.
type ConceptHistoryRow struct {
	ID              uuid.UUID  `json:"id"`
	Title           string     `json:"title"`
	Tags            []string   `json:"tags"`
	ReviewCount     int        `json:"review_count"`
	FirstReviewedAt *time.Time `json:"first_reviewed_at,omitempty"`
	LastReviewedAt  *time.Time `json:"last_reviewed_at,omitempty"`
	IntervalDays    float64    `json:"interval_days"`
	NextReviewAt    *time.Time `json:"next_review_at,omitempty"`
	Status          string     `json:"status"` // "new"|"learning"|"reviewing"|"mastered"
}

// LearningStatsResult is the response shape for GET /api/learning/stats.
type LearningStatsResult struct {
	TotalReviews  int `json:"total_reviews"`
	Reviews7d     int `json:"reviews_7d"`
	Mastered      int `json:"mastered"`
	TotalConcepts int `json:"total_concepts"`
	StreakDays    int `json:"streak_days"`
}

// ComputeConceptStatus maps review metrics to a human-readable learning stage.
// "new": review_count=0; "learning": 1-4; "reviewing": 5-14;
// "mastered": review_count>=15 AND interval_days>30.
// Exported so both Postgres and SQLite stores can use the same logic.
func ComputeConceptStatus(reviewCount int, intervalDays float64) string {
	switch {
	case reviewCount == 0:
		return "new"
	case reviewCount < 5:
		return "learning"
	case reviewCount < 15 || intervalDays <= 30:
		return "reviewing"
	default:
		return "mastered"
	}
}

// StoreIface is the backend-agnostic contract for the Learning (FSRS)
// bounded context.
type StoreIface interface {
	CreateConcept(ctx context.Context, title, content string, tags []string) (*db.Concept, error)
	DueReviews(ctx context.Context, limit int) ([]DueReview, error)
	SubmitReview(ctx context.Context, scheduleID uuid.UUID, currentState CardState, rating Rating) error
	CountDueReviews(ctx context.Context) (int, error)
	ListForAIReview(ctx context.Context, minReviewCount int) ([]ConceptForReview, error)
	UpdateConceptStatus(ctx context.Context, id uuid.UUID, status string) error
	// ReviewHistory returns all concepts with their review summary, sorted by
	// last_reviewed_at DESC NULLS LAST (new concepts appear last).
	ReviewHistory(ctx context.Context) ([]ConceptHistoryRow, error)
	// LearningStats returns aggregate stats across all concepts and reviews.
	LearningStats(ctx context.Context) (*LearningStatsResult, error)
	// ListConcepts returns up to limit concepts ordered by created_at DESC,
	// scoped to the configured workspace.
	ListConcepts(ctx context.Context, limit int) ([]db.Concept, error)
	// ReviewedSince returns review_schedule rows where last_review_at >= since,
	// scoped to the configured workspace. Used by the timeline aggregator.
	ReviewedSince(ctx context.Context, since time.Time, limit int) ([]DueReview, error)
}

var _ StoreIface = (*Store)(nil)
