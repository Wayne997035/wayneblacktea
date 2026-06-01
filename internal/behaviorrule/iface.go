package behaviorrule

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the BehaviorRule domain.
type StoreIface interface {
	// Propose inserts a new behavior rule with status='proposed' and returns
	// the persisted record.
	Propose(ctx context.Context, p CreateParams) (*BehaviorRule, error)

	// List returns behavior rules matching the filter, ordered by created_at DESC.
	List(ctx context.Context, p ListParams) ([]*BehaviorRule, error)

	// GetByID returns the behavior rule with the given ID.
	// Returns ErrNotFound when no matching rule exists.
	GetByID(ctx context.Context, id uuid.UUID) (*BehaviorRule, error)

	// ApplyOutcome applies the confidence formula atomically and conditionally
	// transitions the status:
	//   outcome='success' AND status='proposed' → status='active', confidence += 0.05 (cap 1.00)
	//   outcome='success' AND status='active'   → confidence += 0.05 (cap 1.00)
	//   outcome='failure'                        → confidence -= 0.10 (floor 0.00)
	// The update is atomic (single UPDATE statement) to prevent race conditions.
	// Returns ErrNotFound when no matching rule exists.
	ApplyOutcome(ctx context.Context, id uuid.UUID, outcome string) (*BehaviorRule, error)

	// Deprecate sets the rule's status to 'deprecated'. Idempotent: returns
	// success when the rule is already deprecated.
	// Returns ErrNotFound when no matching rule exists.
	Deprecate(ctx context.Context, id uuid.UUID) (*BehaviorRule, error)

	// PruneOlderThan hard-deletes behavior_rules rows where
	// status IN ('rejected','deprecated') AND created_at < cutoff.
	// Active and proposed rows are NEVER deleted regardless of age.
	// Called daily by the scheduler to enforce the 365-day TTL per
	// backend-security-design.md §1.3.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Compile-time assertion: Store must satisfy StoreIface.
var _ StoreIface = (*Store)(nil)
