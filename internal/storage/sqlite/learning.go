package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/google/uuid"
)

// LearningStore is the SQLite-backed implementation of learning.StoreIface.
type LearningStore struct {
	db *DB
}

// NewLearningStore wraps an open DB into a LearningStore.
func NewLearningStore(d *DB) *LearningStore {
	return &LearningStore{db: d}
}

var _ learning.StoreIface = (*LearningStore)(nil)

const conceptsSelectCols = `id, title, content, tags, created_at, updated_at, workspace_id, status`

func scanConcept(scan func(...any) error) (db.Concept, error) {
	var (
		c                        db.Concept
		idStr                    string
		tagsNS, createdNS, updNS sql.NullString
		workspaceNS              sql.NullString
	)
	err := scan(&idStr, &c.Title, &c.Content, &tagsNS, &createdNS, &updNS, &workspaceNS, &c.Status)
	if err != nil {
		return db.Concept{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		c.ID = id
	}
	tags, err := decodeStringSlice(tagsNS)
	if err != nil {
		return db.Concept{}, err
	}
	c.Tags = tags
	c.CreatedAt = parseTimestamptz(createdNS)
	c.UpdatedAt = parseTimestamptz(updNS)
	c.WorkspaceID = pgtypeUUID(nsString(workspaceNS))
	return c, nil
}

// CreateConcept inserts a concept and its initial review schedule.
func (s *LearningStore) CreateConcept(ctx context.Context, title, content string, tags []string) (*db.Concept, error) {
	tagsJSON, err := encodeStringSlice(tags)
	if err != nil {
		return nil, err
	}

	conceptID := uuid.New()
	scheduleID := uuid.New()
	now := sqliteNowMillis()
	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, errWrap("CreateConcept begin", err)
	}
	defer func() { _ = tx.Rollback() }()

	const conceptQ = `INSERT INTO concepts
		(id, workspace_id, title, content, tags, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?6)`
	if _, err = tx.ExecContext(ctx, conceptQ,
		conceptID.String(), s.db.workspaceArg(), title, content, tagsJSON, now); err != nil {
		return nil, errWrap("CreateConcept", err)
	}
	const scheduleQ = `INSERT INTO review_schedule
		(id, workspace_id, concept_id, due_date, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?4, ?4)`
	if _, err = tx.ExecContext(ctx, scheduleQ,
		scheduleID.String(), s.db.workspaceArg(), conceptID.String(), now); err != nil {
		return nil, errWrap("CreateReviewSchedule", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, errWrap("CreateConcept commit", err)
	}
	return s.conceptByID(ctx, conceptID)
}

// DueReviews returns concepts whose review schedule is due and status is active.
func (s *LearningStore) DueReviews(ctx context.Context, limit int) ([]learning.DueReview, error) {
	const q = `SELECT c.id, rs.id, c.title, c.content, rs.stability, rs.difficulty,
			rs.due_date, rs.review_count
		FROM concepts c
		JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE rs.due_date <= ?1
		  AND c.status = 'active'
		  AND (?2 IS NULL OR c.workspace_id = ?2)
		ORDER BY rs.due_date ASC, rs.id ASC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, sqliteNowMillis(), s.db.workspaceArg(), limit)
	if err != nil {
		return nil, errWrap("DueReviews", err)
	}
	defer func() { _ = rows.Close() }()

	var out []learning.DueReview
	for rows.Next() {
		var (
			r                           learning.DueReview
			conceptIDStr, scheduleIDStr string
			dueNS                       sql.NullString
			reviewCount                 int
		)
		if err := rows.Scan(&conceptIDStr, &scheduleIDStr, &r.Title, &r.Content,
			&r.Stability, &r.Difficulty, &dueNS, &reviewCount); err != nil {
			return nil, errWrap("DueReviews scan", err)
		}
		if id, err := uuid.Parse(conceptIDStr); err == nil {
			r.ConceptID = id
		}
		if id, err := uuid.Parse(scheduleIDStr); err == nil {
			r.ScheduleID = id
		}
		if due := parseTimestamptz(dueNS); due.Valid {
			r.DueDate = due.Time
		}
		r.ReviewCount = reviewCount
		out = append(out, r)
	}
	return out, errWrap("DueReviews iter", rows.Err())
}

// SubmitReview applies FSRS and updates the review schedule.
func (s *LearningStore) SubmitReview(
	ctx context.Context, scheduleID uuid.UUID, currentState learning.CardState, rating learning.Rating,
) error {
	stability, difficulty, intervalDays := learning.NextState(currentState, rating)
	now := time.Now().UTC()
	dueDate := now.Add(time.Duration(intervalDays) * 24 * time.Hour).Format(sqliteMillisLayout)
	nowText := now.Format(sqliteMillisLayout)
	const q = `UPDATE review_schedule
		SET stability = ?2, difficulty = ?3, due_date = ?4,
		    last_review_at = ?5, review_count = review_count + 1, updated_at = ?5
		WHERE id = ?1
		  AND (?6 IS NULL OR workspace_id = ?6)`
	res, err := s.db.conn.ExecContext(ctx, q,
		scheduleID.String(), stability, difficulty, dueDate, nowText, s.db.workspaceArg())
	if err != nil {
		return errWrap("SubmitReview", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return learning.ErrNotFound
	}
	return nil
}

// CountDueReviews returns the total number of concepts currently due.
func (s *LearningStore) CountDueReviews(ctx context.Context) (int, error) {
	const q = `SELECT COUNT(*)
		FROM concepts c
		JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE rs.due_date <= ?1
		  AND (?2 IS NULL OR c.workspace_id = ?2)`
	var count int
	if err := s.db.conn.QueryRowContext(ctx, q, sqliteNowMillis(), s.db.workspaceArg()).Scan(&count); err != nil {
		return 0, errWrap("CountDueReviews", err)
	}
	return count, nil
}

// ListForAIReview returns active concepts with at least minReviewCount completed
// reviews, ordered by review count descending.
func (s *LearningStore) ListForAIReview(ctx context.Context, minReviewCount int) ([]learning.ConceptForReview, error) {
	const q = `SELECT c.id, c.title, c.content, rs.review_count, rs.stability
		FROM concepts c
		JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE c.status = 'active'
		  AND rs.review_count >= ?1
		  AND (?2 IS NULL OR c.workspace_id = ?2)
		ORDER BY rs.review_count DESC`
	rows, err := s.db.conn.QueryContext(ctx, q, minReviewCount, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("ListForAIReview", err)
	}
	defer func() { _ = rows.Close() }()

	var out []learning.ConceptForReview
	for rows.Next() {
		var (
			c     learning.ConceptForReview
			idStr string
		)
		if err := rows.Scan(&idStr, &c.Title, &c.Content, &c.ReviewCount, &c.Stability); err != nil {
			return nil, errWrap("ListForAIReview scan", err)
		}
		if id, err := uuid.Parse(idStr); err == nil {
			c.ID = id
		}
		out = append(out, c)
	}
	return out, errWrap("ListForAIReview iter", rows.Err())
}

// UpdateConceptStatus sets the status column for the given concept.
func (s *LearningStore) UpdateConceptStatus(ctx context.Context, id uuid.UUID, status string) error {
	const q = `UPDATE concepts
		SET status = ?2, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ?1`
	res, err := s.db.conn.ExecContext(ctx, q, id.String(), status)
	if err != nil {
		return errWrap("UpdateConceptStatus", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return learning.ErrNotFound
	}
	return nil
}

func (s *LearningStore) conceptByID(ctx context.Context, id uuid.UUID) (*db.Concept, error) {
	const q = `SELECT ` + conceptsSelectCols + ` FROM concepts
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	c, err := scanConcept(s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg()).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, learning.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("conceptByID", err)
	}
	return &c, nil
}
