package playbook

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed implementation of StoreIface.
// It uses raw pgx without sqlc so the playbooks table stays independent of
// the sqlc-generated query layer.
type Store struct {
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID // zero = unscoped (single-tenant legacy mode)
}

// NewStore returns a Store backed by pool, scoped to the optional workspaceID.
// nil workspaceID = legacy unscoped mode.
func NewStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{pool: pool, workspaceID: toUUID(workspaceID)}
}

// Compile-time assertion: Store must satisfy StoreIface.
var _ StoreIface = (*Store)(nil)

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// selectCols is the explicit column list used for SELECT queries.
const selectCols = `id, workspace_id, trigger_pattern, action_template,
	source_decision_ids, confidence, hits, last_used_at, created_at, updated_at`

// scanPlaybook reads a full playbooks row into a Playbook.
func scanPlaybook(scan func(...any) error) (*Playbook, error) {
	var (
		p          Playbook
		wsID       pgtype.UUID
		srcIDs     []uuid.UUID
		confidence pgtype.Numeric
		lastUsedAt pgtype.Timestamptz
		createdAt  pgtype.Timestamptz
		updatedAt  pgtype.Timestamptz
	)
	if err := scan(
		&p.ID,
		&wsID,
		&p.TriggerPattern,
		&p.ActionTemplate,
		&srcIDs,
		&confidence,
		&p.Hits,
		&lastUsedAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, fmt.Errorf("scanning playbook: %w", err)
	}

	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes)
		p.WorkspaceID = &id
	}
	p.SourceDecisionIDs = srcIDs
	if srcIDs == nil {
		p.SourceDecisionIDs = []uuid.UUID{}
	}
	if confidence.Valid {
		f, err := confidence.Float64Value()
		if err == nil && f.Valid {
			p.Confidence = f.Float64
		}
	}
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		p.LastUsedAt = &t
	}
	if createdAt.Valid {
		p.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		p.UpdatedAt = updatedAt.Time
	}
	return &p, nil
}

// Create inserts a new playbook and returns the persisted record.
func (s *Store) Create(ctx context.Context, p CreateParams) (*Playbook, error) {
	id := uuid.New()
	confidence := p.Confidence
	if confidence == 0 {
		confidence = 0.50
	}
	srcIDs := p.SourceDecisionIDs
	if srcIDs == nil {
		srcIDs = []uuid.UUID{}
	}

	// Build a NUMERIC literal for confidence so pgx does not need special
	// NUMERIC marshaling on the parameter side.
	const q = `
		INSERT INTO playbooks
			(id, workspace_id, trigger_pattern, action_template,
			 source_decision_ids, confidence)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(ctx, q,
		id,
		s.workspaceID,
		p.TriggerPattern,
		p.ActionTemplate,
		srcIDs,
		confidence,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting playbook: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("inserting playbook rows.Err: %w", err)
		}
		return nil, fmt.Errorf("inserting playbook: no row returned")
	}
	pb, err := scanPlaybook(rows.Scan)
	if err != nil {
		return nil, fmt.Errorf("inserting playbook after scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inserting playbook rows.Err post-scan: %w", err)
	}
	return pb, nil
}

// List returns playbooks matching the filter, ordered by confidence DESC.
// If ContextKeywords is non-empty, each keyword adds an OR'd ILIKE clause.
func (s *Store) List(ctx context.Context, p ListParams) ([]*Playbook, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}

	args := []any{s.workspaceID}
	whereParts := []string{"($1::uuid IS NULL OR workspace_id = $1)"}

	if len(p.ContextKeywords) > 0 {
		kwParts := make([]string, 0, len(p.ContextKeywords))
		for _, kw := range p.ContextKeywords {
			if kw == "" {
				continue
			}
			args = append(args, "%"+kw+"%")
			pos := len(args)
			kwParts = append(kwParts, fmt.Sprintf(
				"(trigger_pattern ILIKE $%d OR action_template ILIKE $%d)", pos, pos,
			))
		}
		if len(kwParts) > 0 {
			whereParts = append(whereParts, "("+strings.Join(kwParts, " OR ")+")")
		}
	}

	args = append(args, limit)
	where := "WHERE " + strings.Join(whereParts, " AND ")
	q := `SELECT ` + selectCols + ` FROM playbooks ` + where +
		fmt.Sprintf(` ORDER BY confidence DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing playbooks: %w", err)
	}
	defer rows.Close()

	var out []*Playbook
	for rows.Next() {
		pb, err := scanPlaybook(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("listing playbooks scan: %w", err)
		}
		out = append(out, pb)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing playbooks rows.Err: %w", err)
	}
	return out, nil
}

// IncrementHits atomically increments the hits counter and sets last_used_at
// to NOW() for the given playbook id.
func (s *Store) IncrementHits(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE playbooks
		SET hits = hits + 1,
		    last_used_at = $3,
		    updated_at   = $3
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)`

	now := time.Now().UTC()
	tag, err := s.pool.Exec(ctx, q, id, s.workspaceID, now)
	if err != nil {
		return fmt.Errorf("increment playbook hits %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
