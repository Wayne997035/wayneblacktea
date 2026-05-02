package decision

import (
	"context"
	"fmt"
	"sort"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store handles all database operations for the Decision bounded context.
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
		WorkspaceID:  s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("logging decision %q: %w", p.Title, err)
	}
	return &row, nil
}

// ByRepo returns the most recent decisions for a given repo name.
func (s *Store) ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error) {
	rows, err := s.q.ListDecisionsByRepo(ctx, db.ListDecisionsByRepoParams{
		RepoName:    toText(repoName),
		WorkspaceID: s.workspaceID,
		LimitN:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing decisions for repo %q: %w", repoName, err)
	}
	return rows, nil
}

// All returns the most recent decisions across all repos and projects.
func (s *Store) All(ctx context.Context, limit int32) ([]db.Decision, error) {
	rows, err := s.q.ListAllDecisions(ctx, db.ListAllDecisionsParams{
		WorkspaceID: s.workspaceID,
		LimitN:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing all decisions: %w", err)
	}
	return rows, nil
}

// SearchByCosine returns the top-limit decisions whose embeddings are most
// similar to queryEmbedding, filtered by workspace_id.  Brute-force Go-side
// cosine scan (decisions.embedding is BYTEA, not a pgvector column).
//
// SECURITY: filtered by workspace_id — no cross-workspace data is returned.
func (s *Store) SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.Decision, error) {
	if len(queryEmbedding) == 0 || limit <= 0 {
		return nil, nil
	}

	const q = `SELECT id, project_id, repo_name, title, context, decision, rationale,
		alternatives, created_at, workspace_id, embedding
		FROM decisions
		WHERE embedding IS NOT NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT 200`

	rows, err := s.dbtx.Query(ctx, q, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("decision cosine query: %w", err)
	}
	defer rows.Close()

	type scored struct {
		d   db.Decision
		sim float64
	}
	var candidates []scored
	for rows.Next() {
		var d db.Decision
		var rawEmbed []byte
		if err := rows.Scan(
			&d.ID, &d.ProjectID, &d.RepoName, &d.Title,
			&d.Context, &d.Decision, &d.Rationale, &d.Alternatives,
			&d.CreatedAt, &d.WorkspaceID, &rawEmbed,
		); err != nil {
			continue
		}
		vec := localai.DeserializeEmbedding(rawEmbed)
		if vec == nil {
			continue
		}
		candidates = append(candidates, scored{d: d, sim: localai.CosineSimilarity(queryEmbedding, vec)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating decision cosine results: %w", err)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].sim > candidates[j].sim
	})
	if limit < len(candidates) {
		candidates = candidates[:limit]
	}
	result := make([]db.Decision, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, c.d)
	}
	return result, nil
}

// ByProject returns the most recent decisions for a given project ID.
func (s *Store) ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error) {
	rows, err := s.q.ListDecisionsByProject(ctx, db.ListDecisionsByProjectParams{
		ProjectID:   toUUID(&projectID),
		WorkspaceID: s.workspaceID,
		LimitN:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing decisions for project %s: %w", projectID, err)
	}
	return rows, nil
}
