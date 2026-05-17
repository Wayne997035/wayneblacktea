package session

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
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

// marshalNextActions serialises []NextAction to JSON bytes, returning '[]'
// (the empty JSON array) when the slice is nil or empty.
func marshalNextActions(actions []NextAction) ([]byte, error) {
	if len(actions) == 0 {
		return []byte("[]"), nil
	}
	b, err := json.Marshal(actions)
	if err != nil {
		return nil, fmt.Errorf("marshalling next_actions: %w", err)
	}
	return b, nil
}

// unmarshalNextActions deserialises raw JSON bytes into []NextAction.
// nil or empty bytes → empty slice (not an error).
func unmarshalNextActions(raw []byte) ([]NextAction, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "[]" {
		return []NextAction{}, nil
	}
	var actions []NextAction
	if err := json.Unmarshal(raw, &actions); err != nil {
		return nil, fmt.Errorf("unmarshalling next_actions: %w", err)
	}
	return actions, nil
}

// SetHandoff records a new session handoff for the next session to pick up.
// Returns a descriptive error wrapping sanitize.ErrTagNoise if any text field
// contains tool-call serialization fragments (XML tags leaked from the MCP
// harness), which are never valid user input.
func (s *Store) SetHandoff(ctx context.Context, p HandoffParams) (*db.SessionHandoff, error) {
	if err := sanitize.ValidateNoTagNoise(p.Intent); err != nil {
		return nil, fmt.Errorf("set_session_handoff: intent %w", err)
	}
	if err := sanitize.ValidateNoTagNoise(p.ContextSummary); err != nil {
		return nil, fmt.Errorf("set_session_handoff: context_summary %w", err)
	}
	if err := sanitize.ValidateNoTagNoise(p.RepoName); err != nil {
		return nil, fmt.Errorf("set_session_handoff: repo_name %w", err)
	}

	nextActionsJSON, err := marshalNextActions(p.NextActions)
	if err != nil {
		return nil, fmt.Errorf("set_session_handoff: %w", err)
	}

	// Hand-rolled INSERT so next_actions column (migration 000046) is persisted
	// without requiring a sqlc regen.
	const q = `INSERT INTO session_handoffs
		(project_id, repo_name, intent, context_summary, next_actions, workspace_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, project_id, repo_name, intent, context_summary, resolved_at, created_at, workspace_id, next_actions`
	rows, err := s.dbtx.Query(ctx, q,
		toUUID(p.ProjectID), toText(p.RepoName), p.Intent, toText(p.ContextSummary),
		nextActionsJSON, s.workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("creating session handoff: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if rErr := rows.Err(); rErr != nil {
			return nil, fmt.Errorf("iterating create session handoff: %w", rErr)
		}
		return nil, fmt.Errorf("creating session handoff: no row returned")
	}
	var h db.SessionHandoff
	if err := rows.Scan(
		&h.ID, &h.ProjectID, &h.RepoName, &h.Intent, &h.ContextSummary,
		&h.ResolvedAt, &h.CreatedAt, &h.WorkspaceID, &h.NextActions,
	); err != nil {
		return nil, fmt.Errorf("scanning created session handoff: %w", err)
	}
	return &h, nil
}

