package proposal

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// Store handles all database operations for the Proposal bounded context.
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

// Create records a new pending proposal. Payload is opaque JSON; the caller is
// responsible for marshalling the entity-specific shape.
//
// If CreateParams.WorkspaceID is set, it overrides the store's workspace scope
// (used e.g. for tests or rare cross-workspace proposals). When nil, the
// store's configured workspace is used.
func (s *Store) Create(ctx context.Context, p CreateParams) (*db.PendingProposal, error) {
	ws := s.workspaceID
	if p.WorkspaceID != nil {
		ws = toUUID(p.WorkspaceID)
	}
	row, err := s.q.CreatePendingProposal(ctx, db.CreatePendingProposalParams{
		WorkspaceID: ws,
		Type:        string(p.Type),
		Payload:     p.Payload,
		ProposedBy:  toText(p.ProposedBy),
	})
	if err != nil {
		return nil, fmt.Errorf("creating proposal: %w", err)
	}
	return &row, nil
}

// Get returns a single proposal by ID. Returns ErrNotFound when missing.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (*db.PendingProposal, error) {
	row, err := s.q.GetPendingProposal(ctx, db.GetPendingProposalParams{
		ID:          id,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting proposal %s: %w", id, err)
	}
	return &row, nil
}

// ListPending returns all proposals awaiting user resolution, newest first.
func (s *Store) ListPending(ctx context.Context) ([]db.PendingProposal, error) {
	rows, err := s.q.ListPendingProposals(ctx, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing pending proposals: %w", err)
	}
	return rows, nil
}

// Resolve marks a pending proposal as accepted or rejected. Already-resolved
// proposals return ErrNotFound (idempotent rather than overwrite).
func (s *Store) Resolve(ctx context.Context, id uuid.UUID, status Status) (*db.PendingProposal, error) {
	if status != StatusAccepted && status != StatusRejected {
		return nil, fmt.Errorf("resolve: invalid status %q (want accepted or rejected)", status)
	}
	row, err := s.q.ResolvePendingProposal(ctx, db.ResolvePendingProposalParams{
		ID:          id,
		Status:      string(status),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("resolving proposal %s: %w", id, err)
	}
	return &row, nil
}
