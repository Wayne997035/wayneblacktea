package reflection

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Reflection domain.
type StoreIface interface {
	Create(ctx context.Context, p CreateParams) (*Reflection, error)
	List(ctx context.Context, p ListParams) ([]*Reflection, error)
	GetLatest(ctx context.Context, workspaceID *uuid.UUID, reflType string) (*Reflection, error)
	ByRelatedEntity(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID, limit int) ([]*Reflection, error)
	RecentWithPatterns(ctx context.Context, workspaceID *uuid.UUID, since time.Time, limit int) ([]*Reflection, error)
	// PruneOlderThan hard-deletes reflection rows with created_at < cutoff.
	// Called daily by the scheduler to enforce the 180-day TTL per
	// backend-security-design.md §1.3.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Compile-time assertion: Store must satisfy StoreIface.
var _ StoreIface = (*Store)(nil)
