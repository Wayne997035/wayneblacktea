package gtd_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// fakeDeleteTaskAdapter is a scripted gtd.DeleteTaskAdapter used to exercise
// DeleteTaskOrchestration's control flow in isolation, without a real
// database. Each *Err field controls one adapter method's outcome; calls
// records every method invocation in order so tests can assert both the
// returned error and that later steps are skipped when an earlier one
// blocks or fails.
type fakeDeleteTaskAdapter struct {
	beginTxErr error

	exists      bool
	precheckErr error

	cleanupWorkSessionTasksErr     error
	nullifyWorkSessionsErr         error
	cleanupCompletionCandidatesErr error
	resetPromotedVisionItemsErr    error

	deleteRowErr error
	commitErr    error

	calls []string
}

func (f *fakeDeleteTaskAdapter) BeginTx(context.Context) error {
	f.calls = append(f.calls, "BeginTx")
	return f.beginTxErr
}

func (f *fakeDeleteTaskAdapter) WorkspacePrecheck(context.Context) (bool, error) {
	f.calls = append(f.calls, "WorkspacePrecheck")
	if f.precheckErr != nil {
		return false, f.precheckErr
	}
	return f.exists, nil
}

func (f *fakeDeleteTaskAdapter) CleanupWorkSessionTasks(context.Context) error {
	f.calls = append(f.calls, "CleanupWorkSessionTasks")
	return f.cleanupWorkSessionTasksErr
}

func (f *fakeDeleteTaskAdapter) NullifyWorkSessionsCurrentTask(context.Context) error {
	f.calls = append(f.calls, "NullifyWorkSessionsCurrentTask")
	return f.nullifyWorkSessionsErr
}

func (f *fakeDeleteTaskAdapter) CleanupCompletionCandidates(context.Context) error {
	f.calls = append(f.calls, "CleanupCompletionCandidates")
	return f.cleanupCompletionCandidatesErr
}

func (f *fakeDeleteTaskAdapter) ResetPromotedVisionItems(context.Context) error {
	f.calls = append(f.calls, "ResetPromotedVisionItems")
	return f.resetPromotedVisionItemsErr
}

func (f *fakeDeleteTaskAdapter) DeleteTaskRow(context.Context) error {
	f.calls = append(f.calls, "DeleteTaskRow")
	return f.deleteRowErr
}

func (f *fakeDeleteTaskAdapter) Commit(context.Context) error {
	f.calls = append(f.calls, "Commit")
	return f.commitErr
}

func (f *fakeDeleteTaskAdapter) Rollback(context.Context) {
	f.calls = append(f.calls, "Rollback")
}

// wantStepPrefix asserts err carries the uniform "delete task <id>: <step>: "
// prefix DeleteTaskOrchestration attaches at every step boundary.
func wantStepPrefix(t *testing.T, err error, id uuid.UUID, step string) {
	t.Helper()
	wantPrefix := fmt.Sprintf("delete task %s: %s: ", id, step)
	if !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Errorf("expected error to carry the uniform step prefix %q, got %q", wantPrefix, err.Error())
	}
}

func TestDeleteTaskOrchestration_NormalCommit(t *testing.T) {
	id := uuid.New()
	f := &fakeDeleteTaskAdapter{exists: true}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if err != nil {
		t.Fatalf("DeleteTaskOrchestration: %v", err)
	}
	wantCalls := []string{
		"BeginTx", "WorkspacePrecheck",
		"CleanupWorkSessionTasks", "NullifyWorkSessionsCurrentTask",
		"CleanupCompletionCandidates", "ResetPromotedVisionItems",
		"DeleteTaskRow", "Commit", "Rollback",
	}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("unexpected call sequence: %v", f.calls)
	}
}

func TestDeleteTaskOrchestration_WorkspaceMiss_NoOp(t *testing.T) {
	id := uuid.New()
	f := &fakeDeleteTaskAdapter{exists: false}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if err != nil {
		t.Fatalf("DeleteTaskOrchestration: %v", err)
	}
	wantCalls := []string{"BeginTx", "WorkspacePrecheck", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected the workspace-miss branch to skip all cleanups + commit, got %v", f.calls)
	}
}

func TestDeleteTaskOrchestration_BeginTxError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("conn refused")
	f := &fakeDeleteTaskAdapter{beginTxErr: wantErr}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the begin-tx error to propagate, got %v", err)
	}
	wantCalls := []string{"BeginTx"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected no Rollback when BeginTx itself fails (nothing was opened), got %v", f.calls)
	}
	wantStepPrefix(t, err, id, "begin tx")
}

