package outcome

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

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
// extra optionally scans additional trailing RETURNING columns beyond
// outcomeSelectCols directly into caller-supplied destinations — used by
// FinalizeDraft to also read back the pre-truncation related_rule_ids count
// (PR #152 round 5 Major M-R5-1 guarantee A) in the SAME round trip, without
// duplicating this function's column-order-sensitive scan+convert logic.
func scanOutcomeRow(rows pgx.Rows, extra ...any) (Outcome, error) {
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
	dest := []any{
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
	}
	dest = append(dest, extra...)
	err := rows.Scan(dest...)
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
//
// ORDER BY carries a second key, id DESC, to break ties when two rows share
// a bit-identical created_at (security-review-reproduced: SQLite's created_at
// has millisecond granularity and ties are easy to hit; Postgres timestamptz
// can tie too under fast concurrent writes). Without this, "the latest
// outcome" was non-deterministic on a tie, which could resolve to an older
// row and make a newly-seeded draft permanently unreachable — every
// subsequent record_outcome call for that entity then hard-fails against
// idx_outcomes_one_open_draft. `id DESC` mirrors the tie-break migration
// 000074's dedup step already uses (migrations/000074_outcomes_supersession.
// up.sql) so both the one-time dedup and every live read agree on which row
// wins a tie.
func (s *Store) GetLatestForEntity(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (Outcome, error) {
	const q = `SELECT ` + outcomeSelectCols + ` FROM outcomes
		WHERE entity_type = $1
		  AND entity_id = $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY created_at DESC, id DESC
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

// DedupeUUIDsPreserveOrder returns ids with duplicate UUIDs removed,
// preserving first-occurrence order. FinalizeDraft's related_rule_ids merge
// SQL appends elements of this slice that aren't already present in the
// existing column; pre-deduping the input here (rather than in SQL, where
// `array_agg(DISTINCT x ORDER BY y)` requires y to be one of the aggregate's
// own arguments) keeps the query simple.
//
// Exported (PR #152 round 4 Major, finding F6): the MCP layer's
// parseRelatedRuleIDs (internal/mcp/tools_outcome.go) also calls this so a
// caller-supplied duplicate is deduped even on the FRESH-row CreateOutcome
// path (ActionCreated — no prior draft to union against), which neither
// backend's CreateOutcome ever deduped on its own. Calling it again here in
// FinalizeDraft is deliberate defense-in-depth, not redundancy: StoreIface
// is the actual interface contract, and any other current or future caller
// of FinalizeDraft that does NOT go through parseRelatedRuleIDs must not be
// able to reintroduce a duplicate into the stored array.
func DedupeUUIDsPreserveOrder(ids []uuid.UUID) []uuid.UUID {
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

// CapRelatedRuleIDs bounds merged (existing followed by newly-unioned IDs,
// per unionRelatedRuleIDs in the SQLite backend / the equivalent union in
// FinalizeDraft's PG SQL below) to at most cap entries, WITHOUT EVER
// dropping any element of existing — the absolute guarantee this package's
// FinalizeDraft doc comments make: "an already-linked rule ID can never be
// silently removed by a later call".
//
// PR #152 round 6 Major (m-R6-3): the PREVIOUS implementation (both
// backends, independently) sliced the merged array to exactly `cap`
// elements whenever it exceeded the cap — correct as long as
// len(existing) <= cap (always true under normal forward operation, since
// every write enforces the cap), but WRONG the moment existing already
// exceeded the cap for any reason (legacy data written before this cap
// existed is the realistic case; the round 6 second-army repro used
// existing=150, cap=100 to demonstrate it): slicing to `cap` in that state
// silently dropped 50 already-linked IDs, on a call that may not even have
// supplied any new ones. Two-army reproduction additionally found the two
// backends DISAGREED on a notes-only/zero-new-IDs call in that same
// over-cap state — PG happened to skip the slice entirely (its SQL CASE
// short-circuits when no new IDs are supplied) while SQLite applied the
// slice unconditionally — so the same input produced different stored
// state depending on backend, violating this project's dual-backend parity
// requirement.
//
// This function's rule instead keeps at least max(cap, len(existing))
// elements from the front of merged: when existing already fits within
// cap (the normal case), that is just `cap` — identical behaviour to the
// old code, so no regression for any call whose existing state is already
// within bounds. When existing already exceeds cap, existing is returned
// completely untouched and EVERY newly-appended candidate is dropped
// instead of letting an ordinary-looking enrich call quietly truncate
// pre-existing links. Both backends now compute this SAME rule (PG in SQL
// via `GREATEST(cap, existing_len)`, verified to produce byte-identical
// results to this function — see store_test.go /
// storage/sqlite/outcome_test.go's parity tests), closing both the
// silent-removal gap and the cross-backend disagreement in the same fix.
func CapRelatedRuleIDs(existing, merged []uuid.UUID, capLimit int) []uuid.UUID {
	if len(merged) <= capLimit {
		return merged
	}
	keep := capLimit
	if len(existing) > keep {
		keep = len(existing)
	}
	if keep >= len(merged) {
		return merged
	}
	return merged[:keep]
}

// CapNotesTotal applies the identical "never drop existing content, even if
// existing already exceeds the cap" rule as CapRelatedRuleIDs above, to the
// Notes field's cumulative-size cap (MaxNotesTotalRunes, PR #152 round 6
// Major M-R6-2 guarantee B). merged is the full appended value (existing +
// "\n\n" + incoming, or just incoming when existing is empty — see
// appendNotes / the PG notes CASE expression); existing is the pre-write
// Notes value. Operates on runes (not bytes), matching sanitize.Notes's own
// per-call cap semantics.
//
// Returns (merged, false) unchanged when merged already fits within cap —
// the overwhelmingly common case, since MaxNotesTotalRunes is enforced on
// every write from the moment it shipped, so existing can only ever exceed
// it via data that predates this cap (a smaller theoretical exposure than
// CapRelatedRuleIDs's, which had years of unbounded-growth history before
// this fix; still handled identically for defence-in-depth and cross-field
// design consistency). Returns (truncated, true) otherwise, where truncated
// keeps at least the first len(existing) runes intact (so appended-but-cut
// text is always the NEWLY-supplied tail, never a bite out of existing
// content) and reports true so the caller can log the truncation.
func CapNotesTotal(existing, merged string, capLimit int) (string, bool) {
	mergedRunes := []rune(merged)
	if len(mergedRunes) <= capLimit {
		return merged, false
	}
	keep := capLimit
	if existingLen := len([]rune(existing)); existingLen > keep {
		keep = existingLen
	}
	if keep >= len(mergedRunes) {
		return merged, false
	}
	return string(mergedRunes[:keep]), true
}

// FinalizeDraft transitions a result='unknown' draft to (usually) a terminal
// result in place. The WHERE result='unknown' guard makes this race-safe: if
// the row no longer matches (already finalized by a concurrent caller),
// ErrDraftAlreadyFinalized is returned.
//
// related_rule_ids / notes merge computation MUST be inline bare-column
// expressions in the SET clause, NEVER a WITH CTE (or any other subquery
// with its own FROM outcomes scan) that reads the same row (five-army
// repro, 🔴 C-DBI-1): under READ COMMITTED, when a second concurrent
// FinalizeDraft call is blocked on this row's lock and the first commits,
// Postgres's EvalPlanQual (EPQ) re-fetches the now-committed row and
// re-evaluates the UPDATE's own target-list expressions against it — but
// ONLY for expressions built directly from the target relation's own Vars
// (bare `related_rule_ids`, `notes`, etc., referenced with no separate FROM
// clause). A WITH CTE (or any subquery with `FROM outcomes ...`) is a
// SEPARATE plan node whose result was computed ONCE against the
// pre-wait snapshot and is NOT re-executed by EPQ, so the blocked writer's
// UPDATE silently unions against a stale (sometimes empty) array/notes
// value instead of the just-committed one — the other writer's
// already-committed related_rule_ids/notes are then overwritten outright,
// not just their newest tail. Reproduced with `postgres:16-alpine`, two
// concurrent sessions, and confirmed with both `MATERIALIZED` and
// `NOT MATERIALIZED` hints (byte-identical, wrong, result) — the hint is
// not the cause; "separate re-scan of the same table" is. Verified fixed
// (both `related_rule_ids` int[]-shaped and `notes` text-shaped append
// patterns) against a real two-session concurrent UPDATE before landing
// this rewrite. See TestStore_FinalizeDraft_RelatedRuleIDs_ConcurrentEnrich_
// BothSurvive below for the regression test; flipping this SET clause back
// to a CTE reproduces the failure.
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
//     supplied (params pre-deduped in Go via DedupeUUIDsPreserveOrder).
//   - WorkSessionID: set-once — COALESCE(existing, new) means it can only be
//     written while still NULL; once set, no later call can re-point it.
//
// This closes the audit-trail-loss threat this function used to have: a
// prompt-injected record_outcome(result="unknown", notes=" ") call could
// previously overwrite real postmortem content with near-empty text (any
// non-empty string satisfied the old COALESCE-wins-when-non-empty rule).
// Under append semantics that same call can only ever ADD a whitespace note
// after the real content, never remove it.
//
// related_rule_ids is additionally capped at MaxRelatedRuleIDsTotal AFTER
// the union above is computed (PR #152 round 5 Major M-R5-1 guarantee A;
// widened in round 6 Major m-R6-3 — see below): the SET clause's inline
// union expression is sliced to
// `[1:GREATEST(MaxRelatedRuleIDsTotal, existing_ids_len)]`, where
// existing_ids_len is itself read inline (`COALESCE(array_length(
// related_rule_ids, 1), 0)` — a bare reference, same EPQ-safety rule as
// above). Because existing IDs sort first in the union (see above) and the
// slice never keeps FEWER than existing_ids_len elements, an already-linked
// rule ID can NEVER be silently removed by this call, even in the
// (data-predates-the-cap) case where existing already exceeds the cap — see
// CapRelatedRuleIDs (above) for the Go-side equivalent of this exact
// algorithm, which this SQL is written to match byte-for-byte (verified by
// store_test.go/storage/sqlite/outcome_test.go's cross-backend parity
// tests). Similarly, notes is capped at MaxNotesTotalRunes via
// `LEFT(<appended text>, GREATEST(MaxNotesTotalRunes, existing_notes_len))`
// — the same rule, applied to a rune-counted string instead of an array
// (PR #152 round 6 Major M-R6-2 guarantee B; CapNotesTotal above is its Go
// equivalent for SQLite).
//
// Notes and related_rule_ids's cap logic ONLY runs when this call actually
// supplies new content in that field ($3 non-NULL / $4 non-empty
// respectively) — the CASE's WHEN-NULL/WHEN-empty branch above leaves the
// column completely untouched otherwise, so a call that never touches a
// field can never trigger that field's cap logic either.
//
// RETURNING's two trailing pre-truncation-length columns (used only to
// decide whether the slog.Warn below should fire) deliberately DO use a
// `FROM outcomes pre WHERE pre.id = $6` self-join instead of a bare
// reference: RETURNING for UPDATE always sees the NEW (post-assignment,
// already-capped) value for a bare `related_rule_ids`/`notes` reference —
// verified empirically — so the only way to read a not-yet-capped "would
// this call's merge have exceeded the cap" length inside RETURNING is a
// separate correlated read. This reintroduces the same "separate scan, not
// EPQ-refreshed" characteristic the SET clause fix above deliberately
// avoids, but ONLY for this diagnostic pair — it cannot cause data loss
// (the SET clause never reads from it), and it is no less precise under
// concurrent contention than this function's own pre-fix behaviour (which
// computed these same lengths from an equally non-EPQ-safe CTE). Under
// concurrent contention, the resulting slog.Warn may occasionally
// under/over-fire; it is documentation-grade diagnostics, not a
// correctness guarantee, and none of this function's acceptance
// guarantees depend on it.
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
				ELSE LEFT(
					CASE
						WHEN notes IS NULL OR notes = '' THEN $3
						ELSE notes || E'\n\n' || $3
					END,
					GREATEST($8::int, COALESCE(char_length(notes), 0))
				)
			END,
			related_rule_ids = CASE
				WHEN array_length($4::uuid[], 1) IS NULL THEN related_rule_ids
				ELSE (
					COALESCE(related_rule_ids, '{}'::uuid[]) || COALESCE((
						SELECT array_agg(nid ORDER BY ord)
						FROM unnest($4::uuid[]) WITH ORDINALITY AS u(nid, ord)
						WHERE nid <> ALL(COALESCE(related_rule_ids, '{}'::uuid[]))
					), '{}'::uuid[])
				)[1 : GREATEST($7::int, COALESCE(array_length(related_rule_ids, 1), 0))]
			END,
			work_session_id = COALESCE(work_session_id, $5),
			updated_at = NOW()
		WHERE id = $6 AND result = 'unknown'
		RETURNING ` + outcomeSelectCols + `,
			-- Diagnostic-only (see doc comment above): pre-truncation full
			-- union length, read via a self-join because RETURNING would
			-- otherwise show the already-capped NEW value for a bare
			-- related_rule_ids reference.
			(SELECT COALESCE(array_length(
				COALESCE(pre.related_rule_ids, '{}'::uuid[]) || COALESCE((
					SELECT array_agg(nid ORDER BY ord)
					FROM unnest($4::uuid[]) WITH ORDINALITY AS u(nid, ord)
					WHERE nid <> ALL(COALESCE(pre.related_rule_ids, '{}'::uuid[]))
				), '{}'::uuid[])
			, 1), 0) FROM outcomes pre WHERE pre.id = $6),
			-- Diagnostic-only: pre-truncation full notes length, same
			-- self-join rationale as above.
			(SELECT COALESCE(char_length(
				CASE
					WHEN $3::text IS NULL THEN pre.notes
					WHEN pre.notes IS NULL OR pre.notes = '' THEN $3
					ELSE pre.notes || E'\n\n' || $3
				END
			), 0) FROM outcomes pre WHERE pre.id = $6)`

	var notesArg pgtype.Text
	if params.Notes != "" {
		notesArg = pgtype.Text{String: params.Notes, Valid: true}
	}
	var metricsArg any
	if len(params.Metrics) > 0 {
		metricsArg = string(params.Metrics)
	}
	relatedRuleIDs := DedupeUUIDsPreserveOrder(params.RelatedRuleIDs)
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
		MaxRelatedRuleIDsTotal,
		MaxNotesTotalRunes,
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
	var preTruncateLen, preTruncateNotesLen int
	o, err := scanOutcomeRow(rows, &preTruncateLen, &preTruncateNotesLen)
	if err != nil {
		return Outcome{}, fmt.Errorf("finalizing draft outcome %s scan: %w", id, err)
	}
	// PR #152 round 6 Major (m-R6-4): the pre-truncate-count check alone is
	// NOT sufficient to know truncation actually happened — preTruncateLen
	// reflects the CTE's full_ids/full_notes value REGARDLESS of which
	// branch of the UPDATE's CASE was taken (e.g. a notes-only enrich call
	// against a row whose related_rule_ids already exceeded the cap took
	// the "WHEN array_length($4) IS NULL THEN related_rule_ids" passthrough
	// branch — the column was never touched — yet preTruncateLen still
	// reported the existing over-cap length, so the old
	// `preTruncateLen > cap` check alone fired a "truncated" warning for a
	// call that truncated nothing). Comparing the FINAL returned length
	// against preTruncateLen catches only genuine truncation, regardless of
	// which CASE branch produced it.
	if preTruncateLen > MaxRelatedRuleIDsTotal && len(o.RelatedRuleIDs) < preTruncateLen {
		slog.Warn("Store.FinalizeDraft: related_rule_ids exceeded cumulative cap, truncated",
			"outcome_id", id, "pre_truncate_count", preTruncateLen,
			"cap", MaxRelatedRuleIDsTotal, "final_count", len(o.RelatedRuleIDs))
	}
	if finalNotesLen := utf8.RuneCountInString(o.Notes); preTruncateNotesLen > MaxNotesTotalRunes && finalNotesLen < preTruncateNotesLen {
		slog.Warn("Store.FinalizeDraft: notes exceeded cumulative cap, truncated",
			"outcome_id", id, "pre_truncate_rune_count", preTruncateNotesLen,
			"cap", MaxNotesTotalRunes, "final_rune_count", finalNotesLen)
	}
	return o, nil
}

// SeedDraft atomically ensures a result='unknown' draft exists for the given
// entity. See StoreIface.SeedDraft doc comment for the full semantics: an
// existing outcome (draft or terminal) short-circuits to a no-op read;
// otherwise an INSERT ... ON CONFLICT DO NOTHING against the partial unique
// index idx_outcomes_one_open_draft (migration 000074) serializes concurrent
// first-time seeders.
//
// related_rule_ids is explicitly inserted as '{}'::uuid[] rather than left to
// default to NULL (migration 000063 leaves the column nullable with no
// DEFAULT). PR #152 round 4 Critical: FinalizeDraft's union SQL evaluates
// `nid <> ALL(related_rule_ids)` against the existing column — under SQL
// three-valued logic, `x <> ALL(NULL)` is NULL, not true, so every
// caller-supplied ID got filtered out and array_agg returned NULL, silently
// dropping the entire related_rule_ids payload on the FIRST finalize of any
// SeedDraft-seeded row (the production complete_task -> record_outcome path).
// CreateOutcome (this file, above) already avoids the same trap by writing
// '{}' instead of nil; SeedDraft now does too, so no NEW NULL is ever
// produced. FinalizeDraft's SQL additionally wraps every read of
// related_rule_ids in COALESCE(..., '{}'::uuid[]) as defence-in-depth against
// rows that predate this fix.
func (s *Store) SeedDraft(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (Outcome, bool, error) {
	if latest, err := s.GetLatestForEntity(ctx, workspaceID, entityType, entityID); err == nil {
		return latest, false, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Outcome{}, false, fmt.Errorf("seeding draft outcome: checking existing: %w", err)
	}

	const insertQ = `
		INSERT INTO outcomes (workspace_id, entity_type, entity_id, result, related_rule_ids)
		VALUES ($1, $2, $3, 'unknown', '{}'::uuid[])
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
