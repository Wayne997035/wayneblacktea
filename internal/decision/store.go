package decision

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// Store handles all database operations for the Decision bounded context.
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

// Log records a new architectural decision.
func (s *Store) Log(ctx context.Context, p LogParams) (*db.Decision, error) {
	row, err := s.q.CreateDecision(ctx, db.CreateDecisionParams{
		ProjectID:    toUUID(p.ProjectID),
		RepoName:     toText(p.RepoName),
		Title:        p.Title,
		Context:      p.Context,
		Decision:     p.Decision,
		Rationale:    p.Rationale,
		Alternatives: toText(p.Alternatives),
	})
	if err != nil {
		return nil, fmt.Errorf("logging decision %q: %w", p.Title, err)
	}
	return &row, nil
}

// ByRepo returns the most recent decisions for a given repo name.
func (s *Store) ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error) {
	rows, err := s.q.ListDecisionsByRepo(ctx, db.ListDecisionsByRepoParams{
		RepoName: toText(repoName),
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing decisions for repo %q: %w", repoName, err)
	}
	return rows, nil
}

// All returns the most recent decisions across all repos and projects.
func (s *Store) All(ctx context.Context, limit int32) ([]db.Decision, error) {
	rows, err := s.q.ListAllDecisions(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("listing all decisions: %w", err)
	}
	return rows, nil
}

// ByProject returns the most recent decisions for a given project ID.
func (s *Store) ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error) {
	rows, err := s.q.ListDecisionsByProject(ctx, db.ListDecisionsByProjectParams{
		ProjectID: toUUID(&projectID),
		Limit:     limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing decisions for project %s: %w", projectID, err)
	}
	return rows, nil
}
