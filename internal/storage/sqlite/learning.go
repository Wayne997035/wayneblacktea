package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decay"
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

// conceptsSelectCols includes decay fields added in migration 000019.
const conceptsSelectCols = `id, title, content, tags, created_at, updated_at, workspace_id, status,
	importance, recall_count, last_recalled_at, base_lambda, archived_at`

func scanConcept(scan func(...any) error) (db.Concept, error) {
	var (
		c                        db.Concept
		idStr                    string
		tagsNS, createdNS, updNS sql.NullString
		workspaceNS              sql.NullString
		lastRecalledNS           sql.NullString
		archivedNS               sql.NullString
	)
	err := scan(
		&idStr, &c.Title, &c.Content, &tagsNS, &createdNS, &updNS, &workspaceNS, &c.Status,
		&c.Importance, &c.RecallCount, &lastRecalledNS, &c.BaseLambda, &archivedNS,
	)
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
	c.LastRecalledAt = parseTimestamptz(lastRecalledNS)
	c.ArchivedAt = parseTimestamptz(archivedNS)
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

// CreateConceptTx inserts a concept and its initial review_schedule row within
// the provided *sql.Tx. It is the transactional counterpart of CreateConcept
// and is used by the confirm_proposal accept path so that concept creation and
// proposal resolution are committed atomically.
func (s *LearningStore) CreateConceptTx(ctx context.Context, tx *sql.Tx, title, content string, tags []string) (uuid.UUID, error) {
	tagsJSON, err := encodeStringSlice(tags)
	if err != nil {
		return uuid.UUID{}, err
	}
	conceptID := uuid.New()
	scheduleID := uuid.New()
	now := sqliteNowMillis()
	const conceptQ = `INSERT INTO concepts
		(id, workspace_id, title, content, tags, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?6)`
	if _, err = tx.ExecContext(ctx, conceptQ,
		conceptID.String(), s.db.workspaceArg(), title, content, tagsJSON, now); err != nil {
		return uuid.UUID{}, errWrap("CreateConceptTx concept", err)
	}
	const scheduleQ = `INSERT INTO review_schedule
		(id, workspace_id, concept_id, due_date, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?4, ?4)`
	if _, err = tx.ExecContext(ctx, scheduleQ,
		scheduleID.String(), s.db.workspaceArg(), conceptID.String(), now); err != nil {
		return uuid.UUID{}, errWrap("CreateConceptTx schedule", err)
	}
	return conceptID, nil
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
	if err := rows.Err(); err != nil {
		return nil, errWrap("DueReviews iter", err)
	}
	if out == nil {
		// list endpoints MUST return [] not null (a nil slice marshals to JSON
		// null); matches PG's make([]DueReview, 0, len(rows)) in
		// internal/learning/store.go.
		out = []learning.DueReview{}
	}
	return out, nil
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

// ReviewHistory returns all non-archived concepts joined with their review
// schedule, sorted by last_review_at DESC NULLS LAST.
// interval_days is approximated as stability * 9 (same FSRS formula as the
// Postgres store), since the review_schedule table has no interval_days column.
func (s *LearningStore) ReviewHistory(ctx context.Context) ([]learning.ConceptHistoryRow, error) {
	const q = `SELECT c.id, c.title, c.tags,
		       COALESCE(rs.review_count, 0)        AS review_count,
		       rs.created_at                       AS first_reviewed_at,
		       rs.last_review_at,
		       COALESCE(rs.stability * 9, 0.0)     AS interval_days,
		       rs.due_date                         AS next_review_at
		FROM concepts c
		LEFT JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE c.archived_at IS NULL
		  AND (?1 IS NULL OR c.workspace_id = ?1)
		ORDER BY rs.last_review_at DESC, c.created_at DESC`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("ReviewHistory", err)
	}
	defer func() { _ = rows.Close() }()

	var out []learning.ConceptHistoryRow
	for rows.Next() {
		var (
			row             learning.ConceptHistoryRow
			idStr           string
			tagsNS          sql.NullString
			firstReviewedNS sql.NullString
			lastReviewedNS  sql.NullString
			nextReviewNS    sql.NullString
		)
		if err := rows.Scan(
			&idStr, &row.Title, &tagsNS,
			&row.ReviewCount,
			&firstReviewedNS,
			&lastReviewedNS,
			&row.IntervalDays,
			&nextReviewNS,
		); err != nil {
			return nil, errWrap("ReviewHistory scan", err)
		}
		if id, err := uuid.Parse(idStr); err == nil {
			row.ID = id
		}
		tags, _ := decodeStringSlice(tagsNS)
		if tags == nil {
			tags = []string{}
		}
		row.Tags = tags
		if ts := parseTimestamptz(firstReviewedNS); ts.Valid {
			t := ts.Time
			row.FirstReviewedAt = &t
		}
		if ts := parseTimestamptz(lastReviewedNS); ts.Valid {
			t := ts.Time
			row.LastReviewedAt = &t
		}
		if ts := parseTimestamptz(nextReviewNS); ts.Valid {
			t := ts.Time
			row.NextReviewAt = &t
		}
		row.Status = learning.ComputeConceptStatus(row.ReviewCount, row.IntervalDays)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("ReviewHistory iter", err)
	}
	if out == nil {
		out = []learning.ConceptHistoryRow{}
	}
	return out, nil
}

// LearningStats returns aggregate learning stats for the workspace.
func (s *LearningStore) LearningStats(ctx context.Context) (*learning.LearningStatsResult, error) {
	const statsQ = `SELECT
		       COALESCE(SUM(rs.review_count), 0)   AS total_reviews,
		       COUNT(CASE WHEN rs.last_review_at >= ?1
		                   AND rs.review_count > 0 THEN 1 END) AS reviews_7d,
		       COUNT(CASE WHEN rs.review_count >= 15
		                   AND rs.stability * 9 > 30 THEN 1 END) AS mastered,
		       (SELECT COUNT(*) FROM concepts c2
		         WHERE c2.archived_at IS NULL
		           AND (?2 IS NULL OR c2.workspace_id = ?2)) AS total_concepts
		FROM review_schedule rs
		WHERE (?2 IS NULL OR rs.workspace_id = ?2)`

	sevenDaysAgo := time.Now().UTC().Add(-7 * 24 * time.Hour).Format(sqliteMillisLayout)
	var result learning.LearningStatsResult
	if err := s.db.conn.QueryRowContext(ctx, statsQ, sevenDaysAgo, s.db.workspaceArg()).Scan(
		&result.TotalReviews,
		&result.Reviews7d,
		&result.Mastered,
		&result.TotalConcepts,
	); err != nil {
		return nil, errWrap("LearningStats", err)
	}

	// Compute streak: count consecutive days ending today with ≥1 review.
	const streakQ = `SELECT DISTINCT DATE(last_review_at) AS review_day
		FROM review_schedule
		WHERE review_count > 0
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY review_day DESC`
	streakRows, err := s.db.conn.QueryContext(ctx, streakQ, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("LearningStats streak query", err)
	}
	defer func() { _ = streakRows.Close() }()

	today := time.Now().UTC().Truncate(24 * time.Hour)
	streak := 0
	expected := today
	for streakRows.Next() {
		var dayStr sql.NullString
		if err := streakRows.Scan(&dayStr); err != nil {
			return nil, errWrap("LearningStats streak scan", err)
		}
		if !dayStr.Valid {
			continue
		}
		reviewDay, err := time.Parse("2006-01-02", dayStr.String)
		if err != nil {
			continue
		}
		reviewDay = reviewDay.UTC()
		if !reviewDay.Equal(expected) {
			break
		}
		streak++
		expected = expected.Add(-24 * time.Hour)
	}
	if err := streakRows.Err(); err != nil {
		return nil, errWrap("LearningStats streak iter", err)
	}
	result.StreakDays = streak
	return &result, nil
}

// ListConcepts returns up to limit concepts ordered by created_at DESC,
// scoped to the configured workspace.
func (s *LearningStore) ListConcepts(ctx context.Context, limit int) ([]db.Concept, error) {
	const q = `SELECT ` + conceptsSelectCols + ` FROM concepts
		WHERE (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC
		LIMIT ?2`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), limit)
	if err != nil {
		return nil, errWrap("ListConcepts", err)
	}
	defer func() { _ = rows.Close() }()

	var out []db.Concept
	for rows.Next() {
		c, err := scanConcept(rows.Scan)
		if err != nil {
			return nil, errWrap("ListConcepts scan", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("ListConcepts iter", err)
	}
	return out, nil
}

// ReviewedSince returns DueReview entries whose last_review_at >= since,
// scoped to the configured workspace.
func (s *LearningStore) ReviewedSince(ctx context.Context, since time.Time, limit int) ([]learning.DueReview, error) {
	sinceStr := since.UTC().Format(sqliteMillisLayout)
	const q = `SELECT rs.id, c.title, rs.due_date
		FROM review_schedule rs
		JOIN concepts c ON c.id = rs.concept_id
		WHERE (?1 IS NULL OR rs.workspace_id = ?1)
		  AND rs.last_review_at >= ?2
		  AND rs.last_review_at IS NOT NULL
		ORDER BY rs.last_review_at DESC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), sinceStr, limit)
	if err != nil {
		return nil, errWrap("ReviewedSince", err)
	}
	defer func() { _ = rows.Close() }()

	var out []learning.DueReview
	for rows.Next() {
		var (
			r         learning.DueReview
			idStr     string
			dueDateNS sql.NullString
		)
		if err := rows.Scan(&idStr, &r.Title, &dueDateNS); err != nil {
			return nil, errWrap("ReviewedSince scan", err)
		}
		if id, err := uuid.Parse(idStr); err == nil {
			r.ScheduleID = id
		}
		if ts := parseTimestamptz(dueDateNS); ts.Valid {
			r.DueDate = ts.Time
		}
		out = append(out, r)
	}
	return out, errWrap("ReviewedSince iter", rows.Err())
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

// SoftPruneDecayed implements decay.PrunerStore for the SQLite concepts table.
// It sets archived_at=NOW() on concepts that are:
//   - not already archived (archived_at IS NULL)
//   - older than cutoff (created_at < cutoff, i.e. age > 90 days)
//   - Ebbinghaus strength (computed app-side) < strengthThreshold
//
// Decisions table is never touched.
func (s *LearningStore) SoftPruneDecayed(ctx context.Context, cutoff time.Time, strengthThreshold float64) (int64, error) {
	cutoffStr := cutoff.UTC().Format(sqliteMillisLayout)
	const selQ = `SELECT ` + conceptsSelectCols + ` FROM concepts
		WHERE archived_at IS NULL
		  AND created_at < ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`
	rows, err := s.db.conn.QueryContext(ctx, selQ, cutoffStr, s.db.workspaceArg())
	if err != nil {
		return 0, errWrap("SoftPruneDecayedSelect concepts", err)
	}
	defer func() { _ = rows.Close() }()

	var candidates []db.Concept
	for rows.Next() {
		c, err := scanConcept(rows.Scan)
		if err != nil {
			return 0, errWrap("SoftPruneDecayedScan concepts", err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return 0, errWrap("SoftPruneDecayedIter concepts", err)
	}

	now := time.Now().UTC()
	nowStr := now.Format(sqliteMillisLayout)
	var pruned int64
	const updQ = `UPDATE concepts SET archived_at = ?1
		WHERE id = ?2
		  AND (?3 IS NULL OR workspace_id = ?3)`
	for _, c := range candidates {
		ageDays := computeConceptAgeDays(c, now)
		str := decay.ComputeStrength(c.Importance, c.BaseLambda, ageDays, int(c.RecallCount))
		if str >= strengthThreshold {
			continue
		}
		if _, err := s.db.conn.ExecContext(ctx, updQ, nowStr, c.ID.String(), s.db.workspaceArg()); err != nil {
			slog.Warn("sqlite learning: soft-prune concept update failed", "id", c.ID, "err", err)
			continue
		}
		pruned++
	}
	return pruned, nil
}

// computeConceptAgeDays computes the age in days since last recall (or creation).
func computeConceptAgeDays(c db.Concept, now time.Time) float64 {
	if c.LastRecalledAt.Valid {
		diff := now.Sub(c.LastRecalledAt.Time)
		if diff < 0 {
			return 0
		}
		return diff.Hours() / 24.0
	}
	if c.CreatedAt.Valid {
		diff := now.Sub(c.CreatedAt.Time)
		if diff < 0 {
			return 0
		}
		return diff.Hours() / 24.0
	}
	return 0
}
