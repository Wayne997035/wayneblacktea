package behaviorrule

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed implementation of StoreIface.
// It uses raw pgx without sqlc so the behavior_rules table stays independent
// of the sqlc-generated query layer.
type Store struct {
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID // zero = unscoped (single-tenant legacy mode)
}

// New returns a Store backed by pool, scoped to the optional workspaceID.
// nil workspaceID = legacy unscoped mode.
func New(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{pool: pool, workspaceID: toUUID(workspaceID)}
}

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// selectCols is the explicit column list for SELECT queries.
const selectCols = `id, workspace_id, condition, action, source_type, source_id,
	confidence, status, created_at, updated_at`

// scanRule reads a full behavior_rules row into a BehaviorRule.
// confidence is NUMERIC(5,2) in Postgres; pgx scans it directly into float64.
func scanRule(scan func(...any) error) (*BehaviorRule, error) {
	var (
		r         BehaviorRule
		wsID      pgtype.UUID
		sourceID  pgtype.UUID
		createdAt pgtype.Timestamptz
		updatedAt pgtype.Timestamptz
	)
	if err := scan(
		&r.ID,
		&wsID,
		&r.Condition,
		&r.Action,
		&r.SourceType,
		&sourceID,
		&r.Confidence,
		&r.Status,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, fmt.Errorf("scanning behavior_rule: %w", err)
	}

	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes)
		r.WorkspaceID = &id
	}
	if sourceID.Valid {
		id := uuid.UUID(sourceID.Bytes)
		r.SourceID = &id
	}
	if createdAt.Valid {
		r.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		r.UpdatedAt = updatedAt.Time
	}
	return &r, nil
}

// Propose inserts a new behavior rule with status='proposed' and returns the
// persisted record.
func (s *Store) Propose(ctx context.Context, p CreateParams) (*BehaviorRule, error) {
	id := uuid.New()

	confidence := p.Confidence
	if confidence <= 0 || confidence > 1.0 {
		confidence = 0.50
	}

	var sourceID any
	if p.SourceID != nil {
		sourceID = *p.SourceID
	}

	// Use the param WorkspaceID when set; fall back to the store's scope.
	wsArg := s.workspaceID
	if p.WorkspaceID != nil {
		wsArg = toUUID(p.WorkspaceID)
	}

	const q = `
		INSERT INTO behavior_rules
			(id, workspace_id, condition, action, source_type, source_id, confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(
		ctx, q,
		id,
		wsArg,
		p.Condition,
		p.Action,
		p.SourceType,
		sourceID,
		confidence,
	)
	if err != nil {
		return nil, fmt.Errorf("proposing behavior_rule: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("proposing behavior_rule rows.Err: %w", err)
		}
		return nil, fmt.Errorf("proposing behavior_rule: no row returned")
	}
	r, err := scanRule(rows.Scan)
	if err != nil {
		return nil, fmt.Errorf("proposing behavior_rule after scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("proposing behavior_rule rows.Err post-scan: %w", err)
	}
	return r, nil
}

// List returns behavior rules matching the filter, ordered by created_at DESC.
func (s *Store) List(ctx context.Context, p ListParams) ([]*BehaviorRule, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}

	var statusArg any
	if p.Status != nil {
		statusArg = *p.Status
	}

	wsArg := s.workspaceID
	if p.WorkspaceID != nil {
		wsArg = toUUID(p.WorkspaceID)
	}

	const q = `SELECT ` + selectCols + ` FROM behavior_rules
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		  AND ($2::text IS NULL OR status = $2)
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, wsArg, statusArg, limit)
	if err != nil {
		return nil, fmt.Errorf("listing behavior_rules: %w", err)
	}
	defer rows.Close()

	var out []*BehaviorRule
	for rows.Next() {
		r, err := scanRule(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("listing behavior_rules scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing behavior_rules rows.Err: %w", err)
	}
	return out, nil
}

// ApplyOutcome applies the confidence formula atomically.
// A single UPDATE...RETURNING avoids the read-then-update race condition.
// SECURITY: workspace-scoped — a store scoped to workspace A cannot apply an
// outcome to a rule owned by workspace B, matching List's scoping (§ store
// scoping audit, aligns with the workspace predicate already used by List).
func (s *Store) ApplyOutcome(ctx context.Context, id uuid.UUID, outcome string) (*BehaviorRule, error) {
	var q string
	switch outcome {
	case "success":
		// If status='proposed' → transition to 'active' AND bump confidence.
		// If status='active'   → bump confidence only.
		// Other statuses: bump confidence only (no status change).
		q = `UPDATE behavior_rules
			SET
				status = CASE WHEN status = 'proposed' THEN 'active' ELSE status END,
				confidence = LEAST(confidence + 0.05, 1.00),
				updated_at = NOW()
			WHERE id = $1
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			RETURNING ` + selectCols
	case "failure":
		q = `UPDATE behavior_rules
			SET
				confidence = GREATEST(confidence - 0.10, 0.00),
				updated_at = NOW()
			WHERE id = $1
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			RETURNING ` + selectCols
	default:
		return nil, fmt.Errorf("behaviorrule: invalid outcome %q; must be 'success' or 'failure'", outcome)
	}

	rows, err := s.pool.Query(ctx, q, id, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("applying outcome to behavior_rule: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("applying outcome rows.Err: %w", err)
		}
		return nil, ErrNotFound
	}
	r, err := scanRule(rows.Scan)
	if err != nil {
		return nil, fmt.Errorf("applying outcome scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("applying outcome rows.Err post-scan: %w", err)
	}
	return r, nil
}

// Deprecate sets the rule's status to 'deprecated'. Idempotent.
// Returns ErrNotFound when no matching rule exists.
// SECURITY: workspace-scoped — a store scoped to workspace A cannot deprecate
// a rule owned by workspace B, matching List's scoping.
func (s *Store) Deprecate(ctx context.Context, id uuid.UUID) (*BehaviorRule, error) {
	const q = `UPDATE behavior_rules
		SET status = 'deprecated', updated_at = NOW()
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(ctx, q, id, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("deprecating behavior_rule: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("deprecating behavior_rule rows.Err: %w", err)
		}
		return nil, ErrNotFound
	}
	r, err := scanRule(rows.Scan)
	if err != nil {
		return nil, fmt.Errorf("deprecating behavior_rule scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("deprecating behavior_rule rows.Err post-scan: %w", err)
	}
	return r, nil
}

// PruneOlderThan hard-deletes behavior_rules rows where
// status IN ('rejected','deprecated') AND created_at < cutoff.
// Active and proposed rows are NEVER deleted regardless of age.
func (s *Store) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM behavior_rules
		WHERE status IN ('rejected','deprecated') AND created_at < $1`
	tag, err := s.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning behavior_rules: %w", err)
	}
	return tag.RowsAffected(), nil
}
