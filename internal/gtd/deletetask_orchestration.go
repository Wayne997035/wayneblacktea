package gtd

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DeleteTaskAdapter is the narrow, per-backend seam DeleteTaskOrchestration
// drives to execute DeleteTask's control flow — begin tx → workspace
// pre-check → four cascade cleanups → delete the task row → commit — without
// seeing pgx or database/sql types. See docs/adr/0002-gtd-dual-backend-transaction-orchestration.md
// for why this exception to "no Service/Repository split" exists.
//
// An adapter is a short-lived, single-use value constructed fresh per
// DeleteTask call: it holds the open transaction as state between method
// calls, so implementations are not safe for concurrent reuse across calls.
// The two production adapters are internal/gtd/store.go's pgDeleteTaskAdapter
// and internal/storage/sqlite/gtd.go's sqliteDeleteTaskAdapter.
type DeleteTaskAdapter interface {
	// BeginTx opens the transaction used for the workspace pre-check, the
	// four cascade cleanups, and the final row delete.
	BeginTx(ctx context.Context) error

	// WorkspacePrecheck reports whether the task exists inside the adapter's
	// configured workspace, scoped to the still-open tx. exists=false means
	// the orchestration stops here and returns a silent no-op — matching the
	// pre-fix behaviour where a missing / workspace-mismatched task simply
	// affected 0 rows on the parent DELETE — leaving the deferred Rollback
	// to close out the empty tx.
	WorkspacePrecheck(ctx context.Context) (exists bool, err error)

	// CleanupWorkSessionTasks removes work_session_tasks rows referencing
	// the task (was ON DELETE CASCADE on work_session_tasks.task_id).
	CleanupWorkSessionTasks(ctx context.Context) error

	// NullifyWorkSessionsCurrentTask NULLs out work_sessions.current_task_id
	// rows pointing at the task (was ON DELETE SET NULL).
	NullifyWorkSessionsCurrentTask(ctx context.Context) error

	// CleanupCompletionCandidates removes completion_candidates rows
	// referencing the task.
	CleanupCompletionCandidates(ctx context.Context) error

	// ResetPromotedVisionItems resets vision_items rows that were promoted
	// from the task back to an un-promoted, open state.
	ResetPromotedVisionItems(ctx context.Context) error

	// DeleteTaskRow deletes the task row itself, scoped to the adapter's
	// configured workspace.
	DeleteTaskRow(ctx context.Context) error

	// Commit commits the open tx.
	Commit(ctx context.Context) error

	// Rollback rolls back the open tx. Safe to call after the tx has
	// already been closed by Commit (both pgx.Tx and database/sql.Tx
	// already guarantee a redundant Rollback is a no-op), so the
	// orchestration below defers it unconditionally rather than tracking
	// its own committed flag. This also means the workspace-miss no-op path
	// relies entirely on this deferred call to close out the tx — any error
	// from that rollback is swallowed, mirroring BeginTaskOrchestration's
	// unconditional-defer shape.
	Rollback(ctx context.Context)
}

// DeleteTaskOrchestration runs the dialect-agnostic DeleteTask control flow
// against adapter, and is called by both Store.DeleteTask (Postgres) and
// GTDStore.DeleteTask (SQLite). id is used only for error-message context —
// the adapter itself already knows which task it was constructed for.
func DeleteTaskOrchestration(ctx context.Context, id uuid.UUID, adapter DeleteTaskAdapter) error {
	if err := adapter.BeginTx(ctx); err != nil {
		return fmt.Errorf("delete task %s: begin tx: %w", id, err)
	}
	defer adapter.Rollback(ctx)

	// Workspace authorisation pre-check. The cleanup statements below are
	// keyed only by task_id, so without this guard a cross-workspace caller
	// could delete another workspace's join rows / NULL its current_task_id
	// pointer (the parent DELETE's workspace filter would 0-row but the
	// damage to neighbouring tables would already be done).
	exists, err := adapter.WorkspacePrecheck(ctx)
	if err != nil {
		return fmt.Errorf("delete task %s: workspace pre-check: %w", id, err)
	}
	if !exists {
		// The deferred Rollback closes out the empty tx; a missing /
		// workspace-mismatched task is a silent no-op, matching the pre-fix
		// behaviour where such a DELETE simply affected 0 rows.
		return nil
	}

	if err := adapter.CleanupWorkSessionTasks(ctx); err != nil {
		return fmt.Errorf("delete task %s: cleanup work_session_tasks: %w", id, err)
	}
	if err := adapter.NullifyWorkSessionsCurrentTask(ctx); err != nil {
		return fmt.Errorf("delete task %s: nullify work_sessions.current_task_id: %w", id, err)
	}
	if err := adapter.CleanupCompletionCandidates(ctx); err != nil {
		return fmt.Errorf("delete task %s: cleanup completion_candidates: %w", id, err)
	}
	if err := adapter.ResetPromotedVisionItems(ctx); err != nil {
		return fmt.Errorf("delete task %s: reset vision_items.promoted_task_id: %w", id, err)
	}

	if err := adapter.DeleteTaskRow(ctx); err != nil {
		return fmt.Errorf("delete task %s: delete row: %w", id, err)
	}

	if err := adapter.Commit(ctx); err != nil {
		return fmt.Errorf("delete task %s: commit: %w", id, err)
	}
	return nil
}
