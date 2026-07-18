package vision

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/pgconv"
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

// NewStore returns a Store backed by the given pool, scoped to the optional workspaceID.
// nil workspaceID = legacy unscoped mode.
func NewStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{pool: pool, workspaceID: pgconv.ToUUID(workspaceID)}
}

func dependsOnJSON(deps []string) string {
	if len(deps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(deps)
	return string(b)
}

// scanItem reads a full vision_items row into a VisionItem.
func scanItem(rows pgx.Rows) (*VisionItem, error) {
	var (
		item             VisionItem
		wsID             pgtype.UUID
		projectID        pgtype.UUID
		promotedTaskID   pgtype.UUID
		repoName         pgtype.Text
		parentInitiative pgtype.Text
		contextMD        pgtype.Text
		lastDiscussedAt  pgtype.Timestamptz
		dependsOnRaw     []byte
		statusStr        string
		createdAt        pgtype.Timestamptz
	)
	err := rows.Scan(
		&item.ID,
		&wsID,
		&repoName,
		&projectID,
		&item.Title,
		&item.WhyBlocked,
		&dependsOnRaw,
		&parentInitiative,
		&statusStr,
		&contextMD,
		&promotedTaskID,
		&lastDiscussedAt,
		&createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning vision item: %w", err)
	}

	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes)
		item.WorkspaceID = &id
	}
	if projectID.Valid {
		id := uuid.UUID(projectID.Bytes)
		item.ProjectID = &id
	}
	if promotedTaskID.Valid {
		id := uuid.UUID(promotedTaskID.Bytes)
		item.PromotedTaskID = &id
	}
	item.RepoName = repoName.String
	item.ParentInitiative = parentInitiative.String
	item.ContextMD = contextMD.String
	item.Status = VisionStatus(statusStr)
	if lastDiscussedAt.Valid {
		item.LastDiscussedAt = &lastDiscussedAt.Time
	}
	if createdAt.Valid {
		item.CreatedAt = createdAt.Time
	}

	if len(dependsOnRaw) > 0 {
		if err := json.Unmarshal(dependsOnRaw, &item.DependsOn); err != nil {
			item.DependsOn = []string{}
		}
	}
	if item.DependsOn == nil {
		item.DependsOn = []string{}
	}

	return &item, nil
}

// toSummary converts a VisionItem to VisionItemSummary (omitting context_md).
func toSummary(item VisionItem) VisionItemSummary {
	return VisionItemSummary{
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

const selectCols = `id, workspace_id, repo_name, project_id, title, why_blocked,
	depends_on, parent_initiative, status, context_md,
	promoted_task_id, last_discussed_at, created_at`

// Add inserts a new vision item and returns the persisted record.
func (s *Store) Add(ctx context.Context, p AddVisionParams) (*VisionItem, error) {
	id := uuid.New()
	deps := dependsOnJSON(p.DependsOn)
	const q = `
		INSERT INTO vision_items
			(id, workspace_id, repo_name, project_id, title, why_blocked,
			 depends_on, parent_initiative, context_md)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(
		ctx, q,
		id,
		s.workspaceID,
		pgconv.ToText(p.RepoName),
		pgconv.ToUUID(p.ProjectID),
		p.Title,
		p.WhyBlocked,
		deps,
		pgconv.ToText(p.ParentInitiative),
		pgconv.ToText(p.ContextMD),
	)
	if err != nil {
		return nil, fmt.Errorf("inserting vision item: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("inserting vision item: no row returned")
	}
	item, err := scanItem(rows)
	if err != nil {
		return nil, fmt.Errorf("inserting vision item after scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inserting vision item rows.Err: %w", err)
	}
	return item, nil
}

// List returns vision items, optionally filtered by status and/or initiative.
func (s *Store) List(ctx context.Context, filter ListVisionFilter) ([]VisionItemSummary, error) {
	// Build dynamic query. Using $N parameter positions rather than string
	// interpolation to keep the query safe from injection. The only
	// string-concatenated parts are column names and literal status values
	// (both controlled by the application, never user input).
	args := []any{s.workspaceID}
	whereParts := []string{"($1::uuid IS NULL OR workspace_id = $1)"}

	if filter.Status != "" {
		args = append(args, string(filter.Status))
		whereParts = append(whereParts, fmt.Sprintf("status = $%d", len(args)))
	} else {
		whereParts = append(whereParts, "status != 'dismissed'")
	}

	if filter.ParentInitiative != "" {
		args = append(args, filter.ParentInitiative)
		whereParts = append(whereParts, fmt.Sprintf("parent_initiative = $%d", len(args)))
	}

	where := "WHERE " + strings.Join(whereParts, " AND ")
	q := `SELECT ` + selectCols + ` FROM vision_items ` + where + ` ORDER BY created_at DESC`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("listing vision items: %w", err)
	}
	defer rows.Close()

	var out []VisionItemSummary
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("listing vision items: %w", err)
		}
		out = append(out, toSummary(*item))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing vision items rows.Err: %w", err)
	}
	return out, nil
}

// Update applies non-nil fields in p to the vision item with the given id.
func (s *Store) Update(ctx context.Context, id uuid.UUID, p UpdateVisionParams) (*VisionItem, error) {
	// Auto-set last_discussed_at to NOW() when not explicitly provided.
	if p.LastDiscussedAt == nil {
		now := time.Now().UTC()
		p.LastDiscussedAt = &now
	}

	args := []any{id, s.workspaceID, pgconv.ToTimestamptz(p.LastDiscussedAt)}
	setClauses := []string{"last_discussed_at = $3"}

	if p.Status != nil {
		args = append(args, string(*p.Status))
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", len(args)))
	}
	if p.ContextMD != nil {
		args = append(args, pgconv.ToText(*p.ContextMD))
		setClauses = append(setClauses, fmt.Sprintf("context_md = $%d", len(args)))
	}

	setSQL := strings.Join(setClauses, ", ")
	//nolint:unqueryvet // setSQL contains only parameterised $N placeholders and column names; no user input is interpolated
	q := `UPDATE vision_items SET ` + setSQL + `
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("updating vision item %s: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, ErrNotFound
	}
	item, err := scanItem(rows)
	if err != nil {
		return nil, fmt.Errorf("updating vision item %s scan: %w", id, err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("updating vision item %s rows.Err: %w", id, err)
	}
	return item, nil
}

// GetByID returns the full vision item by id.
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*VisionItem, error) {
	const q = `SELECT ` + selectCols + ` FROM vision_items
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		LIMIT 1`

	rows, err := s.pool.Query(ctx, q, id, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("querying vision item %s: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("querying vision item %s rows.Err: %w", id, err)
		}
		return nil, ErrNotFound
	}
	item, err := scanItem(rows)
	if err != nil {
		return nil, fmt.Errorf("scanning vision item %s: %w", id, err)
	}
	return item, nil
}

// Promote marks a vision item as promoted and records the resulting task id.
func (s *Store) Promote(ctx context.Context, id uuid.UUID, p PromoteParams) (*VisionItem, error) {
	const q = `UPDATE vision_items
		SET status = 'promoted',
		    promoted_task_id = $3,
		    last_discussed_at = NOW()
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(ctx, q, id, s.workspaceID, p.PromotedTaskID)
	if err != nil {
		return nil, fmt.Errorf("promoting vision item %s: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, ErrNotFound
	}
	item, err := scanItem(rows)
	if err != nil {
		return nil, fmt.Errorf("scanning vision item after promote: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("promoting vision item %s rows.Err: %w", id, err)
	}
	return item, nil
}
