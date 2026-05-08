package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/google/uuid"
)

// VisionStore is the SQLite-backed implementation of vision.StoreIface.
type VisionStore struct {
	db *DB
}

// NewVisionStore wraps an open DB into a VisionStore.
func NewVisionStore(d *DB) *VisionStore {
	return &VisionStore{db: d}
}

// Compile-time guarantee against drift from vision.StoreIface.
var _ vision.StoreIface = (*VisionStore)(nil)

// ----- column layout -----

const visionSelectCols = `id, workspace_id, repo_name, project_id, title, why_blocked,
	depends_on, parent_initiative, status, context_md,
	promoted_task_id, last_discussed_at, created_at`

// visionRawRow holds the raw scan variables for a vision_items row. Extracted
// from scanVisionItem to reduce cyclomatic complexity of the scan function.
type visionRawRow struct {
	idStr            string
	wsIDNS           sql.NullString
	repoNameNS       sql.NullString
	projectIDNS      sql.NullString
	title            string
	whyBlocked       string
	dependsOnStr     string
	parentInitNS     sql.NullString
	statusStr        string
	contextMDNS      sql.NullString
	promotedTaskIDNS sql.NullString
	lastDiscussedNS  sql.NullString
	createdNS        sql.NullString
}

// scanVisionItem reads a full vision_items row.
func scanVisionItem(scan func(...any) error) (vision.VisionItem, error) {
	var r visionRawRow
	err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.repoNameNS,
		&r.projectIDNS,
		&r.title,
		&r.whyBlocked,
		&r.dependsOnStr,
		&r.parentInitNS,
		&r.statusStr,
		&r.contextMDNS,
		&r.promotedTaskIDNS,
		&r.lastDiscussedNS,
		&r.createdNS,
	)
	if err != nil {
		return vision.VisionItem{}, err
	}
	return parseVisionRow(r), nil
}

// parseVisionRow converts raw scan values into a VisionItem. Separated from
// scanVisionItem so gocyclo counts are kept below threshold.
func parseVisionRow(r visionRawRow) vision.VisionItem {
	item := vision.VisionItem{
		Title:      r.title,
		WhyBlocked: r.whyBlocked,
		Status:     vision.VisionStatus(r.statusStr),
	}
	if id, err := uuid.Parse(r.idStr); err == nil {
		item.ID = id
	}
	if r.wsIDNS.Valid {
		if id, err := uuid.Parse(r.wsIDNS.String); err == nil {
			item.WorkspaceID = &id
		}
	}
	item.RepoName = r.repoNameNS.String
	if r.projectIDNS.Valid {
		if id, err := uuid.Parse(r.projectIDNS.String); err == nil {
			item.ProjectID = &id
		}
	}
	item.ParentInitiative = r.parentInitNS.String
	item.ContextMD = r.contextMDNS.String
	if r.promotedTaskIDNS.Valid {
		if id, err := uuid.Parse(r.promotedTaskIDNS.String); err == nil {
			item.PromotedTaskID = &id
		}
	}
	item.LastDiscussedAt = parseTimestamptzToPtr(r.lastDiscussedNS)
	if t := parseTimestamptz(r.createdNS); t.Valid {
		item.CreatedAt = t.Time
	}

	item.DependsOn = []string{}
	if r.dependsOnStr != "" && r.dependsOnStr != "[]" {
		if err := json.Unmarshal([]byte(r.dependsOnStr), &item.DependsOn); err != nil {
			item.DependsOn = []string{}
		}
	}
	return item
}

// parseTimestamptzToPtr is like parseTimestamptz but returns a *time.Time pointer.
func parseTimestamptzToPtr(ns sql.NullString) *time.Time {
	t := parseTimestamptz(ns)
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func visionToSummary(item vision.VisionItem) vision.VisionItemSummary {
	return vision.VisionItemSummary{
		ID:               item.ID,
		WorkspaceID:      item.WorkspaceID,
		RepoName:         item.RepoName,
		ProjectID:        item.ProjectID,
		Title:            item.Title,
		WhyBlocked:       item.WhyBlocked,
		DependsOn:        item.DependsOn,
		ParentInitiative: item.ParentInitiative,
		Status:           item.Status,
		PromotedTaskID:   item.PromotedTaskID,
		LastDiscussedAt:  item.LastDiscussedAt,
		CreatedAt:        item.CreatedAt,
	}
}

// ----- StoreIface methods -----

// Add inserts a new vision item.
func (s *VisionStore) Add(ctx context.Context, p vision.AddVisionParams) (*vision.VisionItem, error) {
	id := uuid.New()
	deps := "[]"
	if len(p.DependsOn) > 0 {
		b, _ := json.Marshal(p.DependsOn)
		deps = string(b)
	}

	const q = `INSERT INTO vision_items
		(id, workspace_id, repo_name, project_id, title, why_blocked,
		 depends_on, parent_initiative, context_md, created_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)`
	now := nowRFC3339()
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		s.db.workspaceArg(),
		nullStringIfEmpty(p.RepoName),
		nullStringFromUUID(p.ProjectID),
		p.Title,
		p.WhyBlocked,
		deps,
		nullStringIfEmpty(p.ParentInitiative),
		nullStringIfEmpty(p.ContextMD),
		now,
	)
	if err != nil {
		return nil, errWrap("VisionStore.Add", err)
	}
	return s.getByID(ctx, id)
}

