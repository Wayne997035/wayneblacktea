package session

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Session bounded
// context.
type StoreIface interface {
	SetHandoff(ctx context.Context, p HandoffParams) (*db.SessionHandoff, error)
	LatestHandoff(ctx context.Context) (*db.SessionHandoff, error)
	Resolve(ctx context.Context, id uuid.UUID) error
	// UpdateSummary writes a plain-text session summary to the most recent
	// unresolved handoff's summary_text column. Used by the Stop hook after
	// SummarizeSession produces a ≤500-char digest. Silently no-ops when no
	// unresolved handoff exists (first-ever session, or already resolved).
	UpdateSummary(ctx context.Context, summary string) error
}

var _ StoreIface = (*Store)(nil)
