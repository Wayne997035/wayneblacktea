package proposal

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// ErrAlreadyResolved is returned by AcceptOrchestration when the proposal
// fetched by ReadPending is not in StatusPending. Unlike
// gtd.BeginTaskOrchestration's idempotency shortcut ("already in the target
// state" is a silent success), accepting a non-pending proposal is always an
// error here: there is no "re-accepting" a proposal — accepted stays
// accepted, rejected stays rejected — so silently returning the stale row
// would hide a caller bug or a race with a concurrent resolver instead of
// surfacing it.
var ErrAlreadyResolved = errors.New("proposal: already resolved")

// AcceptAdapter is the narrow, per-backend seam AcceptOrchestration drives to
// execute proposal acceptance's control flow — read pending → prepare
// out-of-band work → begin tx → materialise → resolve → commit — without
// seeing pgx or database/sql types. See
// docs/adr/0003-dual-backend-orchestration-seam-principle.md for the bounded
// exception this seam is built under; docs/adr/0002 (internal/gtd's
// BeginTaskAdapter) is the first instance, this is the second.
//
// An adapter is a short-lived, single-use value constructed fresh per Accept
// call: it holds the open transaction (and, once opened, the proposal row)
// as state between method calls, so implementations are not safe for
// concurrent reuse across calls. The two production adapters are
// internal/proposal/accept_pg.go's pgAcceptAdapter and
// internal/storage/sqlite/accept_proposal.go's sqliteAcceptAdapter.
type AcceptAdapter interface {
	// ReadPending fetches the proposal row the adapter was constructed for,
	// scoped to the backend's configured workspace, translating the
	// backend's own not-found signal into this package's ErrNotFound.
	ReadPending(ctx context.Context) (*db.PendingProposal, error)

	// PrepareOutOfBand performs any work that must NOT run inside a database
	// transaction — currently only TypeKnowledge's embedding generation
	// (external network call) plus its cosine-similarity dedup check (see
	// ADR 0003's G1 "先算後寫": compute before write, so a transaction never
	// wraps an external network call). Called BEFORE BeginTx. The other 6
	// proposal types are no-ops — see BaseAcceptAdapter, embedded by both
	// production adapters so they don't each write an empty override. On
	// success, prepared is an opaque adapter-private value handed back to
	// Materialize unchanged (currently a knowledge.PreparedItem for
	// TypeKnowledge, nil for everything else).
	PrepareOutOfBand(ctx context.Context, prop *db.PendingProposal) (prepared any, err error)

	// BeginTx opens the transaction used for materialise + resolve.
	BeginTx(ctx context.Context) error

	// Materialize creates the concrete entity (goal/project/concept/decision/
	// task/knowledge/playbook) inside the open tx, dispatching on prop.Type.
	// prepared is whatever PrepareOutOfBand returned. created is the entity
	// to surface to the caller (nil for types with no representable "created"
	// value); a non-nil error aborts the whole Accept call — the deferred
	// Rollback fires and the proposal stays pending.
	Materialize(ctx context.Context, prop *db.PendingProposal, prepared any) (created any, err error)

	// ResolveAccepted flips the proposal (the one the adapter was
	// constructed for) to accepted inside the open tx.
	ResolveAccepted(ctx context.Context) (*db.PendingProposal, error)

	// Commit commits the open tx.
	Commit(ctx context.Context) error

	// Rollback rolls back the open tx. Safe to call after the tx has already
	// been closed by Commit (both pgx.Tx and database/sql.Tx guarantee a
	// redundant Rollback is a no-op), so AcceptOrchestration defers it
	// unconditionally rather than tracking its own committed flag.
	Rollback(ctx context.Context)
}

// BaseAcceptAdapter provides the no-op PrepareOutOfBand default shared by 6
// of the 7 proposal types, which have no out-of-band work to run before
// BeginTx. Embed this value in a concrete AcceptAdapter and override
// PrepareOutOfBand only for the type(s) that need it (currently only
// TypeKnowledge, on both production adapters) instead of every adapter
// implementation writing its own empty method.
type BaseAcceptAdapter struct{}

// PrepareOutOfBand is a no-op returning (nil, nil).
func (BaseAcceptAdapter) PrepareOutOfBand(context.Context, *db.PendingProposal) (any, error) {
	return nil, nil //nolint:nilnil // sentinel: nil prepared + nil error means "no out-of-band work for this type", not an error
}

// AcceptOrchestration runs the dialect-agnostic proposal-acceptance control
// flow against adapter: ReadPending → PrepareOutOfBand → BeginTx →
// Materialize → ResolveAccepted → Commit. id is used only for error-message
// context — the adapter itself already knows which proposal it was
// constructed for (mirrors gtd.BeginTaskOrchestration's id parameter).
//
// Unlike BeginTaskOrchestration, there is no idempotency short-circuit: a
// proposal not in StatusPending always returns ErrAlreadyResolved, never a
// silent success — see that sentinel's doc comment for why.
func AcceptOrchestration(ctx context.Context, id uuid.UUID, adapter AcceptAdapter) (*db.PendingProposal, any, error) {
	prop, err := adapter.ReadPending(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w", err) // ErrNotFound already wrapped by the adapter
	}
	if prop.Status != string(StatusPending) {
		return nil, nil, fmt.Errorf("accepting proposal %s: %w", id, ErrAlreadyResolved)
	}

	// G1 "先算後寫" (ADR 0003): all out-of-band work — currently only
	// TypeKnowledge's embedding call + dedup check — runs here, strictly
	// before BeginTx, so a slow or failing external call never holds a
	// database transaction open.
	prepared, err := adapter.PrepareOutOfBand(ctx, prop)
	if err != nil {
		return nil, nil, fmt.Errorf("accepting proposal %s: prepare: %w", id, err)
	}

	if err := adapter.BeginTx(ctx); err != nil {
		return nil, nil, fmt.Errorf("accepting proposal %s: begin tx: %w", id, err)
	}
	defer adapter.Rollback(ctx)

	created, err := adapter.Materialize(ctx, prop, prepared)
	if err != nil {
		return nil, nil, fmt.Errorf("accepting proposal %s: materialize: %w", id, err)
	}

	resolved, err := adapter.ResolveAccepted(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("accepting proposal %s: resolve: %w", id, err)
	}

	if err := adapter.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("accepting proposal %s: commit: %w", id, err)
	}
	return resolved, created, nil
}