// List returns vision items matching the filter, ordered by created_at DESC.
func (s *VisionStore) List(ctx context.Context, filter vision.ListVisionFilter) ([]vision.VisionItemSummary, error) {
	whereParts := []string{"(?1 IS NULL OR workspace_id = ?1)"}
	args := []any{s.db.workspaceArg()}

	if filter.Status != "" {
		args = append(args, string(filter.Status))
		whereParts = append(whereParts, fmt.Sprintf("status = ?%d", len(args)))
	} else {
		whereParts = append(whereParts, "status != 'dismissed'")
	}

	if filter.ParentInitiative != "" {
		args = append(args, filter.ParentInitiative)
		whereParts = append(whereParts, fmt.Sprintf("parent_initiative = ?%d", len(args)))
	}

	where := "WHERE " + strings.Join(whereParts, " AND ")
	//nolint:gosec // G202: only column names and ?N placeholders; no user input interpolated
	q := `SELECT ` + visionSelectCols + ` FROM vision_items ` + where + ` ORDER BY created_at DESC`
	rows, err := s.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap("VisionStore.List", err)
	}
	defer func() { _ = rows.Close() }()

	var out []vision.VisionItemSummary
	for rows.Next() {
		item, err := scanVisionItem(rows.Scan)
		if err != nil {
			return nil, errWrap("VisionStore.List scan", err)
		}
		out = append(out, visionToSummary(item))
	}
	return out, errWrap("VisionStore.List iter", rows.Err())
}

// GetByID returns the full vision item by id.
func (s *VisionStore) GetByID(ctx context.Context, id uuid.UUID) (*vision.VisionItem, error) {
	return s.getByID(ctx, id)
}

func (s *VisionStore) getByID(ctx context.Context, id uuid.UUID) (*vision.VisionItem, error) {
	const q = `SELECT ` + visionSelectCols + ` FROM vision_items
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg())
	item, err := scanVisionItem(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, vision.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("VisionStore.GetByID", err)
	}
	return &item, nil
}

// buildUpdateSet builds the SET clause and argument list for an UPDATE query.
// All positional placeholders use ?N notation; the first two args are always
// id (?1) and workspace_id (?2). last_discussed_at is always updated.
func (s *VisionStore) buildUpdateSet(
	id uuid.UUID, p vision.UpdateVisionParams,
) (setClauses []string, args []any) {
	now := nowRFC3339()
	lastDiscussedVal := any(now)
	if p.LastDiscussedAt != nil {
		lastDiscussedVal = p.LastDiscussedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	}

	args = []any{id.String(), s.db.workspaceArg(), lastDiscussedVal}
	setClauses = []string{"last_discussed_at = ?3"}

	if p.Status != nil {
		args = append(args, string(*p.Status))
		setClauses = append(setClauses, fmt.Sprintf("status = ?%d", len(args)))
	}
	if p.ContextMD != nil {
		args = append(args, nullStringIfEmpty(*p.ContextMD))
		setClauses = append(setClauses, fmt.Sprintf("context_md = ?%d", len(args)))
	}
	return setClauses, args
}

// Update applies non-nil fields in p to the vision item.
func (s *VisionStore) Update(ctx context.Context, id uuid.UUID, p vision.UpdateVisionParams) (*vision.VisionItem, error) {
	setClauses, args := s.buildUpdateSet(id, p)
	setSQL := strings.Join(setClauses, ", ")

	//nolint:gosec,unqueryvet // G202: only column names and ?N placeholders; no user input interpolated
	q := `UPDATE vision_items SET ` + setSQL + `
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`
	res, err := s.db.conn.ExecContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap("VisionStore.Update", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, vision.ErrNotFound
	}
	return s.getByID(ctx, id)
}

// Promote marks a vision item as promoted and records the resulting task id.
func (s *VisionStore) Promote(ctx context.Context, id uuid.UUID, p vision.PromoteParams) (*vision.VisionItem, error) {
	const q = `UPDATE vision_items
		SET status = 'promoted',
		    promoted_task_id = ?3,
		    last_discussed_at = ?4
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`
	res, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		s.db.workspaceArg(),
		p.PromotedTaskID.String(),
		nowRFC3339(),
	)
	if err != nil {
		return nil, errWrap("VisionStore.Promote", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, vision.ErrNotFound
	}
	return s.getByID(ctx, id)
}
