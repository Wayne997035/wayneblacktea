package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// outcomeSelectCols includes related_rule_ids added in migration 000063,
	// work_session_id added in migration 000067, supersedes_id added in
	// migration 000074, and updated_at added in migration 000075.
	outcomeSelectCols = `id, workspace_id, entity_type, entity_id, result, metrics, notes, ` +
		`related_rule_ids, work_session_id, supersedes_id, created_at, updated_at`
	evaluationSelectCols = `id, workspace_id, outcome_id, analysis, lessons, improvement_suggestions, created_at`
)

type outcomeRawRow struct {
	idStr          string
	wsIDNS         sql.NullString
	entityType     string
	entityIDStr    string
	result         string
	metricsNS      sql.NullString
	notesNS        sql.NullString
	relatedRuleIDs string // JSON array stored as TEXT, e.g. '["uuid1","uuid2"]'
	workSessionNS  sql.NullString
	supersedesNS   sql.NullString
	createdNS      sql.NullString
	updatedNS      sql.NullString
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
		&r.relatedRuleIDs,
		&r.workSessionNS,
		&r.supersedesNS,
		&r.createdNS,
		&r.updatedNS,
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
	// Decode related_rule_ids from JSON TEXT using the shared helper in playbooks.go.
	o.RelatedRuleIDs = parseUUIDSlice(r.relatedRuleIDs)
	o.WorkSessionID = nullStringToUUIDPtr(r.workSessionNS)
	o.SupersedesID = nullStringToUUIDPtr(r.supersedesNS)
	if t := parseTimestamptz(r.createdNS); t.Valid {
		o.CreatedAt = t.Time
	}
	if t := parseTimestamptz(r.updatedNS); t.Valid {
		o.UpdatedAt = t.Time
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
		(id, workspace_id, entity_type, entity_id, result, metrics, notes,
		 related_rule_ids, work_session_id, supersedes_id, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12)`

	var metricsArg any
	if len(params.Metrics) > 0 {
		metricsArg = string(params.Metrics)
	}
	var notesArg any
	if params.Notes != "" {
		notesArg = params.Notes
	}
	// Encode related_rule_ids using the shared helper (playbooks.go:109).
	relatedRuleIDsStr := encodeUUIDSlice(params.RelatedRuleIDs)

	_, err := s.db.conn.ExecContext(
		ctx, q,
		id.String(),
		nullStringFromUUID(params.WorkspaceID),
		params.EntityType,
		params.EntityID.String(),
		params.Result,
		metricsArg,
		notesArg,
		relatedRuleIDsStr,
		nullStringFromUUID(params.WorkSessionID),
		nullStringFromUUID(params.SupersedesID),
		now,
		// UpdatedAt starts equal to CreatedAt — a freshly-inserted row has
		// never been modified in place (migration 000075 doc comment).
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

	_, err := s.db.conn.ExecContext(
		ctx, q,
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

// ExistsForEntity reports whether an outcome row already exists for the given
// entity, optionally scoped to workspaceID. Mirrors the outcome.Store (PG)
// implementation.
func (s *OutcomeStore) ExistsForEntity(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (bool, error) {
	var wsArg any
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}
	const q = `SELECT EXISTS(
		SELECT 1 FROM outcomes
		WHERE entity_type = ?1
		  AND entity_id = ?2
		  AND (?3 IS NULL OR workspace_id = ?3)
	)`
	var exists int
	row := s.db.conn.QueryRowContext(ctx, q, entityType, entityID.String(), wsArg)
	if err := row.Scan(&exists); err != nil {
		return false, errWrap("OutcomeStore.ExistsForEntity", err)
	}
	return exists != 0, nil
}

// GetLatestForEntity returns the most recently created outcome for the given
// entity, workspace-scoped when non-nil. Returns outcome.ErrNotFound when no
// outcome exists yet. Mirrors the outcome.Store (PG) implementation.
//
// ORDER BY carries a second key, id DESC, to break ties when two rows share
// a bit-identical created_at (security-review-reproduced: SQLite's TEXT
// created_at has millisecond granularity and ties are easy to hit in fast
// call sequences). Without this, "the latest outcome" was non-deterministic
// on a tie, which could resolve to an older row and make a newly-seeded
// draft permanently unreachable — every subsequent record_outcome call for
// that entity then hard-fails against idx_outcomes_one_open_draft. `id DESC`
// mirrors the tie-break migrations/sqlite/000074_outcomes_supersession.up.sql's
// dedup step already uses, so both the one-time dedup and every live read
// agree on which row wins a tie.
func (s *OutcomeStore) GetLatestForEntity(
	ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID,
) (outcome.Outcome, error) {
	var wsArg any
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}
	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE entity_type = ?1
		  AND entity_id = ?2
		  AND (?3 IS NULL OR workspace_id = ?3)
		ORDER BY created_at DESC, id DESC
		LIMIT 1`

	row := s.db.conn.QueryRowContext(ctx, q, entityType, entityID.String(), wsArg)
	r, err := scanOutcomeRawRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return outcome.Outcome{}, outcome.ErrNotFound
	}
	if err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.GetLatestForEntity", err)
	}
	return parseOutcomeRow(r), nil
}

