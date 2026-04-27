package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// Store handles all database operations for the Workspace bounded context.
type Store struct {
	q           *db.Queries
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given DBTX scoped to the optional
// workspace. nil workspaceID = legacy unscoped mode.
func NewStore(dbtx db.DBTX, workspaceID *uuid.UUID) *Store {
	return &Store{q: db.New(dbtx), workspaceID: toUUID(workspaceID)}
}

// WithTx returns a Store bound to tx, preserving the workspace scope.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), workspaceID: s.workspaceID}
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

// UpsertRepo creates or updates a repo entry.
func (s *Store) UpsertRepo(ctx context.Context, p UpsertRepoParams) (*db.Repo, error) {
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
