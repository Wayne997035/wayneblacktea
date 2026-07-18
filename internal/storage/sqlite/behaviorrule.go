package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/behaviorrule"
	"github.com/google/uuid"
)

// BehaviorRuleStore is the SQLite-backed implementation of behaviorrule.StoreIface.
type BehaviorRuleStore struct {
	db *DB
}

// NewBehaviorRuleStore wraps an open DB into a BehaviorRuleStore.
func NewBehaviorRuleStore(d *DB) *BehaviorRuleStore {
	return &BehaviorRuleStore{db: d}
}

// Compile-time guarantee against drift from behaviorrule.StoreIface.
var _ behaviorrule.StoreIface = (*BehaviorRuleStore)(nil)

const behaviorRuleSelectCols = `id, workspace_id, condition, action, source_type, source_id,
	confidence, status, created_at, updated_at`

type behaviorRuleRawRow struct {
	idStr      string
	wsIDNS     sql.NullString
	condition  string
	action     string
	sourceType string
	sourceIDNS sql.NullString
	confidence float64
	status     string
	createdNS  sql.NullString
	updatedNS  sql.NullString
}

func scanBehaviorRuleRow(scan func(...any) error) (*behaviorrule.BehaviorRule, error) {
	var r behaviorRuleRawRow
	if err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.condition,
		&r.action,
		&r.sourceType,
		&r.sourceIDNS,
		&r.confidence,
		&r.status,
		&r.createdNS,
		&r.updatedNS,
	); err != nil {
		return nil, err
	}
	return parseBehaviorRuleRow(r), nil
}

func parseBehaviorRuleRow(r behaviorRuleRawRow) *behaviorrule.BehaviorRule {
	rule := &behaviorrule.BehaviorRule{
		Condition:  r.condition,
		Action:     r.action,
		SourceType: r.sourceType,
		Confidence: r.confidence,
		Status:     r.status,
	}
	if id, err := uuid.Parse(r.idStr); err == nil {
		rule.ID = id
	}
	if r.wsIDNS.Valid {
		if id, err := uuid.Parse(r.wsIDNS.String); err == nil {
			rule.WorkspaceID = &id
		}
	}
	if r.sourceIDNS.Valid && r.sourceIDNS.String != "" {
		if id, err := uuid.Parse(r.sourceIDNS.String); err == nil {
			rule.SourceID = &id
		}
	}
	if t := parseTimestamptz(r.createdNS); t.Valid {
		rule.CreatedAt = t.Time
	}
	if t := parseTimestamptz(r.updatedNS); t.Valid {
		rule.UpdatedAt = t.Time
	}
	return rule
}

// Propose inserts a new behavior rule with status='proposed' and returns
// the persisted record.
func (s *BehaviorRuleStore) Propose(ctx context.Context, p behaviorrule.CreateParams) (*behaviorrule.BehaviorRule, error) {
	id := uuid.New()
	now := nowRFC3339()

	confidence := p.Confidence
	if confidence <= 0 || confidence > 1.0 {
		confidence = 0.50
	}

	wsArg := s.db.workspaceArg()
	if p.WorkspaceID != nil {
		wsArg = p.WorkspaceID.String()
	}

	var sourceID any
	if p.SourceID != nil {
		sourceID = p.SourceID.String()
	}

	const q = `INSERT INTO behavior_rules
		(id, workspace_id, condition, action, source_type, source_id,
		 confidence, status, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, 'proposed', ?8, ?8)`

	_, err := s.db.conn.ExecContext(
		ctx, q,
		id.String(),
		wsArg,
		p.Condition,
		p.Action,
		p.SourceType,
		sourceID,
		confidence,
		now,
	)
	if err != nil {
		return nil, errWrap("BehaviorRuleStore.Propose", err)
	}
	return s.getByID(ctx, id)
}

