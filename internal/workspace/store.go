package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// Store handles all database operations for the Workspace bounded context.
type Store struct {
	q *db.Queries
}

// NewStore returns a Store backed by the given DBTX (pool or transaction).
func NewStore(dbtx db.DBTX) *Store {
	return &Store{q: db.New(dbtx)}
}

// WithTx returns a Store bound to tx, for use in multi-store transactions.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx)}
}

func toText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

// ActiveRepos returns all repos with status = 'active', ordered by last_activity.
func (s *Store) ActiveRepos(ctx context.Context) ([]db.Repo, error) {
	rows, err := s.q.ListActiveRepos(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing active repos: %w", err)
	}
	return rows, nil
}

// RepoByName returns a single repo by unique name, or ErrNotFound.
func (s *Store) RepoByName(ctx context.Context, name string) (*db.Repo, error) {
	row, err := s.q.GetRepoByName(ctx, name)
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
	})
	if err != nil {
		return nil, fmt.Errorf("upserting repo %q: %w", p.Name, err)
	}
	return &row, nil
}
