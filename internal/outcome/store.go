package outcome

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed implementation of StoreIface.
type Store struct {
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID // zero = unscoped (single-tenant legacy mode)
}

// NewStore returns a Store backed by the given pool, scoped to the optional
// workspaceID. nil workspaceID = legacy unscoped mode.
func NewStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{pool: pool, workspaceID: toPgtypeUUID(workspaceID)}
}

func toPgtypeUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

func uuidFromPgtype(v pgtype.UUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	id := uuid.UUID(v.Bytes)
	return &id
}

// outcomeSelectCols is the canonical column list for outcome SELECT queries.
// related_rule_ids was added in migration 000063; work_session_id in 000067;
// supersedes_id in 000074; updated_at in 000075.
const outcomeSelectCols = `id, workspace_id, entity_type, entity_id, result, metrics, notes, ` +
	`related_rule_ids, work_session_id, supersedes_id, created_at, updated_at`

// evaluationSelectCols is the canonical column list for evaluation SELECT queries.
const evaluationSelectCols = `id, workspace_id, outcome_id, analysis, lessons, improvement_suggestions, created_at`

// scanOutcomeRow reads a full outcomes row from pgx.Rows into an Outcome.
func scanOutcomeRow(rows pgx.Rows) (Outcome, error) {
	var (
		o              Outcome
		wsID           pgtype.UUID
		entityID       pgtype.UUID
		workSessionID  pgtype.UUID
		supersedesID   pgtype.UUID
		createdAt      pgtype.Timestamptz
		updatedAt      pgtype.Timestamptz
		notesText      pgtype.Text
		relatedRuleIDs []uuid.UUID
	)
	err := rows.Scan(
		&o.ID,
		&wsID,
		&o.EntityType,
		&entityID,
		&o.Result,
		&o.Metrics,
		&notesText,
		&relatedRuleIDs,
		&workSessionID,
		&supersedesID,
		&createdAt,
		&updatedAt,
	)
	if err != nil {
		return Outcome{}, fmt.Errorf("scanning outcome: %w", err)
	}
	o.WorkspaceID = uuidFromPgtype(wsID)
	if entityID.Valid {
		o.EntityID = uuid.UUID(entityID.Bytes)
	}
	o.WorkSessionID = uuidFromPgtype(workSessionID)
	o.SupersedesID = uuidFromPgtype(supersedesID)
	if createdAt.Valid {
		o.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		o.UpdatedAt = updatedAt.Time
	}
	if notesText.Valid {
		o.Notes = notesText.String
	}
	if relatedRuleIDs == nil {
		relatedRuleIDs = []uuid.UUID{}
	}
	o.RelatedRuleIDs = relatedRuleIDs
	return o, nil
}

// scanEvaluationRow reads a full evaluations row from pgx.Rows into an Evaluation.
func scanEvaluationRow(rows pgx.Rows) (Evaluation, error) {
	var (
		e         Evaluation
		wsID      pgtype.UUID
		outcomeID pgtype.UUID
		createdAt pgtype.Timestamptz
	)
	err := rows.Scan(
		&e.ID,
		&wsID,
		&outcomeID,
		&e.Analysis,
		&e.Lessons,
		&e.ImprovementSuggestions,
		&createdAt,
	)
	if err != nil {
		return Evaluation{}, fmt.Errorf("scanning evaluation: %w", err)
	}
	e.WorkspaceID = uuidFromPgtype(wsID)
	if outcomeID.Valid {
		e.OutcomeID = uuid.UUID(outcomeID.Bytes)
	}
	if createdAt.Valid {
		e.CreatedAt = createdAt.Time
	}
	return e, nil
}