func TestDeleteTaskOrchestration_WorkspacePrecheckError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("connection reset")
	f := &fakeDeleteTaskAdapter{precheckErr: wantErr}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the workspace pre-check error to propagate, got %v", err)
	}
	wantCalls := []string{"BeginTx", "WorkspacePrecheck", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("unexpected call sequence: %v", f.calls)
	}
	wantStepPrefix(t, err, id, "workspace pre-check")
}

func TestDeleteTaskOrchestration_CleanupWorkSessionTasksError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("disk full")
	f := &fakeDeleteTaskAdapter{exists: true, cleanupWorkSessionTasksErr: wantErr}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the cleanup error to propagate, got %v", err)
	}
	wantCalls := []string{"BeginTx", "WorkspacePrecheck", "CleanupWorkSessionTasks", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected later cleanups + delete + commit to be skipped, got %v", f.calls)
	}
	wantStepPrefix(t, err, id, "cleanup work_session_tasks")
}

func TestDeleteTaskOrchestration_NullifyWorkSessionsCurrentTaskError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("deadlock detected")
	f := &fakeDeleteTaskAdapter{exists: true, nullifyWorkSessionsErr: wantErr}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the nullify error to propagate, got %v", err)
	}
	wantCalls := []string{
		"BeginTx", "WorkspacePrecheck", "CleanupWorkSessionTasks",
		"NullifyWorkSessionsCurrentTask", "Rollback",
	}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected later cleanups + delete + commit to be skipped, got %v", f.calls)
	}
	wantStepPrefix(t, err, id, "nullify work_sessions.current_task_id")
}

func TestDeleteTaskOrchestration_CleanupCompletionCandidatesError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("connection reset")
	f := &fakeDeleteTaskAdapter{exists: true, cleanupCompletionCandidatesErr: wantErr}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the cleanup error to propagate, got %v", err)
	}
	wantCalls := []string{
		"BeginTx", "WorkspacePrecheck", "CleanupWorkSessionTasks",
		"NullifyWorkSessionsCurrentTask", "CleanupCompletionCandidates", "Rollback",
	}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected the remaining cleanup + delete + commit to be skipped, got %v", f.calls)
	}
	wantStepPrefix(t, err, id, "cleanup completion_candidates")
}

func TestDeleteTaskOrchestration_ResetPromotedVisionItemsError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("boom")
	f := &fakeDeleteTaskAdapter{exists: true, resetPromotedVisionItemsErr: wantErr}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the reset error to propagate, got %v", err)
	}
	wantCalls := []string{
		"BeginTx", "WorkspacePrecheck", "CleanupWorkSessionTasks",
		"NullifyWorkSessionsCurrentTask", "CleanupCompletionCandidates",
		"ResetPromotedVisionItems", "Rollback",
	}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected delete + commit to be skipped, got %v", f.calls)
	}
	wantStepPrefix(t, err, id, "reset vision_items.promoted_task_id")
}

func TestDeleteTaskOrchestration_DeleteTaskRowError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("unique violation")
	f := &fakeDeleteTaskAdapter{exists: true, deleteRowErr: wantErr}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the delete-row error to propagate, got %v", err)
	}
	wantCalls := []string{
		"BeginTx", "WorkspacePrecheck", "CleanupWorkSessionTasks",
		"NullifyWorkSessionsCurrentTask", "CleanupCompletionCandidates",
		"ResetPromotedVisionItems", "DeleteTaskRow", "Rollback",
	}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected Commit to be skipped after a delete-row failure, got %v", f.calls)
	}
	wantStepPrefix(t, err, id, "delete row")
}

func TestDeleteTaskOrchestration_CommitError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("connection reset")
	f := &fakeDeleteTaskAdapter{exists: true, commitErr: wantErr}

	err := gtd.DeleteTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the commit error to propagate, got %v", err)
	}
	wantCalls := []string{
		"BeginTx", "WorkspacePrecheck", "CleanupWorkSessionTasks",
		"NullifyWorkSessionsCurrentTask", "CleanupCompletionCandidates",
		"ResetPromotedVisionItems", "DeleteTaskRow", "Commit", "Rollback",
	}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("unexpected call sequence: %v", f.calls)
	}
	wantStepPrefix(t, err, id, "commit")
}
