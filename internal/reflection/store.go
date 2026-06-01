package reflection

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed implementation of StoreIface.
// It uses raw pgx without sqlc so the reflections table stays independent of
// the sqlc-generated query layer.
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
const selectCols = `id, workspace_id, type, related_entity_type, related_entity_id,
	summary, insights, patterns_detected, suggested_actions, confidence, created_at`

// scanReflection reads a full reflections row into a Reflection.
func scanReflection(scan func(...any) error) (*Reflection, error) {
	var (
		r                 Reflection
		wsID              pgtype.UUID
		relatedEntityType pgtype.Text
		relatedEntityID   pgtype.UUID
		insightsRaw       []byte
		patternsRaw       []byte
		actionsRaw        []byte
		createdAt         pgtype.Timestamptz
	)
	if err := scan(
		&r.ID,
		&wsID,
		&r.Type,
		&relatedEntityType,
		&relatedEntityID,
		&r.Summary,
		&insightsRaw,
		&patternsRaw,
		&actionsRaw,
		&r.Confidence,
		&createdAt,
	); err != nil {
		return nil, fmt.Errorf("scanning reflection: %w", err)
	}

	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes)
		r.WorkspaceID = &id
	}
	if relatedEntityType.Valid && relatedEntityType.String != "" {
		s := relatedEntityType.String
		r.RelatedEntityType = &s
	}
	if relatedEntityID.Valid {
		id := uuid.UUID(relatedEntityID.Bytes)
		r.RelatedEntityID = &id
	}
	if len(insightsRaw) > 0 {
		r.Insights = json.RawMessage(insightsRaw)
	}
	if len(patternsRaw) > 0 {
		r.PatternsDetected = json.RawMessage(patternsRaw)
	}
	if len(actionsRaw) > 0 {
		r.SuggestedActions = json.RawMessage(actionsRaw)
	}
	if createdAt.Valid {
		r.CreatedAt = createdAt.Time
	}
	return &r, nil
}

// jsonOrNull returns a value suitable for a JSONB column parameter: the raw
// bytes if non-nil, otherwise nil (SQL NULL).
func jsonOrNull(v json.RawMessage) any {
	if len(v) == 0 {
		return nil
	}
	return []byte(v)
}

// Create inserts a new reflection and returns the persisted record.
func (s *Store) Create(ctx context.Context, p CreateParams) (*Reflection, error) {
	id := uuid.New()
	var relatedEntityType any
	if p.RelatedEntityType != nil {
		relatedEntityType = *p.RelatedEntityType
	}
	var relatedEntityID any
	if p.RelatedEntityID != nil {
		relatedEntityID = *p.RelatedEntityID
	}

	const q = `
		INSERT INTO reflections
			(id, workspace_id, type, related_entity_type, related_entity_id,
			 summary, insights, patterns_detected, suggested_actions, confidence)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(ctx, q,
		id,
		s.workspaceID,
		p.Type,
		relatedEntityType,
		relatedEntityID,
		p.Summary,
		jsonOrNull(p.Insights),
		jsonOrNull(p.PatternsDetected),
		jsonOrNull(p.SuggestedActions),
		p.Confidence,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting reflection: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("inserting reflection rows.Err: %w", err)
		}
		return nil, fmt.Errorf("inserting reflection: no row returned")
	}
	r, err := scanReflection(rows.Scan)
	if err != nil {
		return nil, fmt.Errorf("inserting reflection after scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inserting reflection rows.Err post-scan: %w", err)
	}
	return r, nil
}

// List returns reflections matching the filter, ordered by created_at DESC.
func (s *Store) List(ctx context.Context, p ListParams) ([]*Reflection, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}

	var typeArg any
	if p.Type != nil {
		typeArg = *p.Type
	}

	// Honor an explicit per-call WorkspaceID override, falling back to the
	// store's scope. Mirrors the SQLite ReflectionStore.List behavior so both
	// backends filter identically (dual-backend parity).
	wsArg := s.workspaceID
	if p.WorkspaceID != nil {
		wsArg = toUUID(p.WorkspaceID)
	}

	const q = `SELECT ` + selectCols + ` FROM reflections
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		  AND ($2::text IS NULL OR type = $2)
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, wsArg, typeArg, limit)
	if err != nil {
		return nil, fmt.Errorf("listing reflections: %w", err)
	}
	defer rows.Close()

	var out []*Reflection
	for rows.Next() {
		r, err := scanReflection(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("listing reflections scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing reflections rows.Err: %w", err)
	}
	return out, nil
}

// GetLatest returns the most recent reflection of a given type in the workspace.
// Returns ErrNotFound when no matching reflection exists.
func (s *Store) GetLatest(ctx context.Context, workspaceID *uuid.UUID, reflType string) (*Reflection, error) {
	const q = `SELECT ` + selectCols + ` FROM reflections
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		  AND type = $2
		ORDER BY created_at DESC
		LIMIT 1`

	rows, err := s.pool.Query(ctx, q, toUUID(workspaceID), reflType)
	if err != nil {
		return nil, fmt.Errorf("get latest reflection: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("get latest reflection rows.Err: %w", err)
		}
		return nil, ErrNotFound
	}
	r, err := scanReflection(rows.Scan)
	if err != nil {
		return nil, fmt.Errorf("get latest reflection scan: %w", err)
	}
	return r, nil
}

// ByRelatedEntity returns reflections scoped to a specific related entity,
// ordered by created_at DESC, capped at limit rows.
func (s *Store) ByRelatedEntity(
	ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID, limit int,
) ([]*Reflection, error) {
	if limit <= 0 {
		limit = 20
	}

	const q = `SELECT ` + selectCols + ` FROM reflections
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		  AND related_entity_type = $2
		  AND related_entity_id = $3
		ORDER BY created_at DESC
		LIMIT $4`

	rows, err := s.pool.Query(ctx, q, toUUID(workspaceID), entityType, entityID, limit)
	if err != nil {
		return nil, fmt.Errorf("by related entity: %w", err)
	}
	defer rows.Close()

	var out []*Reflection
	for rows.Next() {
		r, err := scanReflection(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("by related entity scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("by related entity rows.Err: %w", err)
	}
	return out, nil
}

// RecentWithPatterns returns reflections that have a non-NULL patterns_detected
// column and were created on or after since, ordered by created_at DESC.
func (s *Store) RecentWithPatterns(ctx context.Context, workspaceID *uuid.UUID, since time.Time, limit int) ([]*Reflection, error) {
	if limit <= 0 {
		limit = 50
	}

	const q = `SELECT ` + selectCols + ` FROM reflections
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		  AND patterns_detected IS NOT NULL
		  AND created_at >= $2
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, toUUID(workspaceID), since, limit)
	if err != nil {
		return nil, fmt.Errorf("recent with patterns: %w", err)
	}
	defer rows.Close()

	var out []*Reflection
	for rows.Next() {
		r, err := scanReflection(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("recent with patterns scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent with patterns rows.Err: %w", err)
	}
	return out, nil
}