// mergeMetricsPreferExisting merges incoming JSON object bytes into existing,
// adding only keys ABSENT from existing — an existing key's value is never
// overwritten. Mirrors outcome.Store (PG)'s `new || existing` jsonb merge
// (existing on the right of `||` wins conflicts; verified empirically — see
// FinalizeDraft's own doc comment). Returns existing unchanged (nil error)
// when incoming is empty; json.RawMessage values are used instead of
// map[string]any so a value is preserved byte-for-byte rather than being
// lossily round-tripped through Go's float64 number representation.
func mergeMetricsPreferExisting(existing, incoming []byte) ([]byte, error) {
	if len(incoming) == 0 {
		return existing, nil
	}
	var incomingMap map[string]json.RawMessage
	if err := json.Unmarshal(incoming, &incomingMap); err != nil {
		return nil, fmt.Errorf("unmarshal incoming metrics: %w", err)
	}
	merged := make(map[string]json.RawMessage, len(incomingMap))
	for k, v := range incomingMap {
		merged[k] = v
	}
	if len(existing) > 0 {
		var existingMap map[string]json.RawMessage
		if err := json.Unmarshal(existing, &existingMap); err != nil {
			return nil, fmt.Errorf("unmarshal existing metrics: %w", err)
		}
		for k, v := range existingMap {
			merged[k] = v // existing wins on conflicting keys
		}
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, fmt.Errorf("marshal merged metrics: %w", err)
	}
	return out, nil
}

// appendNotes implements the notes append rule: empty existing content is
// replaced directly (nothing to append to yet); non-empty existing content
// gets incoming appended after a "\n\n" separator. Existing content can
// never be removed. Mirrors outcome.Store (PG)'s notes CASE expression.
func appendNotes(existing, incoming string) string {
	if incoming == "" {
		return existing
	}
	if existing == "" {
		return incoming
	}
	return existing + "\n\n" + incoming
}

