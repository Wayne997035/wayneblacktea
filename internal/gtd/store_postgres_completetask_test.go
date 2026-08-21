package gtd_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// TestCompleteTask_OmittedArtifactPreservesExisting is U7's PG bad-case red
// test for Ω4 (2026-08-20-mcp-surface-spec.md): before this fix,
// CompleteTask's SQL unconditionally overwrote the artifact column
// (`artifact = $1` with no COALESCE) — completing a task WITH an artifact,
// reopening it, then re-completing it WITHOUT re-supplying artifact silently
// wiped the already-recorded PR/commit link. artifact is now presence-aware
// (COALESCE(sqlc.narg('artifact'), artifact), sql/queries/gtd.sql).
func TestCompleteTask_OmittedArtifactPreservesExisting(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)

	task, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "omitted artifact preserved PG"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// seed artifact="old" via a first completion.
	oldArtifact := "old"
	if _, err := store.CompleteTask(ctx, task.ID, &oldArtifact); err != nil {
		t.Fatalf("CompleteTask (seed artifact): %v", err)
	}

	// reopen, matching the real reopen→re-complete flow (set_task_status).
	// pending (not in_progress) so this test doesn't also need an assignee —
	// RequireAssigneeForInProgress only gates the in_progress transition.
	if _, err := store.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusPending); err != nil {
		t.Fatalf("UpdateTaskStatus (reopen): %v", err)
	}

	// bad case: re-complete WITHOUT supplying artifact.
	updated, err := store.CompleteTask(ctx, task.ID, nil)
	if err != nil {
		t.Fatalf("CompleteTask (omitted artifact): %v", err)
	}
	if !updated.Artifact.Valid || updated.Artifact.String != oldArtifact {
		t.Errorf("artifact = %+v, want preserved %q (not wiped to NULL)", updated.Artifact, oldArtifact)
	}

	// reload independently — defence-in-depth in case CompleteTask's own
	// RETURNING clause ever drifted from what's actually persisted.
	reloaded, err := store.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if !reloaded.Artifact.Valid || reloaded.Artifact.String != oldArtifact {
		t.Errorf("reloaded artifact = %+v, want preserved %q", reloaded.Artifact, oldArtifact)
	}
}
