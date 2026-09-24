package gtd

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// DeleteProjectAdapter is the dialect-agnostic seam for deleting a project
// together with every task under it (ADR 0003: orchestration-only, two real
// production adapters, passes the deletion test).
//
// The method list is deliberately long and flat: it IS the checklist of every
// table that references a project or one of its tasks. Red line #9 forbids
// foreign keys, so nothing in the database will notice a table this interface
// forgets — which is not hypothetical. DeleteTaskAdapter shipped without
// decisions.task_id and knowledge_items.task_id and quietly left dangling
// references on every delete until someone read the schema next to the code.
//
// The reference list comes from the FK constraint names migration 000026
// dropped (`<table>_project_id_fkey`), plus knowledge_items.project_id which
// arrived later in 000049: activity_log, decisions, knowledge_items,
// session_handoffs, tasks, work_sessions. vision_items.project_id (000029)
// and procedural_memories.project_id (000032) were missed by this same
// interface until a full-repo security review (DBI-FULL0923-01) caught them
// and F191-01 fixed them — hand-written lists drift, which is exactly why
// F191-02's machine-derived-from-schema test now exists, so the NEXT missed
// column is caught mechanically instead of by the next review round. Adding
// a new project_id column means adding a method here AND registering it (or
// its documented exemption) in gtd.ProjectIDCleanupExemptions.
//
// Every cleanup is set-based over the whole project rather than a loop over
// tasks: one statement per table instead of one per task, inside a single
// transaction, so a project with hundreds of tasks is still one atomic unit.
type DeleteProjectAdapter interface {
	// BeginTx opens the transaction used for the pre-check, every cleanup
	// and both row deletes.
	BeginTx(ctx context.Context) error

	// WorkspacePrecheck reports whether the project exists inside the
	// adapter's configured workspace. exists=false stops the orchestration
	// with a silent no-op, matching DeleteTaskOrchestration's contract.
	WorkspacePrecheck(ctx context.Context) (exists bool, err error)

	// CountTasks reports how many tasks sit under the project, read inside
	// the same transaction so the number reported back to the caller is the
	// number actually deleted.
	CountTasks(ctx context.Context) (int, error)

	// --- references to the project's tasks ---

	CleanupWorkSessionTasks(ctx context.Context) error
	NullifyWorkSessionsCurrentTask(ctx context.Context) error
	CleanupCompletionCandidates(ctx context.Context) error
	ResetPromotedVisionItems(ctx context.Context) error
	NullifyDecisionTaskRefs(ctx context.Context) error
	NullifyKnowledgeItemTaskRefs(ctx context.Context) error

	// --- references to the project itself ---

	NullifyActivityLogProjectRefs(ctx context.Context) error
	NullifyDecisionProjectRefs(ctx context.Context) error
	NullifyKnowledgeItemProjectRefs(ctx context.Context) error
	NullifySessionHandoffProjectRefs(ctx context.Context) error
	NullifyWorkSessionProjectRefs(ctx context.Context) error
	// NullifyVisionItemProjectRefs NULLs vision_items.project_id (migration
	// 000029). [F191-01] — missed by this interface from the start; caught
	// by a full-repo security review (DBI-FULL0923-01), not by re-reading
	// the method list above. F191-02's machine-derived table list exists so
	// the next such gap is caught mechanically instead.
	NullifyVisionItemProjectRefs(ctx context.Context) error
	// NullifyProceduralMemoryProjectRefs NULLs procedural_memories.project_id
	// (migration 000032). [F191-01] Same gap, same discovery path.
	NullifyProceduralMemoryProjectRefs(ctx context.Context) error

	// --- the rows themselves ---

	// DeleteTaskRows deletes every task under the project. MUST run after
	// all task-reference cleanups: once the rows are gone their ids cannot
	// be selected any more, so a cleanup that runs afterwards silently
	// matches nothing.
	DeleteTaskRows(ctx context.Context) error

	// DeleteProjectRow deletes the project row itself.
	DeleteProjectRow(ctx context.Context) error

	Commit(ctx context.Context) error

	// Rollback rolls back the open tx. Safe after Commit (both pgx.Tx and
	// database/sql.Tx treat a redundant Rollback as a no-op), so the
	// orchestration defers it unconditionally.
	Rollback(ctx context.Context)

	// --- soft-delete contract stage (PR #191 fan-out, design 1-3): declared
	// now so StoreIface's final shape is fixed, but NEITHER method is called
	// by DeleteProjectOrchestration below yet. Wiring them in (snapshot
	// first, right after CountTasks, before any cleanup — design 1) is
	// F191-04/F191-06's job. Every production implementation MUST return
	// ErrNotImplemented (or sqlite.ErrNotImplemented) — NEVER nil — so a
	// caller cannot mistake "stubbed" for "nothing to snapshot". ---

	// SnapshotProjectAndTasks copies the project row and every task row
	// under it into deletion_tombstones (design 1/2), tagged with the given
	// deletionID/deletedAt/deletedBy — all three generated ONCE by the
	// orchestration layer (design 1) so every row in the group shares them.
	SnapshotProjectAndTasks(ctx context.Context, deletionID uuid.UUID, deletedAt time.Time, deletedBy string) error

	// WriteDeletionAuditLog writes one activity_log row (action
	// "project_deleted") inside the same tx as the delete (design 3, P2(a)).
	// notes MUST carry only the deletion id and task count — never a name or
	// other stored text (design 3's redaction rule).
	WriteDeletionAuditLog(ctx context.Context, deletionID uuid.UUID, deletedBy string, taskCount int) error
}

