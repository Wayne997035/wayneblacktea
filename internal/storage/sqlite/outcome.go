package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/google/uuid"
)

// OutcomeStore is the SQLite-backed implementation of outcome.StoreIface.
type OutcomeStore struct {
	db *DB
}

// NewOutcomeStore wraps an open DB into an OutcomeStore.
func NewOutcomeStore(d *DB) *OutcomeStore {
	return &OutcomeStore{db: d}
}

// Compile-time guarantee against drift from outcome.StoreIface.
var _ outcome.StoreIface = (*OutcomeStore)(nil)

const (
	outcomeSelectCols    = `id, workspace_id, entity_type, entity_id, result, metrics, notes, created_at`
	evaluationSelectCols = `id, workspace_id, outcome_id, analysis, lessons, improvement_suggestions, created_at`
)

type outcomeRawRow struct {
	idStr       string
	wsIDNS      sql.NullString
	entityType  string
	entityIDStr string
	result      string
	metricsNS   sql.NullString
	notesNS     sql.NullString
	createdNS   sql.NullString
}

type evaluationRawRow struct {
	idStr          string
	wsIDNS         sql.NullString
	outcomeIDStr   string
	analysis       string
	lessonsStr     string
	suggestionsStr string
	createdNS      sql.NullString
}

func scanOutcomeRawRow(scan func(...any) error) (outcomeRawRow, error) {
	var r outcomeRawRow
	err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.entityType,
		&r.entityIDStr,
		&r.result,
		&r.metricsNS,
		&r.notesNS,
		&r.createdNS,
	)
	return r, err
}

func parseOutcomeRow(r outcomeRawRow) outcome.Outcome {
	o := outcome.Outcome{
		EntityType: r.entityType,
		Result:     r.result,
	}
	if id, err := uuid.Parse(r.idStr); err == nil {
		o.ID = id
	}
	if r.wsIDNS.Valid {
		if id, err := uuid.Parse(r.wsIDNS.String); err == nil {
			o.WorkspaceID = &id
		}
	}
	if id, err := uuid.Parse(r.entityIDStr); err == nil {
		o.EntityID = id
	}
	if r.metricsNS.Valid && r.metricsNS.String != "" {
		o.Metrics = []byte(r.metricsNS.String)
	}
	if r.notesNS.Valid {
		o.Notes = r.notesNS.String
	}
	if t := parseTimestamptz(r.createdNS); t.Valid {
		o.CreatedAt = t.Time
	}
	return o
}

func scanEvaluationRawRow(scan func(...any) error) (evaluationRawRow, error) {
	var r evaluationRawRow
	err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.outcomeIDStr,
		&r.analysis,
		&r.lessonsStr,
		&r.suggestionsStr,
		&r.createdNS,
	)
	return r, err
}

func parseEvaluationRow(r evaluationRawRow) outcome.Evaluation {
	e := outcome.Evaluation{
		Analysis:               r.analysis,
		Lessons:                []byte(r.lessonsStr),
		ImprovementSuggestions: []byte(r.suggestionsStr),
	}
	if id, err := uuid.Parse(r.idStr); err == nil {
		e.ID = id
	}
	if r.wsIDNS.Valid {
		if id, err := uuid.Parse(r.wsIDNS.String); err == nil {
			e.WorkspaceID = &id
		}
	}
	if id, err := uuid.Parse(r.outcomeIDStr); err == nil {
		e.OutcomeID = id
	}
	if t := parseTimestamptz(r.createdNS); t.Valid {
		e.CreatedAt = t.Time
	}
	return e
}

// CreateOutcome inserts a new outcome row and returns the persisted record.
func (s *OutcomeStore) CreateOutcome(ctx context.Context, params outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	id := uuid.New()
	now := nowRFC3339()

	const q = `INSERT INTO outcomes
		(id, workspace_id, entity_type, entity_id, result, metrics, notes, created_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)`

	var metricsArg any
	if len(params.Metrics) > 0 {
		metricsArg = string(params.Metrics)
	}
	var notesArg any
	if params.Notes != "" {
		notesArg = params.Notes
	}

	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		nullStringFromUUID(params.WorkspaceID),
		params.EntityType,
		params.EntityID.String(),
		params.Result,
		metricsArg,
		notesArg,
		now,
	)
	if err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.CreateOutcome", err)
	}
	return s.getByID(ctx, id)
}

// GetOutcomeByID fetches a single outcome by primary key, workspace-scoped.
func (s *OutcomeStore) GetOutcomeByID(ctx context.Context, id uuid.UUID, workspaceID *uuid.UUID) (outcome.Outcome, error) {
	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`

	var wsArg any
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}

	row := s.db.conn.QueryRowContext(ctx, q, id.String(), wsArg)
	r, err := scanOutcomeRawRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return outcome.Outcome{}, outcome.ErrNotFound
	}
	if err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.GetOutcomeByID", err)
	}
	return parseOutcomeRow(r), nil
}

// getByID is an internal fetch used after insert to return the persisted record.
// It always uses the DB-level workspace scope (the record was just inserted with it).
func (s *OutcomeStore) getByID(ctx context.Context, id uuid.UUID) (outcome.Outcome, error) {
	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes WHERE id = ?1 LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	r, err := scanOutcomeRawRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return outcome.Outcome{}, outcome.ErrNotFound
	}
	if err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.getByID", err)
	}
	return parseOutcomeRow(r), nil
}

// ListRecentOutcomes returns outcomes ordered by created_at DESC, with optional
// workspace and entity_type filters.
func (s *OutcomeStore) ListRecentOutcomes(
	ctx context.Context, workspaceID *uuid.UUID, entityType string, limit int,
) ([]outcome.Outcome, error) {
	if limit <= 0 {
		limit = 20
	}
	var wsArg any
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}
	var etArg any
	if entityType != "" {
		etArg = entityType
	}

	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND (?2 IS NULL OR entity_type = ?2)
		ORDER BY created_at DESC
		LIMIT ?3`

	rows, err := s.db.conn.QueryContext(ctx, q, wsArg, etArg, limit)
	if err != nil {
		return nil, errWrap("OutcomeStore.ListRecentOutcomes", err)
	}
	defer func() { _ = rows.Close() }()

	var out []outcome.Outcome
	for rows.Next() {
		r, err := scanOutcomeRawRow(rows.Scan)
		if err != nil {
			return nil, errWrap("OutcomeStore.ListRecentOutcomes scan", err)
		}
		out = append(out, parseOutcomeRow(r))
	}
	return out, errWrap("OutcomeStore.ListRecentOutcomes iter", rows.Err())
}

