package learning

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("learning: not found")

// Store handles all database operations for the Learning bounded context.
type Store struct {
	q           *db.Queries
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given connection pool scoped to the
// optional workspace. nil workspaceID = legacy unscoped mode.
func NewStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{q: db.New(pool), pool: pool, workspaceID: toUUID(workspaceID)}
}

// WithTx returns a Store bound to tx, preserving the workspace scope, for use
// in multi-store transactions (e.g. atomically materializing a concept while
// resolving a pending proposal).
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), pool: s.pool, workspaceID: s.workspaceID}
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

// ConceptForReview holds the fields needed by the AI reviewer to evaluate
// whether a concept has been mastered or is no longer helpful.
type ConceptForReview struct {
	ID          uuid.UUID
	Title       string
	Content     string
	ReviewCount int
	Stability   float64
}

// ListForAIReview returns active concepts that have at least minReviewCount
// completed reviews, ordered by review count descending.
func (s *Store) ListForAIReview(ctx context.Context, minReviewCount int) ([]ConceptForReview, error) {
	rows, err := s.q.ListConceptsForAIReview(ctx, db.ListConceptsForAIReviewParams{
		MinReviewCount: int32(minReviewCount), //nolint:gosec // minReviewCount is a small bounded constant (5)
		WorkspaceID:    s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing concepts for AI review: %w", err)
	}

	out := make([]ConceptForReview, 0, len(rows))
	for _, r := range rows {
		out = append(out, ConceptForReview{
			ID:          r.ID,
			Title:       r.Title,
			Content:     r.Content,
			ReviewCount: int(r.ReviewCount),
			Stability:   r.Stability,
		})
	}
	return out, nil
}

// UpdateConceptStatus sets the status column of the given concept.
// Valid values are "active", "mastered", and "not_helpful".
func (s *Store) UpdateConceptStatus(ctx context.Context, id uuid.UUID, status string) error {
	if _, err := s.q.UpdateConceptStatus(ctx, db.UpdateConceptStatusParams{
		ID:     id,
		Status: status,
	}); err != nil {
		return fmt.Errorf("updating concept %s status to %q: %w", id, status, err)
	}
	return nil
}

// ReviewHistory returns all concepts joined with their review schedule,
// sorted by last_review_at DESC NULLS LAST (un-reviewed concepts appear last).
// interval_days is approximated as stability * 9 (the FSRS target-90%-retention
// formula used in NextState). Status is computed in Go from review_count and
// the derived interval.
func (s *Store) ReviewHistory(ctx context.Context) ([]ConceptHistoryRow, error) {
	const q = `
		SELECT c.id, c.title, c.tags,
		       COALESCE(rs.review_count, 0)                    AS review_count,
		       rs.created_at                                   AS first_reviewed_at,
		       rs.last_review_at,
		       COALESCE(rs.stability * 9, 0)                   AS interval_days,
		       rs.due_date                                     AS next_review_at
		FROM concepts c
		LEFT JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE c.archived_at IS NULL
		  AND ($1::uuid IS NULL OR c.workspace_id = $1)
		ORDER BY rs.last_review_at DESC NULLS LAST, c.created_at DESC`
	rows, err := s.pool.Query(ctx, q, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("review history: %w", err)
	}
	defer rows.Close()

	var out []ConceptHistoryRow
	for rows.Next() {
		var (
			row             ConceptHistoryRow
			firstReviewedAt pgtype.Timestamptz
			lastReviewedAt  pgtype.Timestamptz
			nextReviewAt    pgtype.Timestamptz
		)
		if err := rows.Scan(
			&row.ID, &row.Title, &row.Tags,
			&row.ReviewCount,
			&firstReviewedAt,
			&lastReviewedAt,
			&row.IntervalDays,
			&nextReviewAt,
		); err != nil {
			return nil, fmt.Errorf("review history scan: %w", err)
		}
		if firstReviewedAt.Valid {
			t := firstReviewedAt.Time
			row.FirstReviewedAt = &t
		}
		if lastReviewedAt.Valid {
			t := lastReviewedAt.Time
			row.LastReviewedAt = &t
		}
		if nextReviewAt.Valid {
			t := nextReviewAt.Time
			row.NextReviewAt = &t
		}
		row.Status = ComputeConceptStatus(row.ReviewCount, row.IntervalDays)
		if row.Tags == nil {
			row.Tags = []string{}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("review history iter: %w", err)
	}
	if out == nil {
		out = []ConceptHistoryRow{}
	}
	return out, nil
}

// LearningStats returns aggregate learning statistics.
// interval is approximated as stability * 9 (FSRS target-90% formula).
// StreakDays counts consecutive days (ending today) on which at least one
// review was submitted, based on last_review_at in review_schedule.
func (s *Store) LearningStats(ctx context.Context) (*LearningStatsResult, error) {
	const statsQ = `
		SELECT
		  COALESCE(SUM(rs.review_count), 0)                                      AS total_reviews,
		  COUNT(*) FILTER (WHERE rs.last_review_at > NOW() - INTERVAL '7 days'
		                    AND rs.review_count > 0)                              AS reviews_7d,
		  COUNT(*) FILTER (WHERE rs.review_count >= 15
		                    AND rs.stability * 9 > 30)                            AS mastered,
		  (SELECT COUNT(*) FROM concepts c2
		    WHERE c2.archived_at IS NULL
		      AND ($1::uuid IS NULL OR c2.workspace_id = $1))                    AS total_concepts
		FROM review_schedule rs
		WHERE ($1::uuid IS NULL OR rs.workspace_id = $1)`

	var result LearningStatsResult
	statsRow := s.pool.QueryRow(ctx, statsQ, s.workspaceID)
	if err := statsRow.Scan(
		&result.TotalReviews,
		&result.Reviews7d,
		&result.Mastered,
		&result.TotalConcepts,
	); err != nil {
		return nil, fmt.Errorf("learning stats: %w", err)
	}

	// Compute streak: count consecutive days ending today where ≥1 review occurred.
	const streakQ = `
		SELECT DISTINCT DATE(last_review_at AT TIME ZONE 'UTC') AS review_day
		FROM review_schedule
		WHERE review_count > 0
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY review_day DESC`
	streakRows, err := s.pool.Query(ctx, streakQ, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("learning stats streak query: %w", err)
	}
	defer streakRows.Close()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	streak := 0
	expected := today
	for streakRows.Next() {
		var d pgtype.Date
		if err := streakRows.Scan(&d); err != nil {
			return nil, fmt.Errorf("learning stats streak scan: %w", err)
		}
		if !d.Valid {
			continue
		}
		reviewDay := time.Date(int(d.Time.Year()), d.Time.Month(), int(d.Time.Day()), 0, 0, 0, 0, time.UTC)
		if !reviewDay.Equal(expected) {
			break
		}
		streak++
		expected = expected.Add(-24 * time.Hour)
	}
	if err := streakRows.Err(); err != nil {
		return nil, fmt.Errorf("learning stats streak iter: %w", err)
	}
	result.StreakDays = streak
	return &result, nil
}

// SoftPruneDecayed implements decay.PrunerStore for the concepts table.
// It sets archived_at=NOW() on concepts that are:
//   - not already archived (archived_at IS NULL)
//   - older than cutoff (created_at < cutoff)
//   - Ebbinghaus strength < strengthThreshold
//
// Decisions table is NEVER touched by this method.
func (s *Store) SoftPruneDecayed(ctx context.Context, cutoff time.Time, strengthThreshold float64) (int64, error) {
	const q = `UPDATE concepts
		SET archived_at = NOW()
		WHERE archived_at IS NULL
		  AND created_at < $1
		  AND GREATEST(0.0, LEAST(1.0,
			importance
			* EXP(-base_lambda * (1.0 - importance * 0.8)
				* EXTRACT(EPOCH FROM (NOW() - COALESCE(last_recalled_at, created_at))) / 86400.0)
			* (1.0 + recall_count * 0.2)
		  )) < $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)`
	tag, err := s.pool.Exec(ctx, q, cutoff, strengthThreshold, s.workspaceID)
	if err != nil {
		return 0, fmt.Errorf("soft prune concepts: %w", err)
	}
	return tag.RowsAffected(), nil
}
