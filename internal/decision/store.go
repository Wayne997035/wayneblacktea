package decision

import (
	"context"
	"fmt"
	"sort"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/pgconv"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
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
	return &Store{q: db.New(dbtx), dbtx: dbtx, workspaceID: pgconv.ToUUID(workspaceID)}
}

// WithTx returns a Store bound to tx, preserving the workspace scope.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), dbtx: tx, workspaceID: s.workspaceID}
}

// Log records a new architectural decision.
// Returns a descriptive error wrapping sanitize.ErrTagNoise if any text field
// contains tool-call serialization fragments (XML tags leaked from the MCP
// harness), which are never valid user input.
func (s *Store) Log(ctx context.Context, p LogParams) (*db.Decision, error) {
	if err := sanitize.ValidateNoTagNoise(p.Title); err != nil {
		return nil, fmt.Errorf("log_decision: title %w", err)
	}
	if err := sanitize.ValidateNoTagNoise(p.RepoName); err != nil {
		return nil, fmt.Errorf("log_decision: repo_name %w", err)
	}
	if err := sanitize.ValidateNoTagNoise(p.Rationale); err != nil {
		return nil, fmt.Errorf("log_decision: rationale %w", err)
	}
	if err := sanitize.ValidateNoTagNoise(p.Alternatives); err != nil {
		return nil, fmt.Errorf("log_decision: alternatives %w", err)
	}
	if err := sanitize.ValidateNoTagNoise(p.Context); err != nil {
		return nil, fmt.Errorf("log_decision: context %w", err)
	}
	if err := sanitize.ValidateNoTagNoise(p.Decision); err != nil {
		return nil, fmt.Errorf("log_decision: decision %w", err)
	}
	row, err := s.q.CreateDecision(ctx, db.CreateDecisionParams{
		ProjectID:    pgconv.ToUUID(p.ProjectID),
		TaskID:       pgconv.ToUUID(p.TaskID),
		RepoName:     pgconv.ToText(p.RepoName),
		Title:        p.Title,
		Context:      p.Context,
		Decision:     p.Decision,
		Rationale:    p.Rationale,
		Alternatives: pgconv.ToText(p.Alternatives),
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
		RepoName:    pgconv.ToText(repoName),
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
// similar to queryEmbedding, filtered by workspace_id and embedding_provider.
// Provider filtering prevents cross-provider dimension mismatches
// (CosineSimilarity returns 0 when dims differ). Brute-force Go-side cosine
// scan (decisions.embedding is BYTEA, not a pgvector column).
//
// NOTE: decisions.embedding has no active writer in the current sprint.
// When no rows match the provider filter this function returns nil, nil —
// the caller falls back to recency order (intended). The writer is a follow-up
// task; the provider filter ensures this is ready when it ships.
//
// SECURITY: filtered by workspace_id — no cross-workspace data is returned.
func (s *Store) SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.Decision, error) {
	if len(queryEmbedding) == 0 || limit <= 0 {
		return nil, nil
	}

	// Filter by provider so legacy 'hashed' rows are excluded when real provider is active.
	activeProvider := localai.ProviderTagFromDim(len(queryEmbedding))
	const q = `SELECT id, project_id, repo_name, title, context, decision, rationale,
		alternatives, created_at, workspace_id, task_id, embedding
		FROM decisions
		WHERE embedding IS NOT NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND (embedding_provider = $2 OR embedding_provider IS NULL AND $2 = 'hashed')
		ORDER BY created_at DESC
		LIMIT 200`

	rows, err := s.dbtx.Query(ctx, q, s.workspaceID, activeProvider)
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
			&d.CreatedAt, &d.WorkspaceID, &d.TaskID, &rawEmbed,
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

// ByTask returns the most recent decisions for a given task ID.
// SECURITY: scoped to workspace_id.
func (s *Store) ByTask(ctx context.Context, taskID uuid.UUID, limit int32) ([]db.Decision, error) {
	rows, err := s.q.ListDecisionsByTaskID(ctx, db.ListDecisionsByTaskIDParams{
		TaskID:      pgconv.ToUUID(&taskID),
		WorkspaceID: s.workspaceID,
		LimitN:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing decisions for task %s: %w", taskID, err)
	}
	return rows, nil
}

// ByProject returns the most recent decisions for a given project ID.
func (s *Store) ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error) {
	rows, err := s.q.ListDecisionsByProject(ctx, db.ListDecisionsByProjectParams{
		ProjectID:   pgconv.ToUUID(&projectID),
		WorkspaceID: s.workspaceID,
		LimitN:      limit,
	})
	if err != nil {
		return nil, fmt.Errorf("listing decisions for project %s: %w", projectID, err)
	}
	return rows, nil
}
