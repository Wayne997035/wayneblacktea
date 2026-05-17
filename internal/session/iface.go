package session

import (
	"context"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Session bounded
// context.
type StoreIface interface {
	SetHandoff(ctx context.Context, p HandoffParams) (*db.SessionHandoff, error)
	LatestHandoff(ctx context.Context) (*db.SessionHandoff, error)
	Resolve(ctx context.Context, id uuid.UUID) error
	// MarkNextActionDone sets next_actions[step].status = "done" for the
	// handoff identified by id, scoped to the caller's workspace.
	// Returns ErrNotFound when no matching handoff exists in the workspace.
	MarkNextActionDone(ctx context.Context, handoffID uuid.UUID, step int) (*db.SessionHandoff, error)
	// UpdateSummary writes a plain-text session summary to the most recent
	// unresolved handoff's summary_text column. Used by the Stop hook after
	// SummarizeSession produces a ≤500-char digest. Silently no-ops when no
	// unresolved handoff exists (first-ever session, or already resolved).
	UpdateSummary(ctx context.Context, summary string) error
	// UpdateEmbedding writes serialized embedding bytes to the most recent
	// unresolved handoff for cosine similarity recall at SessionStart.
	UpdateEmbedding(ctx context.Context, embedding []byte) error
	// UpdateEmbeddingByID writes serialized embedding bytes to the handoff with
	// the given ID. Preferred over UpdateEmbedding when the caller holds the
	// specific row ID (avoids race under concurrent SetHandoff calls).
	UpdateEmbeddingByID(ctx context.Context, id uuid.UUID, embedding []byte) error
	// SearchByCosine returns the top-limit handoffs most similar to queryEmbedding.
	// SECURITY: scoped to workspace_id.
	SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.SessionHandoff, error)
	// HandoffsSince returns handoffs created or resolved on or after since,
	// scoped to the configured workspace. Used by the timeline aggregator.
	HandoffsSince(ctx context.Context, since time.Time, limit int) ([]db.SessionHandoff, error)
	// HandoffsByRepo returns recent handoffs whose repo_name matches the given
	// value, scoped to the configured workspace. Ordered by created_at DESC.
	// Used by the workspace repo overview to surface recent context for a repo.
	HandoffsByRepo(ctx context.Context, repoName string, limit int) ([]db.SessionHandoff, error)
}

var _ StoreIface = (*Store)(nil)
