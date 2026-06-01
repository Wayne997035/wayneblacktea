package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/google/uuid"
)

// PlaybookStore is the SQLite-backed implementation of playbook.StoreIface.
type PlaybookStore struct {
	db *DB
}

// NewPlaybookStore wraps an open DB into a PlaybookStore.
func NewPlaybookStore(d *DB) *PlaybookStore {
	return &PlaybookStore{db: d}
}

// Compile-time guarantee against drift from playbook.StoreIface.
var _ playbook.StoreIface = (*PlaybookStore)(nil)

const playbookSelectCols = `id, workspace_id, trigger_pattern, action_template,
	source_decision_ids, confidence, hits, last_used_at, created_at, updated_at`

// playbookRawRow holds the raw scan variables for a playbooks row.
type playbookRawRow struct {
	idStr          string
	wsIDNS         sql.NullString
	triggerPattern string
	actionTemplate string
	srcIDsStr      string
	confidence     float64
	hits           int
	lastUsedNS     sql.NullString
	createdNS      sql.NullString
	updatedNS      sql.NullString
}

func scanPlaybookRow(scan func(...any) error) (*playbook.Playbook, error) {
	var r playbookRawRow
	if err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.triggerPattern,
		&r.actionTemplate,
		&r.srcIDsStr,
		&r.confidence,
		&r.hits,
		&r.lastUsedNS,
		&r.createdNS,
		&r.updatedNS,
	); err != nil {
		return nil, err
	}
	return parsePlaybookRow(r), nil
}

func parsePlaybookRow(r playbookRawRow) *playbook.Playbook {
	pb := &playbook.Playbook{
		TriggerPattern: r.triggerPattern,
		ActionTemplate: r.actionTemplate,
		Confidence:     r.confidence,
		Hits:           r.hits,
	}
	if id, err := uuid.Parse(r.idStr); err == nil {
		pb.ID = id
	}
	if r.wsIDNS.Valid {
		if id, err := uuid.Parse(r.wsIDNS.String); err == nil {
			pb.WorkspaceID = &id
		}
	}
	pb.SourceDecisionIDs = parseUUIDSlice(r.srcIDsStr)
	pb.LastUsedAt = parseTimestamptzToPtr(r.lastUsedNS)
	if t := parseTimestamptz(r.createdNS); t.Valid {
		pb.CreatedAt = t.Time
	}
	if t := parseTimestamptz(r.updatedNS); t.Valid {
		pb.UpdatedAt = t.Time
	}
	return pb
}

// parseUUIDSlice decodes a JSON array of UUID strings stored in a TEXT column.
func parseUUIDSlice(s string) []uuid.UUID {
	if s == "" || s == "[]" || s == jsonNullLiteral {
		return []uuid.UUID{}
	}
	var raw []string
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return []uuid.UUID{}
	}
	out := make([]uuid.UUID, 0, len(raw))
	for _, v := range raw {
		if id, err := uuid.Parse(v); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// encodeUUIDSlice serialises a []uuid.UUID to a JSON array string.
func encodeUUIDSlice(ids []uuid.UUID) string {
	if len(ids) == 0 {
		return "[]"
	}
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	b, err := json.Marshal(strs)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// Create inserts a new playbook and returns the persisted record.
func (s *PlaybookStore) Create(ctx context.Context, p playbook.CreateParams) (*playbook.Playbook, error) {
	id := uuid.New()
	confidence := p.Confidence
	if confidence == 0 {
		confidence = 0.50
	}
	srcIDs := encodeUUIDSlice(p.SourceDecisionIDs)
	now := nowRFC3339()

	const q = `INSERT INTO playbooks
		(id, workspace_id, trigger_pattern, action_template,
		 source_decision_ids, confidence, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?7)`
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		s.db.workspaceArg(),
		p.TriggerPattern,
		p.ActionTemplate,
		srcIDs,
		confidence,
		now,
	)
	if err != nil {
		return nil, errWrap("PlaybookStore.Create", err)
	}
	return s.getByID(ctx, id)
}

// List returns playbooks matching the filter, ordered by confidence DESC.
func (s *PlaybookStore) List(ctx context.Context, p playbook.ListParams) ([]*playbook.Playbook, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}

	whereParts := []string{"(?1 IS NULL OR workspace_id = ?1)"}
	args := []any{s.db.workspaceArg()}

	if len(p.ContextKeywords) > 0 {
		kwParts := make([]string, 0, len(p.ContextKeywords))
		for _, kw := range p.ContextKeywords {
			if kw == "" {
				continue
			}
			args = append(args, "%"+kw+"%")
			pos := len(args)
			kwParts = append(kwParts, fmt.Sprintf(
				"(trigger_pattern LIKE ?%d OR action_template LIKE ?%d)", pos, pos,
			))
		}
		if len(kwParts) > 0 {
			whereParts = append(whereParts, "("+strings.Join(kwParts, " OR ")+")")
		}
	}

	args = append(args, limit)
	where := "WHERE " + strings.Join(whereParts, " AND ")
	//nolint:gosec // G202: only column names and ?N placeholders; no user input interpolated
	q := `SELECT ` + playbookSelectCols + ` FROM playbooks ` + where +
		fmt.Sprintf(` ORDER BY confidence DESC LIMIT ?%d`, len(args))

	rows, err := s.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap("PlaybookStore.List", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*playbook.Playbook
	for rows.Next() {
		pb, err := scanPlaybookRow(rows.Scan)
		if err != nil {
			return nil, errWrap("PlaybookStore.List scan", err)
		}
		out = append(out, pb)
	}
	return out, errWrap("PlaybookStore.List iter", rows.Err())
}

// IncrementHits atomically increments the hits counter and sets last_used_at.
func (s *PlaybookStore) IncrementHits(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE playbooks
		SET hits = hits + 1,
		    last_used_at = ?3,
		    updated_at   = ?3
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`

	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q, id.String(), s.db.workspaceArg(), now)
	if err != nil {
		return errWrap("PlaybookStore.IncrementHits", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return playbook.ErrNotFound
	}
	return nil
}

func (s *PlaybookStore) getByID(ctx context.Context, id uuid.UUID) (*playbook.Playbook, error) {
	const q = `SELECT ` + playbookSelectCols + ` FROM playbooks
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg())
	pb, err := scanPlaybookRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, playbook.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("PlaybookStore.getByID", err)
	}
	return pb, nil
}