// unionRelatedRuleIDs returns existing followed by any incoming IDs not
// already present in existing, deduped, preserving existing's original
// order first and incoming's supplied order for the appended tail. Mirrors
// outcome.Store (PG)'s `related_rule_ids || (new IDs not already present)`.
func unionRelatedRuleIDs(existing, incoming []uuid.UUID) []uuid.UUID {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[uuid.UUID]bool, len(existing)+len(incoming))
	out := make([]uuid.UUID, 0, len(existing)+len(incoming))
	for _, id := range existing {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range incoming {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// FinalizeDraft transitions a result='unknown' draft to (usually) a terminal
// result in place. Mirrors outcome.Store (PG)'s append-only merge semantics
// (migration 000075, user-directed redesign — see that Store's own doc
// comment for the full field-by-field rule and threat-model rationale): only
// Result and UpdatedAt are unconditionally overwritten; Notes/Metrics/
// RelatedRuleIDs/WorkSessionID are append-only and can never lose existing
// content.
//
// Unlike the PG version, this merge is computed in Go rather than in SQL
// (SQLite's metrics/related_rule_ids columns are TEXT-encoded JSON, not
// native jsonb/array types — modernc.org/sqlite v1.50.0 DOES have JSON1
// compiled in, including json_patch, verified empirically, but doing the
// merge in Go keeps this in one place with the transaction below rather than
// splitting logic between SQL functions and Go transaction control; "兩後端
// 語意等價,寫法可以不同" — see dispatch). That requires reading the
// existing row before computing the merged values, which (MIN-2 fix) is
// wrapped in a single BEGIN IMMEDIATE-equivalent serializable transaction —
// the same lost-update-prevention pattern already used by
// gtd.go's AddChecklistItem/UpdateChecklistItem — so no concurrent writer
// can be observed between the read and the write. The final UPDATE...
// RETURNING (modernc.org/sqlite supports RETURNING, verified empirically)
// reads back the committed row in the SAME statement, closing the
// two-separate-statements race the old Exec-then-getByID pattern had (a
// concurrent writer's content could previously leak into what this call
// returned as "what I wrote").
//
// related_rule_ids is additionally capped at outcome.MaxRelatedRuleIDsTotal
// via outcome.CapRelatedRuleIDs AFTER unionRelatedRuleIDs computes the full
// merge (PR #152 round 5 Major M-R5-1 guarantee A; widened in round 6 Major
// m-R6-3 to also cover the case where existing already exceeds the cap —
// see CapRelatedRuleIDs's doc comment for the full rule and the
// cross-backend-disagreement bug this closes). Notes is likewise capped at
// outcome.MaxNotesTotalRunes via outcome.CapNotesTotal (PR #152 round 6
// Major M-R6-2 guarantee B) — both cap helpers live in
// internal/outcome/store.go so the SAME algorithm backs both this Go
// implementation and Store (PG)'s SQL equivalent, verified byte-identical
// by this file's and store_test.go's parity tests.
//
// Workspace-scoped (PR #152 round 8, 80cf80b6 finding 3): mirrors
// outcome.Store.FinalizeDraft's PG fix — the SELECT and UPDATE below both
// now carry `(?N IS NULL OR workspace_id = ?N)` alongside `id = ...`, the
// same unscoped-when-nil pattern GetLatestForEntity already uses elsewhere
// in this file. params.WorkspaceID == nil (every pre-existing FinalizeDraft
// test in outcome_test.go) still matches any row, unchanged from before; a
// mismatched non-nil WorkspaceID now makes both statements match zero rows,
// surfacing as ErrDraftAlreadyFinalized instead of writing into another
// workspace's draft.
func (s *OutcomeStore) FinalizeDraft(ctx context.Context, id uuid.UUID, params outcome.CreateOutcomeParams) (outcome.Outcome, error) {
	tx, err := s.db.conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.FinalizeDraft begin", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	wsArg := nullStringFromUUID(params.WorkspaceID)

	const selectQ = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE id = ?1 AND result = 'unknown'
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	row := tx.QueryRowContext(ctx, selectQ, id.String(), wsArg)
	r, err := scanOutcomeRawRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return outcome.Outcome{}, outcome.ErrDraftAlreadyFinalized
	}
	if err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.FinalizeDraft select", err)
	}
	existing := parseOutcomeRow(r)

	mergedMetrics, err := mergeMetricsPreferExisting(existing.Metrics, params.Metrics)
	if err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.FinalizeDraft merge metrics", err)
	}
	mergedNotes := appendNotes(existing.Notes, params.Notes)
	mergedRelatedRuleIDs := unionRelatedRuleIDs(existing.RelatedRuleIDs, params.RelatedRuleIDs)

	// Cap related_rule_ids at outcome.MaxRelatedRuleIDsTotal via
	// CapRelatedRuleIDs (PR #152 round 5 Major M-R5-1 guarantee A; round 6
	// Major m-R6-3): unlike the old unconditional
	// `if len(merged) > cap { merged = merged[:cap] }`, CapRelatedRuleIDs
	// never drops any element of existing.RelatedRuleIDs even when existing
	// already exceeds the cap (legacy data) — see its doc comment. This is
	// also what closes m-R6-3's cross-backend disagreement: Store (PG)'s SQL
	// already skipped truncation entirely for a notes-only/zero-new-IDs
	// call in that state, while this file's old unconditional truncation
	// did not — CapRelatedRuleIDs is called unconditionally here too (no
	// `len(params.RelatedRuleIDs) > 0` guard needed) because it already
	// no-ops correctly by itself when merged == existing (the zero-new-IDs
	// case, per unionRelatedRuleIDs's own early return above).
	cappedRelatedRuleIDs := outcome.CapRelatedRuleIDs(existing.RelatedRuleIDs, mergedRelatedRuleIDs, outcome.MaxRelatedRuleIDsTotal)
	if len(cappedRelatedRuleIDs) < len(mergedRelatedRuleIDs) {
		slog.Warn("OutcomeStore.FinalizeDraft: related_rule_ids exceeded cumulative cap, truncated",
			"outcome_id", id, "supplied_count", len(mergedRelatedRuleIDs),
			"cap", outcome.MaxRelatedRuleIDsTotal, "final_count", len(cappedRelatedRuleIDs))
	}
	mergedRelatedRuleIDs = cappedRelatedRuleIDs

	// Cap Notes at outcome.MaxNotesTotalRunes via CapNotesTotal (PR #152
	// round 6 Major M-R6-2 guarantee B) — same "never drop existing content"
	// rule as CapRelatedRuleIDs above, applied to the rune-counted string.
	// Called unconditionally for the same reason: it no-ops correctly when
	// params.Notes == "" (mergedNotes == existing.Notes, per appendNotes's
	// own early return).
	if cappedNotes, truncated := outcome.CapNotesTotal(existing.Notes, mergedNotes, outcome.MaxNotesTotalRunes); truncated {
		slog.Warn("OutcomeStore.FinalizeDraft: notes exceeded cumulative cap, truncated",
			"outcome_id", id, "supplied_rune_count", len([]rune(mergedNotes)),
			"cap", outcome.MaxNotesTotalRunes, "final_rune_count", len([]rune(cappedNotes)))
		mergedNotes = cappedNotes
	}
	mergedWorkSessionID := existing.WorkSessionID
	if mergedWorkSessionID == nil {
		mergedWorkSessionID = params.WorkSessionID
	}
	now := nowRFC3339()

	var metricsArg any
	if len(mergedMetrics) > 0 {
		metricsArg = string(mergedMetrics)
	}
	var notesArg any
	if mergedNotes != "" {
		notesArg = mergedNotes
	}

	const updateQ = `UPDATE outcomes SET
			result = ?1,
			metrics = ?2,
			notes = ?3,
			related_rule_ids = ?4,
			work_session_id = ?5,
			updated_at = ?6
		WHERE id = ?7 AND result = 'unknown'
		  AND (?8 IS NULL OR workspace_id = ?8)
		RETURNING ` + outcomeSelectCols

	row2 := tx.QueryRowContext(
		ctx, updateQ,
		params.Result,
		metricsArg,
		notesArg,
		encodeUUIDSlice(mergedRelatedRuleIDs),
		nullStringFromUUID(mergedWorkSessionID),
		now,
		id.String(),
		wsArg,
	)
	r2, err := scanOutcomeRawRow(row2.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		// Lost a race between the SELECT above and this UPDATE — should be
		// unreachable given the serializable tx + single-connection pool
		// (db.go SetMaxOpenConns(1)), but treated defensively as
		// already-finalized rather than silently returning a stale row.
		return outcome.Outcome{}, outcome.ErrDraftAlreadyFinalized
	}
	if err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.FinalizeDraft update", err)
	}

	if err := tx.Commit(); err != nil {
		return outcome.Outcome{}, errWrap("OutcomeStore.FinalizeDraft commit", err)
	}
	committed = true
	return parseOutcomeRow(r2), nil
}

