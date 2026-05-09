package procedural

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// New returns a Store backed by the given pool, scoped to the optional workspaceID.
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

func toText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

func jsonArray(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

const selectCols = `id, workspace_id, repo_name, project_id, title, when_to_use,
	approach_md, tools_used, files_touched, success_count, last_used_at, created_at`

// scanRow reads a full procedural_memories row into a ProceduralMemory.
func scanRow(rows pgx.Rows) (*ProceduralMemory, error) {
	var (
		mem        ProceduralMemory
		wsID       pgtype.UUID
		projectID  pgtype.UUID
		repoName   pgtype.Text
		toolsRaw   []byte
		filesRaw   []byte
		lastUsedAt pgtype.Timestamptz
		createdAt  pgtype.Timestamptz
	)
	err := rows.Scan(
		&mem.ID,
		&wsID,
		&repoName,
		&projectID,
		&mem.Title,
		&mem.WhenToUse,
		&mem.ApproachMD,
		&toolsRaw,
		&filesRaw,
		&mem.SuccessCount,
		&lastUsedAt,
		&createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning procedural memory: %w", err)
	}

	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes)
		mem.WorkspaceID = &id
	}
	if projectID.Valid {
		id := uuid.UUID(projectID.Bytes)
		mem.ProjectID = &id
	}
	mem.RepoName = repoName.String
	if lastUsedAt.Valid {
		t := lastUsedAt.Time
		mem.LastUsedAt = &t
	}
	if createdAt.Valid {
		mem.CreatedAt = createdAt.Time
	}

	mem.ToolsUsed = parseStringSlice(toolsRaw)
	mem.FilesTouched = parseStringSlice(filesRaw)

	return &mem, nil
}

func parseStringSlice(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

// Add inserts a new procedural memory and returns the persisted record.
func (s *Store) Add(ctx context.Context, p AddParams) (*ProceduralMemory, error) {
	id := uuid.New()
	tools := jsonArray(p.ToolsUsed)
	files := jsonArray(p.FilesTouched)

	const q = `
		INSERT INTO procedural_memories
			(id, workspace_id, repo_name, project_id, title, when_to_use,
			 approach_md, tools_used, files_touched)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(ctx, q,
		id,
		s.workspaceID,
		toText(p.RepoName),
		toUUID(p.ProjectID),
		p.Title,
		p.WhenToUse,
		p.ApproachMD,
		tools,
		files,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting procedural memory: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("inserting procedural memory: no row returned")
	}
	mem, err := scanRow(rows)
	if err != nil {
		return nil, fmt.Errorf("inserting procedural memory after scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inserting procedural memory rows.Err: %w", err)
	}
	return mem, nil
}

// Query returns procedural memories matching the filter.
func (s *Store) Query(ctx context.Context, f QueryFilter) ([]ProceduralMemory, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 10
	}

	args := []any{s.workspaceID}
	whereParts := []string{"($1::uuid IS NULL OR workspace_id = $1)"}

	if f.RepoName != "" {
		args = append(args, f.RepoName)
		whereParts = append(whereParts, fmt.Sprintf("repo_name = $%d", len(args)))
	}

	if f.Keywords != "" {
		kw := "%" + f.Keywords + "%"
		args = append(args, kw)
		pos := len(args)
		whereParts = append(whereParts,
			fmt.Sprintf("(title ILIKE $%d OR when_to_use ILIKE $%d OR approach_md ILIKE $%d)",
				pos, pos, pos))
	}

	args = append(args, limit)
	where := "WHERE " + strings.Join(whereParts, " AND ")
	q := `SELECT ` + selectCols + ` FROM procedural_memories ` + where +
		fmt.Sprintf(` ORDER BY success_count DESC, created_at DESC LIMIT $%d`, len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("querying procedural memories: %w", err)
	}
	defer rows.Close()

	var out []ProceduralMemory
	for rows.Next() {
		mem, err := scanRow(rows)
		if err != nil {
			return nil, fmt.Errorf("querying procedural memories: %w", err)
		}
		out = append(out, *mem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("querying procedural memories rows.Err: %w", err)
	}
	return out, nil
}

// MarkUsed increments success_count and sets last_used_at = now.
func (s *Store) MarkUsed(ctx context.Context, id uuid.UUID) (*ProceduralMemory, error) {
	const q = `UPDATE procedural_memories
		SET success_count = success_count + 1,
		    last_used_at  = NOW()
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		RETURNING ` + selectCols

	rows, err := s.pool.Query(ctx, q, id, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("marking procedural memory used %s: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, ErrNotFound
	}
	mem, err := scanRow(rows)
	if err != nil {
		return nil, fmt.Errorf("marking procedural memory used %s scan: %w", id, err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marking procedural memory used %s rows.Err: %w", id, err)
	}
	return mem, nil
}
