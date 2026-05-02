package session

import (
	"context"
	"errors"
	"fmt"
	"sort"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
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

// UpdateEmbedding writes the serialized embedding bytes to the most recent
// unresolved session handoff.  Best-effort: 0 rows updated (no unresolved
// handoff) is not an error.
func (s *Store) UpdateEmbedding(ctx context.Context, embedding []byte) error {
	const q = `UPDATE session_handoffs
		SET embedding = $1
		WHERE id = (
			SELECT id FROM session_handoffs
			WHERE resolved_at IS NULL
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			ORDER BY created_at DESC
			LIMIT 1
		)`
	if _, err := s.dbtx.Exec(ctx, q, embedding, s.workspaceID); err != nil {
		return fmt.Errorf("updating session embedding: %w", err)
	}
	return nil
}

// handoffEmbedRow holds the minimal fields needed for cosine recall.
type handoffEmbedRow struct {
	id        uuid.UUID
	intent    string
	summaryTx *string
	embedding []byte
}

// SearchByCosine returns the top-limit session handoffs whose embeddings are
// most similar to queryEmbedding, filtered by workspace_id.  Only handoffs
// with non-null embeddings are considered.  Similarity is computed on the Go
// side (brute-force scan) because session_handoffs.embedding is BYTEA and
// not yet a pgvector column.
//
// SECURITY: filtered by workspace_id — no cross-workspace data is returned.
func (s *Store) SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.SessionHandoff, error) {
	if len(queryEmbedding) == 0 || limit <= 0 {
		return nil, nil
	}

	const q = `SELECT id, intent, summary_text, embedding
		FROM session_handoffs
		WHERE embedding IS NOT NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT 200` // scan at most 200 recent handoffs (personal-OS scale)

	rows, err := s.dbtx.Query(ctx, q, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("session cosine query: %w", err)
	}
	defer rows.Close()

	type scored struct {
		row handoffEmbedRow
		sim float64
	}
	var candidates []scored
	for rows.Next() {
		var r handoffEmbedRow
		if err := rows.Scan(&r.id, &r.intent, &r.summaryTx, &r.embedding); err != nil {
			continue
		}
		vec := localai.DeserializeEmbedding(r.embedding)
		if vec == nil {
			continue
		}
		sim := localai.CosineSimilarity(queryEmbedding, vec)
		candidates = append(candidates, scored{row: r, sim: sim})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating session cosine results: %w", err)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].sim > candidates[j].sim
	})

	if limit < len(candidates) {
		candidates = candidates[:limit]
	}

	result := make([]db.SessionHandoff, 0, len(candidates))
	for _, c := range candidates {
		h := db.SessionHandoff{
			ID:     c.row.id,
			Intent: c.row.intent,
		}
		if c.row.summaryTx != nil {
			h.ContextSummary = pgtype.Text{String: *c.row.summaryTx, Valid: true}
		}
		result = append(result, h)
	}
	return result, nil
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