// SeedDraft atomically ensures a result='unknown' draft exists for the given
// entity. See outcome.StoreIface.SeedDraft doc comment for the full
// semantics. Uses ExecContext + RowsAffected (rather than a RETURNING
// clause) for the INSERT ... ON CONFLICT DO NOTHING step, consistent with
// this file's other upsert call sites (e.g. worksession.go's
// work_session_tasks link insert).
func (s *OutcomeStore) SeedDraft(
	ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID,
) (outcome.Outcome, bool, error) {
	if latest, err := s.GetLatestForEntity(ctx, workspaceID, entityType, entityID); err == nil {
		return latest, false, nil
	} else if !errors.Is(err, outcome.ErrNotFound) {
		return outcome.Outcome{}, false, errWrap("OutcomeStore.SeedDraft checking existing", err)
	}

	id := uuid.New()
	now := nowRFC3339()
	// updated_at is supplied explicitly, equal to created_at (mirrors
	// CreateOutcome's own "UpdatedAt starts equal to CreatedAt" rule above).
	// PR #152 round 4 Major: SeedDraft is a THIRD writer of this table besides
	// CreateOutcome and FinalizeDraft — the migrations/sqlite/000075 comment
	// claiming those two "always supply an explicit value on every write" (so
	// the column is "never actually NULL in practice") was wrong; SeedDraft
	// left it unset, producing a zero-value time.Time (SQLite NULL, parsed as
	// Go's zero time) on every seeded draft until this fix.
	const insertQ = `INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, 'unknown', ?5, ?6)
		ON CONFLICT (COALESCE(workspace_id, '00000000-0000-0000-0000-000000000000'), entity_type, entity_id)
		WHERE result = 'unknown'
		DO NOTHING`

	res, err := s.db.conn.ExecContext(
		ctx, insertQ,
		id.String(),
		nullStringFromUUID(workspaceID),
		entityType,
		entityID.String(),
		now,
		now,
	)
	if err != nil {
		return outcome.Outcome{}, false, errWrap("OutcomeStore.SeedDraft insert", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return outcome.Outcome{}, false, errWrap("OutcomeStore.SeedDraft rows affected", err)
	}
	if n > 0 {
		created, err := s.getByID(ctx, id)
		if err != nil {
			return outcome.Outcome{}, false, errWrap("OutcomeStore.SeedDraft fetch inserted", err)
		}
		return created, true, nil
	}

	// Lost the race to a concurrent seeder — fetch and return the winner's row.
	existing, err := s.GetLatestForEntity(ctx, workspaceID, entityType, entityID)
	if err != nil {
		return outcome.Outcome{}, false, errWrap("OutcomeStore.SeedDraft fetching post-conflict draft", err)
	}
	return existing, false, nil
}
