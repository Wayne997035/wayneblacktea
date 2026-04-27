package learning

import (
	"context"

	"github.com/google/uuid"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// StoreIface is the backend-agnostic contract for the Learning (FSRS)
// bounded context.
type StoreIface interface {
	CreateConcept(ctx context.Context, title, content string, tags []string) (*db.Concept, error)
	DueReviews(ctx context.Context, limit int) ([]DueReview, error)
	SubmitReview(ctx context.Context, scheduleID uuid.UUID, currentState CardState, rating Rating) error
	CountDueReviews(ctx context.Context) (int, error)
}

var _ StoreIface = (*Store)(nil)