// CreateEvaluation inserts a new evaluation row and returns the persisted record.
func (s *OutcomeStore) CreateEvaluation(ctx context.Context, params outcome.CreateEvaluationParams) (outcome.Evaluation, error) {
	id := uuid.New()
	now := nowRFC3339()

	lessons := params.Lessons
	if len(lessons) == 0 {
		lessons = []byte("[]")
	}
	suggestions := params.ImprovementSuggestions
	if len(suggestions) == 0 {
		suggestions = []byte("[]")
	}

	const q = `INSERT INTO evaluations
		(id, workspace_id, outcome_id, analysis, lessons, improvement_suggestions, created_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)`

	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		nullStringFromUUID(params.WorkspaceID),
		params.OutcomeID.String(),
		params.Analysis,
		string(lessons),
		string(suggestions),
		now,
	)
	if err != nil {
		return outcome.Evaluation{}, errWrap("OutcomeStore.CreateEvaluation", err)
	}
	return s.getEvalByID(ctx, id)
}

func (s *OutcomeStore) getEvalByID(ctx context.Context, id uuid.UUID) (outcome.Evaluation, error) {
	const q = `SELECT ` + evaluationSelectCols + ` FROM evaluations WHERE id = ?1 LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	r, err := scanEvaluationRawRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return outcome.Evaluation{}, fmt.Errorf("evaluation not found after insert")
	}
	if err != nil {
		return outcome.Evaluation{}, errWrap("OutcomeStore.getEvalByID", err)
	}
	return parseEvaluationRow(r), nil
}

// ListEvaluationsByOutcomeID returns all evaluations for an outcome, ordered by created_at ASC.
func (s *OutcomeStore) ListEvaluationsByOutcomeID(
	ctx context.Context, outcomeID uuid.UUID, workspaceID *uuid.UUID,
) ([]outcome.Evaluation, error) {
	var wsArg any
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}

	const q = `SELECT ` + evaluationSelectCols + ` FROM evaluations
		WHERE outcome_id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at ASC`

	rows, err := s.db.conn.QueryContext(ctx, q, outcomeID.String(), wsArg)
	if err != nil {
		return nil, errWrap("OutcomeStore.ListEvaluationsByOutcomeID", err)
	}
	defer func() { _ = rows.Close() }()

	var out []outcome.Evaluation
	for rows.Next() {
		r, err := scanEvaluationRawRow(rows.Scan)
		if err != nil {
			return nil, errWrap("OutcomeStore.ListEvaluationsByOutcomeID scan", err)
		}
		out = append(out, parseEvaluationRow(r))
	}
	return out, errWrap("OutcomeStore.ListEvaluationsByOutcomeID iter", rows.Err())
}

// ListFailedOutcomes returns outcomes with result='failure' or result='regressed'.
func (s *OutcomeStore) ListFailedOutcomes(ctx context.Context, workspaceID *uuid.UUID, limit int) ([]outcome.Outcome, error) {
	if limit <= 0 {
		limit = 20
	}
	var wsArg any
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}

	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE result IN ('failure', 'regressed')
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC
		LIMIT ?2`

	rows, err := s.db.conn.QueryContext(ctx, q, wsArg, limit)
	if err != nil {
		return nil, errWrap("OutcomeStore.ListFailedOutcomes", err)
	}
	defer func() { _ = rows.Close() }()

	var out []outcome.Outcome
	for rows.Next() {
		r, err := scanOutcomeRawRow(rows.Scan)
		if err != nil {
			return nil, errWrap("OutcomeStore.ListFailedOutcomes scan", err)
		}
		out = append(out, parseOutcomeRow(r))
	}
	return out, errWrap("OutcomeStore.ListFailedOutcomes iter", rows.Err())
}

// PruneOlderThan hard-deletes outcomes and their evaluations older than cutoff.
// Evaluations are deleted first (no FK cascade per red-line §9).
func (s *OutcomeStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffStr := cutoff.UTC().Format("2006-01-02T15:04:05.000Z07:00")

	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("pruning outcomes begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const deleteEvals = `
		DELETE FROM evaluations
		WHERE outcome_id IN (SELECT id FROM outcomes WHERE created_at < ?)`
	if _, err := tx.ExecContext(ctx, deleteEvals, cutoffStr); err != nil {
		return 0, fmt.Errorf("pruning evaluations: %w", err)
	}

	const deleteOutcomes = `DELETE FROM outcomes WHERE created_at < ?`
	result, err := tx.ExecContext(ctx, deleteOutcomes, cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("pruning outcomes: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("pruning outcomes commit: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("pruning outcomes rows affected: %w", err)
	}
	return n, nil
}
