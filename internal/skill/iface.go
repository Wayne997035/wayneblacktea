package skill

import (
	"context"
	"errors"
)

// ErrNotFound is returned when a Skill row cannot be found for the given ID.
var ErrNotFound = errors.New("skill not found")

// StoreIface is the backend-agnostic contract for the Skill Library bounded context.
// Both the Postgres-backed *Store and the SQLite-backed *SkillStore satisfy this interface.
type StoreIface interface {
	// Add inserts a new skill and returns the persisted record.
	Add(ctx context.Context, p AddParams) (*Skill, error)

	// Search returns skills whose name or description match f.Query (ILIKE/LIKE),
	// scoped to f.WorkspaceID when non-nil, ordered by success_count DESC.
	Search(ctx context.Context, f SearchFilter) ([]*Skill, error)

	// IncrementSuccess increments success_count and sets last_used_at = NOW().
	// Returns ErrNotFound when no matching row exists.
	IncrementSuccess(ctx context.Context, id string, workspaceID *string) (*Skill, error)

	// UpdateFromOutcome increments the success or failure counter, appends an
	// example entry {"outcome_id":"…","notes":"…","at":"…"} to examples, and
	// sets last_used_at = NOW(). Returns ErrNotFound when no matching row exists.
	UpdateFromOutcome(ctx context.Context, p UpdateFromOutcomeParams, workspaceID *string) (*Skill, error)

	// ListRelevant returns skills ordered by success_count DESC, last_used_at DESC,
	// optionally filtered by query (name/description LIKE), scoped to workspaceID.
	ListRelevant(ctx context.Context, workspaceID *string, query string, limit int) ([]*Skill, error)
}

// Compile-time assertion: Postgres Store satisfies StoreIface.
var _ StoreIface = (*Store)(nil)