// LatestHandoff returns the most recent unresolved handoff, or ErrNotFound.
func (s *Store) LatestHandoff(ctx context.Context) (*db.SessionHandoff, error) {
	// Hand-rolled query so next_actions (migration 000046) is included.
	const q = `SELECT id, project_id, repo_name, intent, context_summary, resolved_at, created_at, workspace_id, next_actions
		FROM session_handoffs
		WHERE resolved_at IS NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT 1`
	rows, err := s.dbtx.Query(ctx, q, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("getting latest handoff: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if rErr := rows.Err(); rErr != nil {
			return nil, fmt.Errorf("iterating latest handoff: %w", rErr)
		}
		return nil, ErrNotFound
	}
	var h db.SessionHandoff
	if err := rows.Scan(
		&h.ID, &h.ProjectID, &h.RepoName, &h.Intent, &h.ContextSummary,
		&h.ResolvedAt, &h.CreatedAt, &h.WorkspaceID, &h.NextActions,
	); err != nil {
		return nil, fmt.Errorf("scanning latest handoff: %w", err)
	}
	return &h, nil
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

// UpdateEmbeddingByID writes the serialized embedding bytes to the session
// handoff with the given ID, scoped to workspace_id to prevent cross-workspace
// overwrites.  Best-effort: 0 rows updated is not an error.
func (s *Store) UpdateEmbeddingByID(ctx context.Context, id uuid.UUID, embedding []byte) error {
	const q = `UPDATE session_handoffs SET embedding = $1
		WHERE id = $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)`
	if _, err := s.dbtx.Exec(ctx, q, embedding, id, s.workspaceID); err != nil {
		return fmt.Errorf("updating session embedding by id: %w", err)
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

// HandoffsSince returns handoffs created or resolved on or after since,
// scoped to the configured workspace. Used by the timeline aggregator.
func (s *Store) HandoffsSince(ctx context.Context, since time.Time, limit int) ([]db.SessionHandoff, error) {
	rows, err := s.q.HandoffsSince(ctx, db.HandoffsSinceParams{
		WorkspaceID: s.workspaceID,
		Since:       pgtype.Timestamptz{Time: since, Valid: true},
		LimitN:      int32(limit), //nolint:gosec // limit is bounded by caller (10000 max)
	})
	if err != nil {
		return nil, fmt.Errorf("handoffs since %s: %w", since, err)
	}
	out := make([]db.SessionHandoff, 0, len(rows))
	out = append(out, rows...)
	return out, nil
}

// HandoffsByRepo returns recent handoffs whose repo_name matches repoName,
// scoped to the configured workspace, newest first. Used by the workspace
// repo overview to surface recent context for a single repo. Hand-written
// query so the sqlc surface stays untouched.
func (s *Store) HandoffsByRepo(ctx context.Context, repoName string, limit int) ([]db.SessionHandoff, error) {
	const q = `SELECT id, project_id, repo_name, intent, context_summary,
		resolved_at, created_at, workspace_id
		FROM session_handoffs
		WHERE repo_name = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY created_at DESC, id DESC
		LIMIT $3`
	rows, err := s.dbtx.Query(ctx, q, repoName, s.workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing handoffs for repo %q: %w", repoName, err)
	}
	defer rows.Close()
	out := make([]db.SessionHandoff, 0, limit)
	for rows.Next() {
		var h db.SessionHandoff
		if err := rows.Scan(
			&h.ID, &h.ProjectID, &h.RepoName, &h.Intent, &h.ContextSummary,
			&h.ResolvedAt, &h.CreatedAt, &h.WorkspaceID,
		); err != nil {
			return nil, fmt.Errorf("scanning handoff: %w", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating handoffs: %w", err)
	}
	return out, nil
}

// MarkNextActionDone updates next_actions[step].status = "done" for the
// handoff identified by id, scoped to the caller's workspace_id.
// Returns ErrNotFound when no matching handoff exists in the workspace.
//
// SECURITY: workspace isolation enforced — the handoff_id is validated against
// the configured workspace_id before any mutation.
func (s *Store) MarkNextActionDone(ctx context.Context, handoffID uuid.UUID, step int) (*db.SessionHandoff, error) {
	// 1. Load the handoff with workspace isolation.
	const loadQ = `SELECT id, project_id, repo_name, intent, context_summary, resolved_at, created_at, workspace_id, next_actions
		FROM session_handoffs
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		LIMIT 1`
	loadRows, err := s.dbtx.Query(ctx, loadQ, handoffID, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("mark_next_action_done: load handoff %s: %w", handoffID, err)
	}
	defer loadRows.Close()
	if !loadRows.Next() {
		if rErr := loadRows.Err(); rErr != nil {
			return nil, fmt.Errorf("mark_next_action_done: iterating handoff %s: %w", handoffID, rErr)
		}
		return nil, ErrNotFound
	}
	var h db.SessionHandoff
	if err := loadRows.Scan(
		&h.ID, &h.ProjectID, &h.RepoName, &h.Intent, &h.ContextSummary,
		&h.ResolvedAt, &h.CreatedAt, &h.WorkspaceID, &h.NextActions,
	); err != nil {
		return nil, fmt.Errorf("mark_next_action_done: scan handoff %s: %w", handoffID, err)
	}
	loadRows.Close()

	// 2. Parse, update step.
	actions, err := unmarshalNextActions(h.NextActions)
	if err != nil {
		return nil, fmt.Errorf("mark_next_action_done: parse next_actions: %w", err)
	}
	updated := false
	for i := range actions {
		if actions[i].Step == step {
			actions[i].Status = NextActionDone
			updated = true
			break
		}
	}
	if !updated {
		return nil, fmt.Errorf("mark_next_action_done: step %d not found in handoff %s", step, handoffID)
	}

	// 3. Marshal and write back.
	newJSON, err := marshalNextActions(actions)
	if err != nil {
		return nil, fmt.Errorf("mark_next_action_done: marshal next_actions: %w", err)
	}

	const updateQ = `UPDATE session_handoffs
		SET next_actions = $1
		WHERE id = $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		RETURNING id, project_id, repo_name, intent, context_summary, resolved_at, created_at, workspace_id, next_actions`
	updateRows, err := s.dbtx.Query(ctx, updateQ, newJSON, handoffID, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("mark_next_action_done: update handoff %s: %w", handoffID, err)
	}
	defer updateRows.Close()
	if !updateRows.Next() {
		if rErr := updateRows.Err(); rErr != nil {
			return nil, fmt.Errorf("mark_next_action_done: iterating update result %s: %w", handoffID, rErr)
		}
		return nil, ErrNotFound
	}
	var updated2 db.SessionHandoff
	if err := updateRows.Scan(
		&updated2.ID, &updated2.ProjectID, &updated2.RepoName, &updated2.Intent, &updated2.ContextSummary,
		&updated2.ResolvedAt, &updated2.CreatedAt, &updated2.WorkspaceID, &updated2.NextActions,
	); err != nil {
		return nil, fmt.Errorf("mark_next_action_done: scan update result %s: %w", handoffID, err)
	}
	return &updated2, nil
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
