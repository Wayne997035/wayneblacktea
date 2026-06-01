package atom

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the MemoryAtom bounded
// context. Postgres-backed *Store and SQLite-backed *AtomStore both satisfy
// this interface.
type StoreIface interface {
	// AddAtom inserts a new atom and returns the persisted record.
	AddAtom(ctx context.Context, p AddAtomParams) (*Atom, error)

	// AddLink inserts a directed link between two atoms.
	// If the (from_atom_id, to_atom_id, link_type) triple already exists the
	// call is a no-op (ON CONFLICT DO NOTHING).
	AddLink(ctx context.Context, p AddLinkParams) error

	// ListByParent returns all atoms whose parent_table and parent_id match.
	ListByParent(ctx context.Context, parentTable string, parentID uuid.UUID) ([]Atom, error)

	// Traverse does BFS from startAtomID up to depth hops (capped at 5),
	// returning all visited atoms and the links that connect them.
	// Total atoms returned is capped at 50.
	Traverse(ctx context.Context, startAtomID uuid.UUID, depth int) (*TraverseResult, error)

	// Search returns atoms whose content, keywords, or tags contain query
	// (ILIKE on Postgres, LIKE on SQLite). Scoped to workspaceID when non-nil.
	Search(ctx context.Context, workspaceID *uuid.UUID, query string, limit int) ([]Atom, error)

	// PruneAtoms hard-deletes memory_atoms rows older than cutoff.
	// Called daily by the decay.Pruner to enforce the 90-day TTL.
	PruneAtoms(ctx context.Context, cutoff time.Time) (int64, error)

	// SetDigestStatus updates the digest_status and error_msg for the given atom.
	// status must be one of "pending", "done", "failed".
	// errMsg is stored only when status="failed"; pass "" otherwise.
	SetDigestStatus(ctx context.Context, atomID uuid.UUID, status string, errMsg string) error

	// CountByDigestStatus returns the number of atoms with the given digest_status
	// in the given workspace (nil = all workspaces).
	CountByDigestStatus(ctx context.Context, workspaceID *uuid.UUID, status string) (int64, error)

	// CountTotal returns the total number of atoms scoped to the given workspace
	// (nil = all workspaces). Used by the M9 consolidation cron to compare
	// against the configured capacity threshold.
	CountTotal(ctx context.Context, workspaceID *uuid.UUID) (int64, error)
}

// Compile-time assertion: Postgres Store satisfies StoreIface.
var _ StoreIface = (*Store)(nil)
