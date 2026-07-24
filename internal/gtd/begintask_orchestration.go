package gtd

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// BeginTaskAdapter is the narrow, per-backend seam BeginTaskOrchestration
// drives to execute BeginTask's control flow — idempotency check → assignee
// gate → begin tx → guarded write → activity log → commit — without seeing
// pgx or database/sql types. See docs/adr/0002-gtd-dual-backend-transaction-orchestration.md
// for why this exception to "no Service/Repository split" exists.
//
// An adapter is a short-lived, single-use value constructed fresh per
// BeginTask call: it holds the open transaction as state between method
// calls, so implementations are not safe for concurrent reuse across calls.
// The two production adapters are internal/gtd/store.go's pgBeginTaskAdapter
// and internal/storage/sqlite/gtd.go's sqliteBeginTaskAdapter.
type BeginTaskAdapter interface {
	// ReadExisting fetches the task's current row, scoped to the backend's
	// configured workspace, translating the backend's own not-found signal
	// into this package's ErrNotFound.
	ReadExisting(ctx context.Context) (*db.Task, error)

	// BeginTx opens the transaction used for the guarded write + activity log.
	BeginTx(ctx context.Context) error

	// GuardedUpdate flips status to in_progress inside the open tx, guarded by
	// `AND status != 'in_progress'` to close the TOCTOU race window opened
	// between ReadExisting and this write. guardBlocked=true means the guard
	// matched zero rows — translated from the backend's own native signal
	// (pgx.ErrNoRows from a sqlc conditional UPDATE, or RowsAffected() == 0
	// from a raw SQL UPDATE); this interface never sees either type directly.
	GuardedUpdate(ctx context.Context) (guardBlocked bool, err error)

	// ResolveGuardBlocked is called only when GuardedUpdate reports
	// guardBlocked=true. It closes out the open tx and re-reads to distinguish
	// "row vanished" (returns ErrNotFound) from "a concurrent caller already
	// flipped it" (returns the row, idempotent). Each adapter keeps its own
	// pre-existing tx-cleanup choice and internal ordering here: the two
	// production backends genuinely differ (see their doc comments) and
	// unifying the ordering would be a correctness change, not a cleanup.
	ResolveGuardBlocked(ctx context.Context) (*db.Task, error)

	// CreateActivityLog records the work_session_started entry inside the
	// still-open tx. Only called after a successful (non-guard-blocked) write.
	// The title logged is adapter-owned, not orchestration-supplied: the
	// Postgres adapter uses the tx-fresh row from GuardedUpdate (RETURNING),
	// the SQLite adapter uses the row it stashed in ReadExisting. This keeps
	// each backend's original title-freshness behavior — in particular, it
	// prevents Postgres from logging a stale pre-tx title if a concurrent
	// caller edits the task's title between ReadExisting and the guarded
	// write below.
	CreateActivityLog(ctx context.Context) error

	// Commit commits the open tx and returns the task's final state.
	Commit(ctx context.Context) (*db.Task, error)

	// Rollback rolls back the open tx. Safe to call after the tx has already
	// been closed by Commit or ResolveGuardBlocked (both pgx.Tx and
	// database/sql.Tx already guarantee a redundant Rollback is a no-op), so
	// the orchestration below defers it unconditionally rather than tracking
	// its own committed/resolved flag.
	Rollback(ctx context.Context)
}

// BeginTaskOrchestration runs the dialect-agnostic BeginTask control flow
// against adapter, and is called by both Store.BeginTask (Postgres) and
// GTDStore.BeginTask (SQLite). id is used only for error-message context —
// the adapter itself already knows which task it was constructed for.
func BeginTaskOrchestration(ctx context.Context, id uuid.UUID, adapter BeginTaskAdapter) (*db.Task, error) {
	// Idempotency: read current status before opening a transaction.
	existing, err := adapter.ReadExisting(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w", err) // ErrNotFound already wrapped by the adapter
	}
	if existing.Status == string(TaskStatusInProgress) {
		return existing, nil
	}

	// Domain-layer gate (P6.7): BeginTask takes no assignee argument, so the
	// only source of truth is the existing row.
	existingAssignee := ""
	if existing.Assignee.Valid {
		existingAssignee = existing.Assignee.String
	}
	if err := RequireAssigneeForInProgress(existingAssignee, TaskStatusInProgress); err != nil {
		return nil, fmt.Errorf("beginning task %s: %w", id, err)
	}

	if err := adapter.BeginTx(ctx); err != nil {
		return nil, fmt.Errorf("begin task %s: begin tx: %w", id, err)
	}
	defer adapter.Rollback(ctx)

	guardBlocked, err := adapter.GuardedUpdate(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin task %s: update status: %w", id, err)
	}
	if guardBlocked {
		task, err := adapter.ResolveGuardBlocked(ctx)
		if err != nil {
			// ResolveGuardBlocked returns two error kinds: ErrNotFound (the
			// row vanished — must stay errors.Is-detectable, so it is kept
			// bare) or the adapter's own commit/re-read failure (gets the
			// uniform step prefix like every other step below).
			if errors.Is(err, ErrNotFound) {
				return nil, fmt.Errorf("%w", err)
			}
			return nil, fmt.Errorf("begin task %s: resolve guard-blocked: %w", id, err)
		}
		return task, nil
	}

	if err := adapter.CreateActivityLog(ctx); err != nil {
		return nil, fmt.Errorf("begin task %s: log activity: %w", id, err)
	}

	task, err := adapter.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin task %s: commit: %w", id, err)
	}
	return task, nil
}