// List returns behavior rules matching the filter, ordered by created_at DESC.
//
//nolint:dupl // Structural pattern mirrors ReflectionStore.List; domain types differ, extraction would reduce type safety.
func (s *BehaviorRuleStore) List(ctx context.Context, p behaviorrule.ListParams) ([]*behaviorrule.BehaviorRule, error) {
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

	if p.Status != nil {
		args = append(args, *p.Status)
		where += fmt.Sprintf(` AND status = ?%d`, len(args))
	}

	args = append(args, limit)
	//nolint:gosec // G202: only column names and ?N placeholders; no user input interpolated
	q := `SELECT ` + behaviorRuleSelectCols + ` FROM behavior_rules WHERE ` + where +
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT ?%d`, len(args))

	rows, err := s.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap("BehaviorRuleStore.List", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*behaviorrule.BehaviorRule
	for rows.Next() {
		r, err := scanBehaviorRuleRow(rows.Scan)
		if err != nil {
			return nil, errWrap("BehaviorRuleStore.List scan", err)
		}
		out = append(out, r)
	}
	return out, errWrap("BehaviorRuleStore.List iter", rows.Err())
}

func (s *BehaviorRuleStore) getByID(ctx context.Context, id uuid.UUID) (*behaviorrule.BehaviorRule, error) {
	const q = `SELECT ` + behaviorRuleSelectCols + ` FROM behavior_rules
		WHERE id = ?1
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	r, err := scanBehaviorRuleRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, behaviorrule.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("BehaviorRuleStore.getByID", err)
	}
	return r, nil
}

// ApplyOutcome applies the confidence formula atomically in a single UPDATE.
// SQLite uses CASE expressions to implement the atomic update without a
// read-then-write race.
// SECURITY: workspace-scoped — matches List's scoping and the Postgres Store,
// so a store scoped to workspace A cannot apply an outcome to a rule owned by
// workspace B.
func (s *BehaviorRuleStore) ApplyOutcome(ctx context.Context, id uuid.UUID, outcome string) (*behaviorrule.BehaviorRule, error) {
	now := nowRFC3339()

	var q string
	switch outcome {
	case "success":
		// Atomic: transition proposed→active and bump confidence in one UPDATE.
		q = `UPDATE behavior_rules
			SET
				status = CASE WHEN status = 'proposed' THEN 'active' ELSE status END,
				confidence = MIN(confidence + 0.05, 1.00),
				updated_at = ?2
			WHERE id = ?1
			  AND (?3 IS NULL OR workspace_id = ?3)`
	case "failure":
		q = `UPDATE behavior_rules
			SET
				confidence = MAX(confidence - 0.10, 0.00),
				updated_at = ?2
			WHERE id = ?1
			  AND (?3 IS NULL OR workspace_id = ?3)`
	default:
		return nil, fmt.Errorf("behaviorrule: invalid outcome %q; must be 'success' or 'failure'", outcome)
	}

	result, err := s.db.conn.ExecContext(ctx, q, id.String(), now, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("BehaviorRuleStore.ApplyOutcome", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, errWrap("BehaviorRuleStore.ApplyOutcome rows affected", err)
	}
	if n == 0 {
		return nil, behaviorrule.ErrNotFound
	}
	return s.getByID(ctx, id)
}

// Deprecate sets the rule's status to 'deprecated'. Idempotent.
// SECURITY: workspace-scoped — matches List's scoping and the Postgres Store.
func (s *BehaviorRuleStore) Deprecate(ctx context.Context, id uuid.UUID) (*behaviorrule.BehaviorRule, error) {
	now := nowRFC3339()
	const q = `UPDATE behavior_rules
		SET status = 'deprecated', updated_at = ?2
		WHERE id = ?1
		  AND (?3 IS NULL OR workspace_id = ?3)`

	result, err := s.db.conn.ExecContext(ctx, q, id.String(), now, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("BehaviorRuleStore.Deprecate", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, errWrap("BehaviorRuleStore.Deprecate rows affected", err)
	}
	if n == 0 {
		return nil, behaviorrule.ErrNotFound
	}
	return s.getByID(ctx, id)
}

// PruneOlderThan hard-deletes behavior_rules rows where
// status IN ('rejected','deprecated') AND created_at < cutoff.
// Active and proposed rows are NEVER deleted regardless of age.
func (s *BehaviorRuleStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffStr := cutoff.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	const q = `DELETE FROM behavior_rules
		WHERE status IN ('rejected','deprecated') AND created_at < ?`
	result, err := s.db.conn.ExecContext(ctx, q, cutoffStr)
	if err != nil {
		return 0, errWrap("BehaviorRuleStore.PruneOlderThan", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, errWrap("BehaviorRuleStore.PruneOlderThan rows affected", err)
	}
	return n, nil
}
