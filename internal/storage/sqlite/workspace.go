package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

// WorkspaceStore is the SQLite-backed implementation of workspace.StoreIface.
type WorkspaceStore struct {
	db *DB
}

// NewWorkspaceStore wraps an open DB into a WorkspaceStore.
func NewWorkspaceStore(d *DB) *WorkspaceStore {
	return &WorkspaceStore{db: d}
}

var _ workspace.StoreIface = (*WorkspaceStore)(nil)

const reposSelectCols = `id, name, path, description, language, status,
	current_branch, known_issues, next_planned_step, last_activity, created_at,
	updated_at, workspace_id`

func encodeStringSlice(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	b, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal string slice: %w", err)
	}
	return string(b), nil
}

func decodeStringSlice(raw sql.NullString) ([]string, error) {
	if !raw.Valid || raw.String == "" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw.String), &values); err != nil {
		return nil, fmt.Errorf("unmarshal string slice: %w", err)
	}
	if values == nil {
		return []string{}, nil
	}
	return values, nil
}

func scanRepo(scan func(...any) error) (db.Repo, error) {
	var (
		r                          db.Repo
		idStr                      string
		pathNS, descNS, langNS     sql.NullString
		branchNS, issuesNS, stepNS sql.NullString
		lastNS, createdNS, updNS   sql.NullString
		wsNS                       sql.NullString
	)
	err := scan(&idStr, &r.Name, &pathNS, &descNS, &langNS, &r.Status,
		&branchNS, &issuesNS, &stepNS, &lastNS, &createdNS, &updNS, &wsNS)
	if err != nil {
		return db.Repo{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		r.ID = id
	}
	issues, err := decodeStringSlice(issuesNS)
	if err != nil {
		return db.Repo{}, err
	}
	r.Path = pgtypeText(pathNS.String, pathNS.Valid)
	r.Description = pgtypeText(descNS.String, descNS.Valid)
	r.Language = pgtypeText(langNS.String, langNS.Valid)
	r.CurrentBranch = pgtypeText(branchNS.String, branchNS.Valid)
	r.KnownIssues = issues
	r.NextPlannedStep = pgtypeText(stepNS.String, stepNS.Valid)
	r.LastActivity = parseTimestamptz(lastNS)
	r.CreatedAt = parseTimestamptz(createdNS)
	r.UpdatedAt = parseTimestamptz(updNS)
	r.WorkspaceID = pgtypeUUID(nsString(wsNS))
	return r, nil
}

// ActiveRepos returns all active repos, ordered by recent activity.
func (s *WorkspaceStore) ActiveRepos(ctx context.Context) ([]db.Repo, error) {
	const q = `SELECT ` + reposSelectCols + ` FROM repos
		WHERE status = 'active'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY last_activity DESC NULLS LAST, name ASC`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("ActiveRepos", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Repo
	for rows.Next() {
		r, err := scanRepo(rows.Scan)
		if err != nil {
			return nil, errWrap("ActiveRepos scan", err)
		}
		out = append(out, r)
	}
	return out, errWrap("ActiveRepos iter", rows.Err())
}

// RepoByName returns a single repo by unique name, or workspace.ErrNotFound.
func (s *WorkspaceStore) RepoByName(ctx context.Context, name string) (*db.Repo, error) {
	const q = `SELECT ` + reposSelectCols + ` FROM repos
		WHERE name = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	r, err := scanRepo(s.db.conn.QueryRowContext(ctx, q, name, s.db.workspaceArg()).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("RepoByName", err)
	}
	return &r, nil
}

// UpsertRepo creates or updates a repo entry.
func (s *WorkspaceStore) UpsertRepo(ctx context.Context, p workspace.UpsertRepoParams) (*db.Repo, error) {
	id := uuid.New()
	issuesJSON, err := encodeStringSlice(p.KnownIssues)
	if err != nil {
		return nil, err
	}
	now := sqliteNowMillis()
	const q = `INSERT INTO repos
		(id, workspace_id, name, path, description, language, current_branch,
		 known_issues, next_planned_step, last_activity, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?10, ?10)
		ON CONFLICT(name) DO UPDATE SET
			path = excluded.path,
			description = excluded.description,
			language = excluded.language,
			current_branch = excluded.current_branch,
			known_issues = excluded.known_issues,
			next_planned_step = excluded.next_planned_step,
			last_activity = excluded.last_activity,
			updated_at = excluded.updated_at`
	_, err = s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), p.Name, nullStringIfEmpty(p.Path),
		nullStringIfEmpty(p.Description), nullStringIfEmpty(p.Language),
		nullStringIfEmpty(p.CurrentBranch), issuesJSON,
		nullStringIfEmpty(p.NextPlannedStep), now)
	if err != nil {
		return nil, errWrap("UpsertRepo", err)
	}
	return s.RepoByName(ctx, p.Name)
}
