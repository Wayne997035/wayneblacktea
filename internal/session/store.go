package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// Store handles all database operations for the Session bounded context.
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

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// SetHandoff records a new session handoff for the next session to pick up.
func (s *Store) SetHandoff(ctx context.Context, p HandoffParams) (*db.SessionHandoff, error) {
	row, err := s.q.CreateSessionHandoff(ctx, db.CreateSessionHandoffParams{
		ProjectID:      toUUID(p.ProjectID),
		RepoName:       toText(p.RepoName),
		Intent:         p.Intent,
		ContextSummary: toText(p.ContextSummary),
	})
	if err != nil {
		return nil, fmt.Errorf("creating session handoff: %w", err)
	}
	return &row, nil
}

// LatestHandoff returns the most recent unresolved handoff, or ErrNotFound.
func (s *Store) LatestHandoff(ctx context.Context) (*db.SessionHandoff, error) {
	row, err := s.q.GetLatestUnresolvedHandoff(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting latest handoff: %w", err)
	}
	return &row, nil
}

// Resolve marks a handoff as resolved so it will not appear in future queries.
func (s *Store) Resolve(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.ResolveHandoff(ctx, id)
	if err != nil {
		return fmt.Errorf("resolving handoff %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
