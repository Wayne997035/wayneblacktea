package sqlite_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
)

// TestCompleteTask_OmittedArtifactPreservesExisting is U7's SQLite bad-case
// red test for Ω4 (2026-08-20-mcp-surface-spec.md), mirroring the PG test in
// internal/gtd. Before this fix, CompleteTask's SQL unconditionally
// overwrote the artifact column (`artifact = ?2` with no COALESCE) —
// completing a task WITH an artifact, reopening it, then re-completing it
// WITHOUT re-supplying artifact silently wiped the already-recorded
// PR/commit link. artifact is now presence-aware
// (COALESCE(?2, artifact), sqlite/gtd.go's CompleteTask).
func TestCompleteTask_OmittedArtifactPreservesExisting(t *testing.T) {
	s := openMem(t, "")
	ctx := context.Background()

	task, err := s.CreateTask(ctx, gtd.CreateTaskParams{Title: "omitted artifact preserved SQLite"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// seed artifact="old" via a first completion.
	oldArtifact := "old"
	if _, err := s.CompleteTask(ctx, task.ID, &oldArtifact); err != nil {
		t.Fatalf("CompleteTask (seed artifact): %v", err)
	}

	// reopen, matching the real reopen→re-complete flow (set_task_status).
	// pending (not in_progress) so this test doesn't also need an assignee.
	if _, err := s.UpdateTaskStatus(ctx, task.ID, gtd.TaskStatusPending); err != nil {
		t.Fatalf("UpdateTaskStatus (reopen): %v", err)
	}

	// bad case: re-complete WITHOUT supplying artifact.
	updated, err := s.CompleteTask(ctx, task.ID, nil)
	if err != nil {
		t.Fatalf("CompleteTask (omitted artifact): %v", err)
	}
	if !updated.Artifact.Valid || updated.Artifact.String != oldArtifact {
		t.Errorf("artifact = %+v, want preserved %q (not wiped to NULL)", updated.Artifact, oldArtifact)
	}

	// reload independently — defence-in-depth in case CompleteTask's own
	// returned row ever drifted from what's actually persisted.
	reloaded, err := s.GetTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTaskByID: %v", err)
	}
	if !reloaded.Artifact.Valid || reloaded.Artifact.String != oldArtifact {
		t.Errorf("reloaded artifact = %+v, want preserved %q", reloaded.Artifact, oldArtifact)
	}
}
