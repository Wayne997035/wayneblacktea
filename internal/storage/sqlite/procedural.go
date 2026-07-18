package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/likeescape"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/google/uuid"
)

// ProceduralStore is the SQLite-backed implementation of procedural.StoreIface.
type ProceduralStore struct {
	db *DB
}

// NewProceduralStore wraps an open DB into a ProceduralStore.
func NewProceduralStore(d *DB) *ProceduralStore {
	return &ProceduralStore{db: d}
}

// Compile-time guarantee against drift from procedural.StoreIface.
var _ procedural.StoreIface = (*ProceduralStore)(nil)

const proceduralSelectCols = `id, workspace_id, repo_name, project_id, title, when_to_use,
	approach_md, tools_used, files_touched, success_count, last_used_at, created_at`

// proceduralRawRow holds raw scan variables for a procedural_memories row.
type proceduralRawRow struct {
	idStr         string
	wsIDNS        sql.NullString
	repoName      string
	projectIDNS   sql.NullString
	title         string
	whenToUse     string
	approachMD    string
	toolsUsedStr  string
	filesTouchStr string
	successCount  int
	lastUsedNS    sql.NullString
	createdNS     sql.NullString
}

func scanProceduralRow(scan func(...any) error) (procedural.ProceduralMemory, error) {
	var r proceduralRawRow
	err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.repoName,
		&r.projectIDNS,
		&r.title,
		&r.whenToUse,
		&r.approachMD,
		&r.toolsUsedStr,
		&r.filesTouchStr,
		&r.successCount,
		&r.lastUsedNS,
		&r.createdNS,
	)
	if err != nil {
		return procedural.ProceduralMemory{}, err
	}
	return parseProceduralRow(r), nil
}

func parseProceduralRow(r proceduralRawRow) procedural.ProceduralMemory {
	mem := procedural.ProceduralMemory{
		RepoName:     r.repoName,
		Title:        r.title,
		WhenToUse:    r.whenToUse,
		ApproachMD:   r.approachMD,
		SuccessCount: r.successCount,
	}
	if id, err := uuid.Parse(r.idStr); err == nil {
		mem.ID = id
	}
	if r.wsIDNS.Valid {
		if id, err := uuid.Parse(r.wsIDNS.String); err == nil {
			mem.WorkspaceID = &id
		}
	}
	if r.projectIDNS.Valid {
		if id, err := uuid.Parse(r.projectIDNS.String); err == nil {
			mem.ProjectID = &id
		}
	}
	mem.LastUsedAt = parseTimestamptzToPtr(r.lastUsedNS)
	if t := parseTimestamptz(r.createdNS); t.Valid {
		mem.CreatedAt = t.Time
	}
	mem.ToolsUsed = parseStringSliceSQLite(r.toolsUsedStr)
	mem.FilesTouched = parseStringSliceSQLite(r.filesTouchStr)
	return mem
}

func parseStringSliceSQLite(s string) []string {
	if s == "" || s == "[]" || s == jsonNullLiteral {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func encodeStrings(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ss)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// Add inserts a new procedural memory and returns the persisted record.
func (s *ProceduralStore) Add(ctx context.Context, p procedural.AddParams) (*procedural.ProceduralMemory, error) {
	id := uuid.New()
	tools := encodeStrings(p.ToolsUsed)
	files := encodeStrings(p.FilesTouched)
	now := nowRFC3339()

	const q = `INSERT INTO procedural_memories
		(id, workspace_id, repo_name, project_id, title, when_to_use,
		 approach_md, tools_used, files_touched, created_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10)`
	_, err := s.db.conn.ExecContext(
		ctx, q,
		id.String(),
		s.db.workspaceArg(),
		p.RepoName,
		nullStringFromUUID(p.ProjectID),
		p.Title,
		p.WhenToUse,
		p.ApproachMD,
		tools,
		files,
		now,
	)
	if err != nil {
		return nil, errWrap("ProceduralStore.Add", err)
	}
	return s.getByID(ctx, id)
}

// Query returns procedural memories matching the filter.
func (s *ProceduralStore) Query(ctx context.Context, f procedural.QueryFilter) ([]procedural.ProceduralMemory, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 10
	}

	whereParts := []string{"(?1 IS NULL OR workspace_id = ?1)"}
	args := []any{s.db.workspaceArg()}

	if f.RepoName != "" {
		args = append(args, f.RepoName)
		whereParts = append(whereParts, fmt.Sprintf("repo_name = ?%d", len(args)))
	}

	if f.Keywords != "" {
		kw := "%" + likeescape.Escape(f.Keywords) + "%"
		args = append(args, kw)
		pos := len(args)
		whereParts = append(whereParts,
			fmt.Sprintf(`(title LIKE ?%d ESCAPE '\' OR when_to_use LIKE ?%d ESCAPE '\' OR approach_md LIKE ?%d ESCAPE '\')`,
				pos, pos, pos))
	}

	args = append(args, limit)
	where := "WHERE " + strings.Join(whereParts, " AND ")
	//nolint:gosec // G202: only column names and ?N placeholders; no user input interpolated
	q := `SELECT ` + proceduralSelectCols + ` FROM procedural_memories ` + where +
		fmt.Sprintf(` ORDER BY success_count DESC, created_at DESC LIMIT ?%d`, len(args))

	rows, err := s.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap("ProceduralStore.Query", err)
	}
	defer func() { _ = rows.Close() }()

	var out []procedural.ProceduralMemory
	for rows.Next() {
		mem, err := scanProceduralRow(rows.Scan)
		if err != nil {
			return nil, errWrap("ProceduralStore.Query scan", err)
		}
		out = append(out, mem)
	}
	return out, errWrap("ProceduralStore.Query iter", rows.Err())
}

// MarkUsed increments success_count and sets last_used_at = now.
func (s *ProceduralStore) MarkUsed(ctx context.Context, id uuid.UUID) (*procedural.ProceduralMemory, error) {
	const q = `UPDATE procedural_memories
		SET success_count = success_count + 1,
		    last_used_at  = ?3
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`
	res, err := s.db.conn.ExecContext(
		ctx, q,
		id.String(),
		s.db.workspaceArg(),
		nowRFC3339(),
	)
	if err != nil {
		return nil, errWrap("ProceduralStore.MarkUsed", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, procedural.ErrNotFound
	}
	return s.getByID(ctx, id)
}

func (s *ProceduralStore) getByID(ctx context.Context, id uuid.UUID) (*procedural.ProceduralMemory, error) {
	const q = `SELECT ` + proceduralSelectCols + ` FROM procedural_memories
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg())
	mem, err := scanProceduralRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, procedural.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("ProceduralStore.getByID", err)
	}
	return &mem, nil
}
