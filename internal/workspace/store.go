package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store handles all database operations for the Workspace bounded context.
type Store struct {
	q           *db.Queries
	dbtx        db.DBTX // retained for hand-written queries outside of sqlc
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given DBTX scoped to the optional
// workspace. nil workspaceID = legacy unscoped mode.
func NewStore(dbtx db.DBTX, workspaceID *uuid.UUID) *Store {
	return &Store{q: db.New(dbtx), dbtx: dbtx, workspaceID: toUUID(workspaceID)}
}

// WithTx returns a Store bound to tx, preserving the workspace scope.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), dbtx: tx, workspaceID: s.workspaceID}
}

func toText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// ActiveRepos returns all repos with status = 'active', ordered by last_activity.
func (s *Store) ActiveRepos(ctx context.Context) ([]db.Repo, error) {
	rows, err := s.q.ListActiveRepos(ctx, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing active repos: %w", err)
	}
	return rows, nil
}

// RepoByName returns a single repo by unique name, or ErrNotFound.
func (s *Store) RepoByName(ctx context.Context, name string) (*db.Repo, error) {
	row, err := s.q.GetRepoByName(ctx, db.GetRepoByNameParams{
		Name:        name,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying repo %q: %w", name, err)
	}
	return &row, nil
}

// RepoByID returns a single repo by primary key UUID, scoped to the configured
// workspace. Returns ErrNotFound when no row matches. Hand-written query (not
// sqlc) so the change is contained to the Go store layer.
func (s *Store) RepoByID(ctx context.Context, id uuid.UUID) (*db.Repo, error) {
	const q = `SELECT id, name, path, description, language, status,
		current_branch, known_issues, next_planned_step, last_activity,
		created_at, updated_at, workspace_id
		FROM repos
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		LIMIT 1`
	row := s.dbtx.QueryRow(ctx, q, id, s.workspaceID)
	var r db.Repo
	if err := row.Scan(
		&r.ID, &r.Name, &r.Path, &r.Description, &r.Language, &r.Status,
		&r.CurrentBranch, &r.KnownIssues, &r.NextPlannedStep, &r.LastActivity,
		&r.CreatedAt, &r.UpdatedAt, &r.WorkspaceID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying repo by id %s: %w", id, err)
	}
	return &r, nil
}

// UpsertRepo creates or updates a repo entry.
// workspaceID must be non-nil: after migration 000028 the unique constraint is
// (workspace_id, name), so ON CONFLICT does not fire for NULL workspace_id.
func (s *Store) UpsertRepo(ctx context.Context, p UpsertRepoParams) (*db.Repo, error) {
	if !s.workspaceID.Valid {
		return nil, fmt.Errorf("UpsertRepo requires a non-nil workspaceID after migration 000028")
	}
	row, err := s.q.UpsertRepo(ctx, db.UpsertRepoParams{
		Name:            p.Name,
		Path:            toText(p.Path),
		Description:     toText(p.Description),
		Language:        toText(p.Language),
		CurrentBranch:   toText(p.CurrentBranch),
		KnownIssues:     p.KnownIssues,
		NextPlannedStep: toText(p.NextPlannedStep),
		WorkspaceID:     s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("upserting repo %q: %w", p.Name, err)
	}
	return &row, nil
}
