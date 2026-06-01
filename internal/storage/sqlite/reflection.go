package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/google/uuid"
)

// ReflectionStore is the SQLite-backed implementation of reflection.StoreIface.
type ReflectionStore struct {
	db *DB
}

// NewReflectionStore wraps an open DB into a ReflectionStore.
func NewReflectionStore(d *DB) *ReflectionStore {
	return &ReflectionStore{db: d}
}

// Compile-time guarantee against drift from reflection.StoreIface.
var _ reflection.StoreIface = (*ReflectionStore)(nil)

const reflectionSelectCols = `id, workspace_id, type, related_entity_type, related_entity_id,
	summary, insights, patterns_detected, suggested_actions, confidence, created_at`

type reflectionRawRow struct {
	idStr           string
	wsIDNS          sql.NullString
	reflType        string
	relEntityTypeNS sql.NullString
	relEntityIDNS   sql.NullString
	summary         string
	insightsStr     string
	patternsStr     string
	actionsStr      string
	confidence      float64
	createdNS       sql.NullString
}

func scanReflectionRow(scan func(...any) error) (*reflection.Reflection, error) {
	var r reflectionRawRow
	if err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.reflType,
		&r.relEntityTypeNS,
		&r.relEntityIDNS,
		&r.summary,
		&r.insightsStr,
		&r.patternsStr,
		&r.actionsStr,
		&r.confidence,
		&r.createdNS,
	); err != nil {
		return nil, err
	}
	return parseReflectionRow(r), nil
}

func parseReflectionRow(r reflectionRawRow) *reflection.Reflection {
	refl := &reflection.Reflection{
		Type:       r.reflType,
		Summary:    r.summary,
		Confidence: r.confidence,
	}
	if id, err := uuid.Parse(r.idStr); err == nil {
		refl.ID = id
	}
	if r.wsIDNS.Valid {
		if id, err := uuid.Parse(r.wsIDNS.String); err == nil {
			refl.WorkspaceID = &id
		}
	}
	if r.relEntityTypeNS.Valid && r.relEntityTypeNS.String != "" {
		s := r.relEntityTypeNS.String
		refl.RelatedEntityType = &s
	}
	if r.relEntityIDNS.Valid && r.relEntityIDNS.String != "" {
		if id, err := uuid.Parse(r.relEntityIDNS.String); err == nil {
			refl.RelatedEntityID = &id
		}
	}
	refl.Insights = parseJSONText(r.insightsStr)
	refl.PatternsDetected = parseJSONText(r.patternsStr)
	refl.SuggestedActions = parseJSONText(r.actionsStr)
	if t := parseTimestamptz(r.createdNS); t.Valid {
		refl.CreatedAt = t.Time
	}
	return refl
}

// parseJSONText converts a stored TEXT JSON value. Returns nil when the stored
// value is the JSON null literal or empty (indicating a SQL NULL column default).
func parseJSONText(s string) json.RawMessage {
	if s == "" || s == jsonNullText {
		return nil
	}
	return json.RawMessage(s)
}

// encodeJSON serialises a json.RawMessage for storage as TEXT.
// nil input returns the JSON null literal so the CHECK constraint on the column
// is satisfied (columns default to 'null', never empty string).
func encodeJSON(v json.RawMessage) string {
	if len(v) == 0 {
		return jsonNullText
	}
	return string(v)
}

// Create inserts a new reflection and returns the persisted record.
func (s *ReflectionStore) Create(ctx context.Context, p reflection.CreateParams) (*reflection.Reflection, error) {
	id := uuid.New()
	now := nowRFC3339()

	const q = `INSERT INTO reflections
		(id, workspace_id, type, related_entity_type, related_entity_id,
		 summary, insights, patterns_detected, suggested_actions, confidence, created_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11)`

	var relEntityType any
	if p.RelatedEntityType != nil {
		relEntityType = *p.RelatedEntityType
	}
	var relEntityID any
	if p.RelatedEntityID != nil {
		relEntityID = p.RelatedEntityID.String()
	}

	// Use the param WorkspaceID when set; fall back to the DB-level workspace.
	wsArg := s.db.workspaceArg()
	if p.WorkspaceID != nil {
		wsArg = p.WorkspaceID.String()
	}

	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		wsArg,
		p.Type,
		relEntityType,
		relEntityID,
		p.Summary,
		encodeJSON(p.Insights),
		encodeJSON(p.PatternsDetected),
		encodeJSON(p.SuggestedActions),
		p.Confidence,
		now,
	)
	if err != nil {
		return nil, errWrap("ReflectionStore.Create", err)
	}
	return s.getByID(ctx, id)
}