// DeleteProjectOrchestration deletes a project and all of its tasks, running
// every reference cleanup first. Returns the number of tasks deleted.
//
// A missing or workspace-mismatched project is a no-op returning 0, not an
// error — same shape as DeleteTaskOrchestration, so a caller that deletes the
// same project twice gets a quiet second answer rather than a failure.
func DeleteProjectOrchestration(ctx context.Context, id uuid.UUID, adapter DeleteProjectAdapter) (int, error) {
	if err := adapter.BeginTx(ctx); err != nil {
		return 0, fmt.Errorf("delete project %s: begin tx: %w", id, err)
	}
	defer adapter.Rollback(ctx)

	exists, err := adapter.WorkspacePrecheck(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete project %s: workspace precheck: %w", id, err)
	}
	if !exists {
		return 0, nil
	}

	taskCount, err := adapter.CountTasks(ctx)
	if err != nil {
		return 0, fmt.Errorf("delete project %s: count tasks: %w", id, err)
	}

	// Order matters twice over: every reference cleanup before the rows they
	// point at, and the task cleanups before DeleteTaskRows specifically.
	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"cleanup work_session_tasks", adapter.CleanupWorkSessionTasks},
		{"nullify work_sessions.current_task_id", adapter.NullifyWorkSessionsCurrentTask},
		{"cleanup completion_candidates", adapter.CleanupCompletionCandidates},
		{"reset vision_items.promoted_task_id", adapter.ResetPromotedVisionItems},
		{"nullify decisions.task_id", adapter.NullifyDecisionTaskRefs},
		{"nullify knowledge_items.task_id", adapter.NullifyKnowledgeItemTaskRefs},
		{"nullify activity_log.project_id", adapter.NullifyActivityLogProjectRefs},
		{"nullify decisions.project_id", adapter.NullifyDecisionProjectRefs},
		{"nullify knowledge_items.project_id", adapter.NullifyKnowledgeItemProjectRefs},
		{"nullify session_handoffs.project_id", adapter.NullifySessionHandoffProjectRefs},
		{"nullify work_sessions.project_id", adapter.NullifyWorkSessionProjectRefs},
		{"nullify vision_items.project_id", adapter.NullifyVisionItemProjectRefs},
		{"nullify procedural_memories.project_id", adapter.NullifyProceduralMemoryProjectRefs},
		{"delete task rows", adapter.DeleteTaskRows},
		{"delete project row", adapter.DeleteProjectRow},
	}
	for _, s := range steps {
		if err := s.fn(ctx); err != nil {
			return 0, fmt.Errorf("delete project %s: %s: %w", id, s.name, err)
		}
	}

	if err := adapter.Commit(ctx); err != nil {
		return 0, fmt.Errorf("delete project %s: commit: %w", id, err)
	}
	return taskCount, nil
}
