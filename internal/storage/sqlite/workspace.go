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

// RepoByID returns a single repo by primary key UUID, or workspace.ErrNotFound.
// Workspace-scoped via the configured workspace_id (NULL → unscoped legacy mode).
func (s *WorkspaceStore) RepoByID(ctx context.Context, id uuid.UUID) (*db.Repo, error) {
	const q = `SELECT ` + reposSelectCols + ` FROM repos
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	r, err := scanRepo(s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg()).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, workspace.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("RepoByID", err)
	}
	return &r, nil
}

// GetModelPreference returns the stored model_preference, or
// workspace.DefaultModelPreference when no row exists.
func (s *WorkspaceStore) GetModelPreference(ctx context.Context) (string, error) {
	const q = `SELECT model_preference FROM workspace_preferences WHERE workspace_id = ?1`
	var model string
	err := s.db.conn.QueryRowContext(ctx, q, s.db.workspaceArg()).Scan(&model)
	if errors.Is(err, sql.ErrNoRows) {
		return workspace.DefaultModelPreference, nil
	}
	if err != nil {
		return "", errWrap("GetModelPreference", err)
	}
	return model, nil
}

// UpsertModelPreference stores the workspace's model_preference. Returns
// workspace.ErrInvalidModel when model is not allowed.
func (s *WorkspaceStore) UpsertModelPreference(ctx context.Context, model string) error {
	if !workspace.IsAllowedModel(model) {
		return workspace.ErrInvalidModel
	}
	if s.db.workspaceID == "" {
		return fmt.Errorf("UpsertModelPreference requires a workspace_id")
	}
	now := sqliteNowMillis()
	const q = `INSERT INTO workspace_preferences (workspace_id, model_preference, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?3)
		ON CONFLICT(workspace_id) DO UPDATE SET
			model_preference = excluded.model_preference,
			updated_at = excluded.updated_at`
	if _, err := s.db.conn.ExecContext(ctx, q, s.db.workspaceID, model, now); err != nil {
		return errWrap("UpsertModelPreference", err)
	}
	return nil
}

// UpsertRepo creates or updates a repo entry. path/description/language/
// current_branch/known_issues/next_planned_step are presence-aware (Ω6,
// 2026-08-20-mcp-surface-spec.md): the ON CONFLICT CASE branches check the
// bound PARAMETER (?4-?8), not excluded.<col> (which is never NULL — it's
// whatever the VALUES clause carried), so a nil pointer preserves the stored
// value instead of wiping it. This closes the PG/SQLite divergence
// (known_issues was already COALESCE-preserved on PG but unconditionally
// overwritten here).
func (s *WorkspaceStore) UpsertRepo(ctx context.Context, p workspace.UpsertRepoParams) (*db.Repo, error) {
	id := uuid.New()
	var issuesArg any
	if p.KnownIssues != nil {
		encoded, err := encodeStringSlice(p.KnownIssues)
		if err != nil {
			return nil, err
		}
		issuesArg = encoded
	}
	now := sqliteNowMillis()
	// known_issues is NOT NULL DEFAULT '[]' in the schema — COALESCE(?8, '[]')
	// in the VALUES clause only matters for the brand-new-row (no-conflict)
	// path; a bare ?8 there would try to insert a literal NULL and violate
	// the NOT NULL constraint whenever a caller's first-ever sync_repo call
	// for a new repo doesn't set known_issues (sync_repo never does — that
	// arg isn't part of its schema). The ON CONFLICT branch below still
	// checks the raw ?8 parameter, not this VALUES-clause default, so
	// preserve-on-omit for existing rows is unaffected.
	const q = `INSERT INTO repos
		(id, workspace_id, name, path, description, language, current_branch,
		 known_issues, next_planned_step, last_activity, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, COALESCE(?8, '[]'), ?9, ?10, ?10, ?10)
		ON CONFLICT(COALESCE(workspace_id,''), name) DO UPDATE SET
			path = CASE WHEN ?4 IS NULL THEN repos.path ELSE excluded.path END,
			description = CASE WHEN ?5 IS NULL THEN repos.description ELSE excluded.description END,
			language = CASE WHEN ?6 IS NULL THEN repos.language ELSE excluded.language END,
			current_branch = CASE WHEN ?7 IS NULL THEN repos.current_branch ELSE excluded.current_branch END,
			known_issues = CASE WHEN ?8 IS NULL THEN repos.known_issues ELSE excluded.known_issues END,
			next_planned_step = CASE WHEN ?9 IS NULL THEN repos.next_planned_step ELSE excluded.next_planned_step END,
			last_activity = excluded.last_activity,
			updated_at = excluded.updated_at`
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), p.Name, nullStringPtr(p.Path),
		nullStringPtr(p.Description), nullStringPtr(p.Language),
		nullStringPtr(p.CurrentBranch), issuesArg,
		nullStringPtr(p.NextPlannedStep), now)
	if err != nil {
		return nil, errWrap("UpsertRepo", err)
	}
	return s.RepoByName(ctx, p.Name)
}