// List returns reflections matching the filter, ordered by created_at DESC.
func (s *ReflectionStore) List(ctx context.Context, p reflection.ListParams) ([]*reflection.Reflection, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}

	wsArg := s.db.workspaceArg()
	if p.WorkspaceID != nil {
		wsArg = p.WorkspaceID.String()
	}
	args := []any{wsArg}
	where := `(?1 IS NULL OR workspace_id = ?1)`

	if p.Type != nil {
		args = append(args, *p.Type)
		where += fmt.Sprintf(` AND type = ?%d`, len(args))
	}

	args = append(args, limit)
	//nolint:gosec // G202: only column names and ?N placeholders; no user input interpolated
	q := `SELECT ` + reflectionSelectCols + ` FROM reflections WHERE ` + where +
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT ?%d`, len(args))

	rows, err := s.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap("ReflectionStore.List", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*reflection.Reflection
	for rows.Next() {
		r, err := scanReflectionRow(rows.Scan)
		if err != nil {
			return nil, errWrap("ReflectionStore.List scan", err)
		}
		out = append(out, r)
	}
	return out, errWrap("ReflectionStore.List iter", rows.Err())
}

// GetLatest returns the most recent reflection of a given type in the workspace.
func (s *ReflectionStore) GetLatest(ctx context.Context, workspaceID *uuid.UUID, reflType string) (*reflection.Reflection, error) {
	wsArg := s.db.workspaceArg()
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}

	const q = `SELECT ` + reflectionSelectCols + ` FROM reflections
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND type = ?2
		ORDER BY created_at DESC
		LIMIT 1`

	row := s.db.conn.QueryRowContext(ctx, q, wsArg, reflType)
	r, err := scanReflectionRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, reflection.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("ReflectionStore.GetLatest", err)
	}
	return r, nil
}

// ByRelatedEntity returns reflections scoped to a specific related entity.
func (s *ReflectionStore) ByRelatedEntity(
	ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID, limit int,
) ([]*reflection.Reflection, error) {
	if limit <= 0 {
		limit = 20
	}
	wsArg := s.db.workspaceArg()
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}

	const q = `SELECT ` + reflectionSelectCols + ` FROM reflections
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND related_entity_type = ?2
		  AND related_entity_id = ?3
		ORDER BY created_at DESC
		LIMIT ?4`

	rows, err := s.db.conn.QueryContext(ctx, q, wsArg, entityType, entityID.String(), limit)
	if err != nil {
		return nil, errWrap("ReflectionStore.ByRelatedEntity", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*reflection.Reflection
	for rows.Next() {
		r, err := scanReflectionRow(rows.Scan)
		if err != nil {
			return nil, errWrap("ReflectionStore.ByRelatedEntity scan", err)
		}
		out = append(out, r)
	}
	return out, errWrap("ReflectionStore.ByRelatedEntity iter", rows.Err())
}

// RecentWithPatterns returns reflections that have a non-null patterns_detected
// value and were created on or after since, ordered by created_at DESC.
func (s *ReflectionStore) RecentWithPatterns(
	ctx context.Context, workspaceID *uuid.UUID, since time.Time, limit int,
) ([]*reflection.Reflection, error) {
	if limit <= 0 {
		limit = 50
	}
	wsArg := s.db.workspaceArg()
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}
	sinceStr := since.UTC().Format("2006-01-02T15:04:05.000Z07:00")

	// patterns_detected != ?4 filters out rows where the column holds the JSON
	// null sentinel ('null'). Parameterising it avoids any string interpolation.
	const q = `SELECT ` + reflectionSelectCols + ` FROM reflections
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND patterns_detected != ?4
		  AND created_at >= ?2
		ORDER BY created_at DESC
		LIMIT ?3`

	rows, err := s.db.conn.QueryContext(ctx, q, wsArg, sinceStr, limit, jsonNullText)
	if err != nil {
		return nil, errWrap("ReflectionStore.RecentWithPatterns", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*reflection.Reflection
	for rows.Next() {
		r, err := scanReflectionRow(rows.Scan)
		if err != nil {
			return nil, errWrap("ReflectionStore.RecentWithPatterns scan", err)
		}
		out = append(out, r)
	}
	return out, errWrap("ReflectionStore.RecentWithPatterns iter", rows.Err())
}

// PruneOlderThan hard-deletes reflection rows with created_at < cutoff.
// Called daily by the scheduler to enforce the 180-day TTL per
// backend-security-design.md §1.3.
func (s *ReflectionStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffStr := cutoff.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	const q = `DELETE FROM reflections WHERE created_at < ?`
	result, err := s.db.conn.ExecContext(ctx, q, cutoffStr)
	if err != nil {
		return 0, errWrap("ReflectionStore.PruneOlderThan", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, errWrap("ReflectionStore.PruneOlderThan rows affected", err)
	}
	return n, nil
}

func (s *ReflectionStore) getByID(ctx context.Context, id uuid.UUID) (*reflection.Reflection, error) {
	const q = `SELECT ` + reflectionSelectCols + ` FROM reflections
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg())
	r, err := scanReflectionRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, reflection.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("ReflectionStore.getByID", err)
	}
	return r, nil
}
