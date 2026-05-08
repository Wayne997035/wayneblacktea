package vision

import (
	"context"

	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Vision bounded context.
// Postgres-backed *Store and SQLite-backed *SQLiteStore both satisfy this interface.
type StoreIface interface {
	// Add inserts a new vision item and returns the persisted record.
	Add(ctx context.Context, p AddVisionParams) (*VisionItem, error)

	// List returns vision items, optionally filtered by status and/or initiative.
	// When filter.Status is empty, all non-dismissed items are returned.
	// Results are ordered by created_at DESC.
	List(ctx context.Context, filter ListVisionFilter) ([]VisionItemSummary, error)

	// Update applies non-nil fields in p to the vision item with the given id.
	// Returns ErrNotFound if the id does not exist in the store's workspace scope.
	Update(ctx context.Context, id uuid.UUID, p UpdateVisionParams) (*VisionItem, error)

	// GetByID returns the full vision item by id.
	// Returns ErrNotFound if the id does not exist.
	GetByID(ctx context.Context, id uuid.UUID) (*VisionItem, error)

	// Promote marks a vision item as promoted and records the resulting task id.
	// Returns ErrNotFound if the id does not exist.
	Promote(ctx context.Context, id uuid.UUID, p PromoteParams) (*VisionItem, error)
}

// Compile-time assertion: pg-backed Store implements StoreIface.
var _ StoreIface = (*Store)(nil)
