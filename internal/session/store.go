package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store handles all database operations for the Session bounded context.
type Store struct {
	q           *db.Queries
	dbtx        db.DBTX
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

// SetHandoff records a new session handoff for the next session to pick up.
func (s *Store) SetHandoff(ctx context.Context, p HandoffParams) (*db.SessionHandoff, error) {
	row, err := s.q.CreateSessionHandoff(ctx, db.CreateSessionHandoffParams{
		ProjectID:      toUUID(p.ProjectID),
		RepoName:       toText(p.RepoName),
		Intent:         p.Intent,
		ContextSummary: toText(p.ContextSummary),
		WorkspaceID:    s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("creating session handoff: %w", err)
	}
	return &row, nil
}

// LatestHandoff returns the most recent unresolved handoff, or ErrNotFound.
func (s *Store) LatestHandoff(ctx context.Context) (*db.SessionHandoff, error) {
	row, err := s.q.GetLatestUnresolvedHandoff(ctx, s.workspaceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting latest handoff: %w", err)
	}
	return &row, nil
}

// UpdateSummary writes summary to the most recent unresolved handoff's
// summary_text column. It is a best-effort operation: if no unresolved
// handoff exists the update affects 0 rows and returns nil (not ErrNotFound),
// so the Stop hook is never blocked.
func (s *Store) UpdateSummary(ctx context.Context, summary string) error {
	const q = `UPDATE session_handoffs
		SET summary_text = $1
		WHERE id = (
			SELECT id FROM session_handoffs
			WHERE resolved_at IS NULL
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			ORDER BY created_at DESC
			LIMIT 1
		)`
	_, err := s.dbtx.Exec(ctx, q, summary, s.workspaceID)
	if err != nil {
		return fmt.Errorf("updating session summary: %w", err)
	}
	return nil
}

// Resolve marks a handoff as resolved so it will not appear in future queries.
func (s *Store) Resolve(ctx context.Context, id uuid.UUID) error {
	n, err := s.q.ResolveHandoff(ctx, db.ResolveHandoffParams{
		ID:          id,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return fmt.Errorf("resolving handoff %s: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
