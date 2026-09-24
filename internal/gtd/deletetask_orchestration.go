package gtd

import (
	"context"
	"fmt"
	"time"

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

	// NullifyDecisionTaskRefs NULLs decisions.task_id on rows pointing at the
	// task (migration 000048). The decision itself is kept: it records a
	// choice that stays true after the task it was attached to is gone.
	NullifyDecisionTaskRefs(ctx context.Context) error

	// NullifyKnowledgeItemTaskRefs NULLs knowledge_items.task_id on rows
	// pointing at the task (migration 000049). Same reasoning as decisions —
	// the knowledge outlives the task that produced it.
	//
	// These two joined the interface later than the four above, and the gap
	// is the whole reason they exist: both columns are indexed and neither
	// was ever cleaned, so every delete_task left up to one dangling
	// reference per row. Red line #9 forbids foreign keys, which means the
	// database cannot notice — referential integrity here is exactly and
	// only what this interface remembers to do.
	NullifyKnowledgeItemTaskRefs(ctx context.Context) error

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

	// --- soft-delete snapshot + audit (design 1-3): SnapshotTask is called
	// first, right after the workspace pre-check and before any cleanup
	// step below — see DeleteProjectAdapter's twin methods for the full
	// rationale. WriteDeletionAuditLog is called last, immediately before
	// Commit, so a failed audit write rolls back the whole delete rather
	// than leaving one with no record of who did it. See
	// DeleteTaskOrchestration below for the call sites. ---

	// SnapshotTask copies the task row into deletion_tombstones (design
	// 1/2), tagged with the given deletionID/deletedAt/deletedBy — all three
	// generated ONCE by the orchestration layer (design 1).
	SnapshotTask(ctx context.Context, deletionID uuid.UUID, deletedAt time.Time, deletedBy string) error

	// WriteDeletionAuditLog writes one activity_log row (action
	// "task_deleted") inside the same tx as the delete (design 3, P2(a)).
	// notes MUST carry only the deletion id — never a title or other stored
	// text (design 3's redaction rule).
	WriteDeletionAuditLog(ctx context.Context, deletionID uuid.UUID, deletedBy string) error
}

// DeleteTaskOrchestration runs the dialect-agnostic DeleteTask control flow
// against adapter, and is called by both Store.DeleteTask (Postgres) and
// GTDStore.DeleteTask (SQLite). id is used only for error-message context —
// the adapter itself already knows which task it was constructed for.
//
// deletedBy/deletedAt have the same contract as DeleteProjectOrchestration's:
// a single deletedAt value and a deletionID generated once here, shared by
// this task's tombstone row and its audit row.
func DeleteTaskOrchestration(ctx context.Context, id uuid.UUID, deletedBy string, deletedAt time.Time, adapter DeleteTaskAdapter) error {
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

	// Snapshot BEFORE any cleanup runs (design 1/2, same placement rule as
	// DeleteProjectOrchestration): the row this copies must be exactly what
	// existed before this call touched anything.
	deletionID := uuid.New()
	if err := adapter.SnapshotTask(ctx, deletionID, deletedAt, deletedBy); err != nil {
		return fmt.Errorf("delete task %s: snapshot task: %w", id, err)
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
	if err := adapter.NullifyDecisionTaskRefs(ctx); err != nil {
		return fmt.Errorf("delete task %s: nullify decisions.task_id: %w", id, err)
	}
	if err := adapter.NullifyKnowledgeItemTaskRefs(ctx); err != nil {
		return fmt.Errorf("delete task %s: nullify knowledge_items.task_id: %w", id, err)
	}

	if err := adapter.DeleteTaskRow(ctx); err != nil {
		return fmt.Errorf("delete task %s: delete row: %w", id, err)
	}

	// Audit last, immediately before Commit (design 3, P2(a)).
	if err := adapter.WriteDeletionAuditLog(ctx, deletionID, deletedBy); err != nil {
		return fmt.Errorf("delete task %s: write deletion audit log: %w", id, err)
	}

	if err := adapter.Commit(ctx); err != nil {
		return fmt.Errorf("delete task %s: commit: %w", id, err)
	}
	return nil
}
