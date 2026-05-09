package procedural

import (
	"context"

	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the ProceduralMemory
// bounded context. Postgres-backed *Store and SQLite-backed *ProceduralStore
// both satisfy this interface.
type StoreIface interface {
	// Add inserts a new procedural memory and returns the persisted record.
	Add(ctx context.Context, p AddParams) (*ProceduralMemory, error)

	// Query returns procedural memories matching the filter.
	// Searches title, when_to_use, and approach_md with ILIKE (Postgres)
	// or LIKE (SQLite) when Keywords is non-empty.
	// Results ordered by success_count DESC, created_at DESC.
	Query(ctx context.Context, f QueryFilter) ([]ProceduralMemory, error)

	// MarkUsed increments success_count and sets last_used_at = now.
	// Returns ErrNotFound if the id does not exist.
	MarkUsed(ctx context.Context, id uuid.UUID) (*ProceduralMemory, error)
}

// Compile-time assertion: Postgres Store satisfies StoreIface.
var _ StoreIface = (*Store)(nil)