// CreateOutcome inserts a new outcome row and returns the persisted record.
func (s *Store) CreateOutcome(ctx context.Context, params CreateOutcomeParams) (Outcome, error) {
	const q = `
		INSERT INTO outcomes (workspace_id, entity_type, entity_id, result, metrics, notes, related_rule_ids, work_session_id, supersedes_id)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, $9)
		RETURNING ` + outcomeSelectCols

	var notesArg pgtype.Text
	if params.Notes != "" {
		notesArg = pgtype.Text{String: params.Notes, Valid: true}
	}
	var metricsArg any
	if len(params.Metrics) > 0 {
		metricsArg = string(params.Metrics)
	}
	// pgx handles []uuid.UUID natively for UUID[]. Use empty slice, not nil,
	// so the column receives '{}' rather than NULL (consistent with the nil check
	// in migration 000063 which makes the column nullable — both are valid,
	// but empty array is more explicit).
	relatedRuleIDs := params.RelatedRuleIDs
	if relatedRuleIDs == nil {
		relatedRuleIDs = []uuid.UUID{}
	}

	rows, err := s.pool.Query(
		ctx, q,
		toPgtypeUUID(params.WorkspaceID),
		params.EntityType,
		params.EntityID,
		params.Result,
		metricsArg,
		notesArg,
		relatedRuleIDs,
		toPgtypeUUID(params.WorkSessionID),
		toPgtypeUUID(params.SupersedesID),
	)
	if err != nil {
		return Outcome{}, fmt.Errorf("creating outcome: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return Outcome{}, fmt.Errorf("creating outcome: no row returned")
	}
	o, err := scanOutcomeRow(rows)
	if err != nil {
		return Outcome{}, fmt.Errorf("creating outcome scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return Outcome{}, fmt.Errorf("creating outcome rows.Err: %w", err)
	}
	return o, nil
}

// GetOutcomeByID fetches a single outcome by primary key, workspace-scoped.
func (s *Store) GetOutcomeByID(ctx context.Context, id uuid.UUID, workspaceID *uuid.UUID) (Outcome, error) {
	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		LIMIT 1`

	rows, err := s.pool.Query(ctx, q, id, toPgtypeUUID(workspaceID))
	if err != nil {
		return Outcome{}, fmt.Errorf("getting outcome %s: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Outcome{}, fmt.Errorf("getting outcome rows.Err: %w", err)
		}
		return Outcome{}, ErrNotFound
	}
	o, err := scanOutcomeRow(rows)
	if err != nil {
		return Outcome{}, fmt.Errorf("getting outcome scan: %w", err)
	}
	return o, nil
}

// ListRecentOutcomes returns outcomes ordered by created_at DESC, with optional
// workspace and entity_type filters.
func (s *Store) ListRecentOutcomes(ctx context.Context, workspaceID *uuid.UUID, entityType string, limit int) ([]Outcome, error) {
	if limit <= 0 {
		limit = 20
	}
	var entityTypeArg pgtype.Text
	if entityType != "" {
		entityTypeArg = pgtype.Text{String: entityType, Valid: true}
	}

	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		  AND ($2::text IS NULL OR entity_type = $2)
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, toPgtypeUUID(workspaceID), entityTypeArg, limit)
	if err != nil {
		return nil, fmt.Errorf("listing recent outcomes: %w", err)
	}
	defer rows.Close()

	var out []Outcome
	for rows.Next() {
		o, err := scanOutcomeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("listing recent outcomes scan: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing recent outcomes rows.Err: %w", err)
	}
	return out, nil
}

// CreateEvaluation inserts a new evaluation row and returns the persisted record.
func (s *Store) CreateEvaluation(ctx context.Context, params CreateEvaluationParams) (Evaluation, error) {
	const q = `
		INSERT INTO evaluations (workspace_id, outcome_id, analysis, lessons, improvement_suggestions)
		VALUES ($1, $2, $3, $4::jsonb, $5::jsonb)
		RETURNING ` + evaluationSelectCols

	lessons := params.Lessons
	if len(lessons) == 0 {
		lessons = []byte("[]")
	}
	suggestions := params.ImprovementSuggestions
	if len(suggestions) == 0 {
		suggestions = []byte("[]")
	}

	rows, err := s.pool.Query(
		ctx, q,
		toPgtypeUUID(params.WorkspaceID),
		params.OutcomeID,
		params.Analysis,
		string(lessons),
		string(suggestions),
	)
	if err != nil {
		return Evaluation{}, fmt.Errorf("creating evaluation: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return Evaluation{}, fmt.Errorf("creating evaluation: no row returned")
	}
	e, err := scanEvaluationRow(rows)
	if err != nil {
		return Evaluation{}, fmt.Errorf("creating evaluation scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return Evaluation{}, fmt.Errorf("creating evaluation rows.Err: %w", err)
	}
	return e, nil
}

// ListEvaluationsByOutcomeID returns all evaluations for an outcome, ordered
// by created_at ASC.
func (s *Store) ListEvaluationsByOutcomeID(ctx context.Context, outcomeID uuid.UUID, workspaceID *uuid.UUID) ([]Evaluation, error) {
	const q = `SELECT ` + evaluationSelectCols + ` FROM evaluations
		WHERE outcome_id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY created_at ASC`

	rows, err := s.pool.Query(ctx, q, outcomeID, toPgtypeUUID(workspaceID))
	if err != nil {
		return nil, fmt.Errorf("listing evaluations for outcome %s: %w", outcomeID, err)
	}
	defer rows.Close()

	var out []Evaluation
	for rows.Next() {
		e, err := scanEvaluationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("listing evaluations scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing evaluations rows.Err: %w", err)
	}
	return out, nil
}

// ListFailedOutcomes returns outcomes with result='failure' or
// result='regressed', ordered by created_at DESC.
func (s *Store) ListFailedOutcomes(ctx context.Context, workspaceID *uuid.UUID, limit int) ([]Outcome, error) {
	if limit <= 0 {
		limit = 20
	}

	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE result IN ('failure', 'regressed')
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, q, toPgtypeUUID(workspaceID), limit)
	if err != nil {
		return nil, fmt.Errorf("listing failed outcomes: %w", err)
	}
	defer rows.Close()

	var out []Outcome
	for rows.Next() {
		o, err := scanOutcomeRow(rows)
		if err != nil {
			return nil, fmt.Errorf("listing failed outcomes scan: %w", err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing failed outcomes rows.Err: %w", err)
	}
	return out, nil
}

// PruneOlderThan hard-deletes outcomes and their evaluations older than cutoff.
// Evaluations are deleted first (no FK cascade per red-line §9; referential
// integrity enforced in code).
func (s *Store) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("pruning outcomes begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const deleteEvals = `
		DELETE FROM evaluations
		WHERE outcome_id IN (SELECT id FROM outcomes WHERE created_at < $1)`
	if _, err := tx.Exec(ctx, deleteEvals, cutoff); err != nil {
		return 0, fmt.Errorf("pruning evaluations: %w", err)
	}

	const deleteOutcomes = `DELETE FROM outcomes WHERE created_at < $1`
	tag, err := tx.Exec(ctx, deleteOutcomes, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning outcomes: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("pruning outcomes commit: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ExistsForEntity reports whether an outcome row already exists for the given
// entity, optionally scoped to workspaceID. Backed by idx_outcomes_entity_id /
// idx_outcomes_workspace_entity (migration 000054).
func (s *Store) ExistsForEntity(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS(
		SELECT 1 FROM outcomes
		WHERE entity_type = $1
		  AND entity_id = $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)
	)`
	var exists bool
	if err := s.pool.QueryRow(ctx, q, entityType, entityID, toPgtypeUUID(workspaceID)).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking outcome existence for %s %s: %w", entityType, entityID, err)
	}
	return exists, nil
}

// GetLatestForEntity returns the most recently created outcome for the given
// entity, workspace-scoped when non-nil. Returns ErrNotFound when no outcome
// exists yet.
func (s *Store) GetLatestForEntity(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (Outcome, error) {
	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE entity_type = $1
		  AND entity_id = $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY created_at DESC
		LIMIT 1`

	rows, err := s.pool.Query(ctx, q, entityType, entityID, toPgtypeUUID(workspaceID))
	if err != nil {
		return Outcome{}, fmt.Errorf("getting latest outcome for %s %s: %w", entityType, entityID, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Outcome{}, fmt.Errorf("getting latest outcome rows.Err: %w", err)
		}
		return Outcome{}, ErrNotFound
	}
	o, err := scanOutcomeRow(rows)
	if err != nil {
		return Outcome{}, fmt.Errorf("getting latest outcome scan: %w", err)
	}
	return o, nil
}

// dedupeUUIDsPreserveOrder returns ids with duplicate UUIDs removed,
// preserving first-occurrence order. FinalizeDraft's related_rule_ids merge
// SQL appends elements of this slice that aren't already present in the
// existing column; pre-deduping the input here (rather than in SQL, where
// `array_agg(DISTINCT x ORDER BY y)` requires y to be one of the aggregate's
// own arguments) keeps the query simple.
func dedupeUUIDsPreserveOrder(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// FinalizeDraft transitions a result='unknown' draft to (usually) a terminal
// result in place. The WHERE result='unknown' guard makes this race-safe: if
// the row no longer matches (already finalized by a concurrent caller),
// ErrDraftAlreadyFinalized is returned.
//
// Append-only merge semantics (user-directed redesign, migration 000075):
// Result and updated_at are always overwritten — that's the point of
// finalizing, and updated_at is the audit trail for every in-place write.
// Every other column is append-only, never a destructive replace:
//   - Notes: if the existing column is empty, the new text is written
//     directly; otherwise the new text is APPENDED after a "\n\n"
//     separator. Existing notes content can never be removed by this call.
//   - Metrics: only keys ABSENT from the existing jsonb are added — this is
//     `new || existing` (verified empirically: PG's jsonb `||` operator has
//     the RIGHT operand win on key conflicts, so putting the existing value
//     on the right makes existing values win while still admitting genuinely
//     new keys from the left). An existing key's value can never be
//     overwritten by this call — correcting it requires an explicit
//     supersede (a new row), not a draft enrich.
//   - RelatedRuleIDs: element-level UNION, not a whole-array replace —
//     existing IDs stay first in their original order, then any
//     caller-supplied IDs not already present are appended in the order
//     supplied (params pre-deduped in Go via dedupeUUIDsPreserveOrder).
//   - WorkSessionID: set-once — COALESCE(existing, new) means it can only be
//     written while still NULL; once set, no later call can re-point it.
//
// This closes the audit-trail-loss threat this function used to have: a
// prompt-injected record_outcome(result="unknown", notes=" ") call could
// previously overwrite real postmortem content with near-empty text (any
// non-empty string satisfied the old COALESCE-wins-when-non-empty rule).
// Under append semantics that same call can only ever ADD a whitespace note
// after the real content, never remove it.
func (s *Store) FinalizeDraft(ctx context.Context, id uuid.UUID, params CreateOutcomeParams) (Outcome, error) {
	const q = `
		UPDATE outcomes SET
			result = $1,
			metrics = CASE
				WHEN $2::jsonb IS NULL THEN metrics
				ELSE COALESCE($2::jsonb, '{}'::jsonb) || COALESCE(metrics, '{}'::jsonb)
			END,
			notes = CASE
				WHEN $3::text IS NULL THEN notes
				WHEN notes IS NULL OR notes = '' THEN $3
				ELSE notes || E'\n\n' || $3
			END,
			related_rule_ids = CASE
				WHEN array_length($4::uuid[], 1) IS NULL THEN related_rule_ids
				ELSE related_rule_ids || COALESCE((
					SELECT array_agg(nid ORDER BY ord)
					FROM unnest($4::uuid[]) WITH ORDINALITY AS u(nid, ord)
					WHERE nid <> ALL(related_rule_ids)
				), '{}'::uuid[])
			END,
			work_session_id = COALESCE(work_session_id, $5),
			updated_at = NOW()
		WHERE id = $6 AND result = 'unknown'
		RETURNING ` + outcomeSelectCols

	var notesArg pgtype.Text
	if params.Notes != "" {
		notesArg = pgtype.Text{String: params.Notes, Valid: true}
	}
	var metricsArg any
	if len(params.Metrics) > 0 {
		metricsArg = string(params.Metrics)
	}
	relatedRuleIDs := dedupeUUIDsPreserveOrder(params.RelatedRuleIDs)
	if relatedRuleIDs == nil {
		relatedRuleIDs = []uuid.UUID{}
	}

	rows, err := s.pool.Query(
		ctx, q,
		params.Result,
		metricsArg,
		notesArg,
		relatedRuleIDs,
		toPgtypeUUID(params.WorkSessionID),
		id,
	)
	if err != nil {
		return Outcome{}, fmt.Errorf("finalizing draft outcome %s: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Outcome{}, fmt.Errorf("finalizing draft outcome %s rows.Err: %w", id, err)
		}
		return Outcome{}, ErrDraftAlreadyFinalized
	}
	o, err := scanOutcomeRow(rows)
	if err != nil {
		return Outcome{}, fmt.Errorf("finalizing draft outcome %s scan: %w", id, err)
	}
	return o, nil
}

// SeedDraft atomically ensures a result='unknown' draft exists for the given
// entity. See StoreIface.SeedDraft doc comment for the full semantics: an
// existing outcome (draft or terminal) short-circuits to a no-op read;
// otherwise an INSERT ... ON CONFLICT DO NOTHING against the partial unique
// index idx_outcomes_one_open_draft (migration 000074) serializes concurrent
// first-time seeders.
func (s *Store) SeedDraft(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (Outcome, bool, error) {
	if latest, err := s.GetLatestForEntity(ctx, workspaceID, entityType, entityID); err == nil {
		return latest, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Outcome{}, false, fmt.Errorf("seeding draft outcome: checking existing: %w", err)
	}

	const insertQ = `
		INSERT INTO outcomes (workspace_id, entity_type, entity_id, result)
		VALUES ($1, $2, $3, 'unknown')
		ON CONFLICT (COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'::uuid), entity_type, entity_id)
		WHERE result = 'unknown'
		DO NOTHING
		RETURNING ` + outcomeSelectCols

	rows, err := s.pool.Query(ctx, insertQ, toPgtypeUUID(workspaceID), entityType, entityID)
	if err != nil {
		return Outcome{}, false, fmt.Errorf("seeding draft outcome: %w", err)
	}
	if rows.Next() {
		o, err := scanOutcomeRow(rows)
		rows.Close()
		if err != nil {
			return Outcome{}, false, fmt.Errorf("seeding draft outcome scan: %w", err)
		}
		return o, true, nil
	}
	rowsErr := rows.Err()
	rows.Close()
	if rowsErr != nil {
		return Outcome{}, false, fmt.Errorf("seeding draft outcome rows.Err: %w", rowsErr)
	}

	// Lost the race to a concurrent seeder — fetch and return the winner's row.
	existing, err := s.GetLatestForEntity(ctx, workspaceID, entityType, entityID)
	if err != nil {
		return Outcome{}, false, fmt.Errorf("seeding draft outcome: fetching post-conflict draft: %w", err)
	}
	return existing, false, nil
}
