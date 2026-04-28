package learning

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("learning: not found")

// Store handles all database operations for the Learning bounded context.
type Store struct {
	q           *db.Queries
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given connection pool scoped to the
// optional workspace. nil workspaceID = legacy unscoped mode.
func NewStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{q: db.New(pool), workspaceID: toUUID(workspaceID)}
}

// WithTx returns a Store bound to tx, preserving the workspace scope, for use
// in multi-store transactions (e.g. atomically materializing a concept while
// resolving a pending proposal).
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), workspaceID: s.workspaceID}
}

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// DueReview represents a concept with its associated review schedule.
type DueReview struct {
	ConceptID   uuid.UUID `json:"concept_id"`
	ScheduleID  uuid.UUID `json:"schedule_id"`
	Title       string    `json:"title"`
	Content     string    `json:"content"`
	Stability   float64   `json:"stability"`
	Difficulty  float64   `json:"difficulty"`
	DueDate     time.Time `json:"due_date"`
	ReviewCount int       `json:"review_count"`
}

// CreateConcept inserts a concept and its initial review schedule.
func (s *Store) CreateConcept(ctx context.Context, title, content string, tags []string) (*db.Concept, error) {
	if tags == nil {
		tags = []string{}
	}
	concept, err := s.q.CreateConcept(ctx, db.CreateConceptParams{
		Title:       title,
		Content:     content,
		Tags:        tags,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("creating concept %q: %w", title, err)
	}

	if _, err := s.q.CreateReviewSchedule(ctx, db.CreateReviewScheduleParams{
		ConceptID:   concept.ID,
		WorkspaceID: s.workspaceID,
	}); err != nil {
		return nil, fmt.Errorf("creating review schedule for concept %s: %w", concept.ID, err)
	}

	return &concept, nil
}

// DueReviews returns concepts whose review is due, up to the given limit.
func (s *Store) DueReviews(ctx context.Context, limit int) ([]DueReview, error) {
	rows, err := s.q.ListDueReviews(ctx, db.ListDueReviewsParams{
		LimitN:      int32(limit), //nolint:gosec // limit is bounded by caller
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing due reviews: %w", err)
	}

	reviews := make([]DueReview, 0, len(rows))
	for _, r := range rows {
		var dueDate time.Time
		if r.DueDate.Valid {
			dueDate = r.DueDate.Time
		}
		reviews = append(reviews, DueReview{
			ConceptID:   r.ID,
			ScheduleID:  r.ScheduleID,
			Title:       r.Title,
			Content:     r.Content,
			Stability:   r.Stability,
			Difficulty:  r.Difficulty,
			DueDate:     dueDate,
			ReviewCount: int(r.ReviewCount),
		})
	}
	return reviews, nil
}

// SubmitReview applies the FSRS algorithm and updates the review schedule.
func (s *Store) SubmitReview(ctx context.Context, scheduleID uuid.UUID, currentState CardState, rating Rating) error {
	newStability, newDifficulty, intervalDays := NextState(currentState, rating)

	dueDate := time.Now().UTC().Add(time.Duration(intervalDays) * 24 * time.Hour)

	_, err := s.q.UpdateReviewSchedule(ctx, db.UpdateReviewScheduleParams{
		ID:          scheduleID,
		Stability:   newStability,
		Difficulty:  newDifficulty,
		DueDate:     pgtype.Timestamptz{Time: dueDate, Valid: true},
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("updating review schedule %s: %w", scheduleID, err)
	}
	return nil
}

// CountDueReviews returns the total number of concepts currently due for review.
func (s *Store) CountDueReviews(ctx context.Context) (int, error) {
	// Fetch a generous limit to count without an extra SQL query.
	reviews, err := s.DueReviews(ctx, 1000)
	if err != nil {
		return 0, err
	}
	return len(reviews), nil
}
