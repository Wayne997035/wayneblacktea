package gtd_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// fakeBeginTaskAdapter is a scripted gtd.BeginTaskAdapter used to exercise
// BeginTaskOrchestration's control flow in isolation, without a real
// database. Each *Err/return field controls one adapter method's outcome;
// calls records every method invocation in order so tests can assert both
// the returned value and that later steps are skipped when an earlier one
// blocks or fails.
type fakeBeginTaskAdapter struct {
	existing *db.Task
	readErr  error

	beginTxErr error

	guardBlocked bool
	guardErr     error

	resolvedTask *db.Task
	resolveErr   error

	logErr error

	commitTask *db.Task
	commitErr  error

	calls []string
}

func (f *fakeBeginTaskAdapter) ReadExisting(context.Context) (*db.Task, error) {
	f.calls = append(f.calls, "ReadExisting")
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.existing, nil
}

func (f *fakeBeginTaskAdapter) BeginTx(context.Context) error {
	f.calls = append(f.calls, "BeginTx")
	return f.beginTxErr
}

func (f *fakeBeginTaskAdapter) GuardedUpdate(context.Context) (bool, error) {
	f.calls = append(f.calls, "GuardedUpdate")
	if f.guardErr != nil {
		return false, f.guardErr
	}
	return f.guardBlocked, nil
}

func (f *fakeBeginTaskAdapter) ResolveGuardBlocked(context.Context) (*db.Task, error) {
	f.calls = append(f.calls, "ResolveGuardBlocked")
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return f.resolvedTask, nil
}

func (f *fakeBeginTaskAdapter) CreateActivityLog(context.Context) error {
	f.calls = append(f.calls, "CreateActivityLog")
	return f.logErr
}

func (f *fakeBeginTaskAdapter) Commit(context.Context) (*db.Task, error) {
	f.calls = append(f.calls, "Commit")
	if f.commitErr != nil {
		return nil, f.commitErr
	}
	return f.commitTask, nil
}

func (f *fakeBeginTaskAdapter) Rollback(context.Context) {
	f.calls = append(f.calls, "Rollback")
}

// fakeTask builds a minimal *db.Task for orchestration-level assertions.
func fakeTask(id uuid.UUID, status, assignee string) *db.Task {
	t := &db.Task{ID: id, Title: "fake task", Status: status}
	if assignee != "" {
		t.Assignee = pgtype.Text{String: assignee, Valid: true}
	}
	return t
}

func TestBeginTaskOrchestration_IdempotentReturn(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "in_progress", "claude")
	f := &fakeBeginTaskAdapter{existing: existing}

	got, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if err != nil {
		t.Fatalf("BeginTaskOrchestration: %v", err)
	}
	if got != existing {
		t.Errorf("expected the existing row returned as-is, got %+v", got)
	}
	wantCalls := []string{"ReadExisting"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected only ReadExisting on the idempotent shortcut (no tx opened), got %v", f.calls)
	}
}

func TestBeginTaskOrchestration_AssigneeGateBlocks(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "") // no assignee
	f := &fakeBeginTaskAdapter{existing: existing}

	_, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, gtd.ErrAssigneeRequiredForInProgress) {
		t.Fatalf("expected ErrAssigneeRequiredForInProgress, got %v", err)
	}
	wantCalls := []string{"ReadExisting"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected no tx to be opened when the assignee gate blocks, got %v", f.calls)
	}
}

func TestBeginTaskOrchestration_GuardBlocked_RacedToInProgress(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "claude")
	raced := fakeTask(id, "in_progress", "claude")
	f := &fakeBeginTaskAdapter{
		existing:     existing,
		guardBlocked: true,
		resolvedTask: raced,
	}

	got, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if err != nil {
		t.Fatalf("BeginTaskOrchestration: %v", err)
	}
	if got != raced {
		t.Errorf("expected the raced row from ResolveGuardBlocked, got %+v", got)
	}
	wantCalls := []string{"ReadExisting", "BeginTx", "GuardedUpdate", "ResolveGuardBlocked", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("unexpected call sequence: %v", f.calls)
	}
}

func TestBeginTaskOrchestration_GuardBlocked_NotFound(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "claude")
	f := &fakeBeginTaskAdapter{
		existing:     existing,
		guardBlocked: true,
		resolveErr:   gtd.ErrNotFound,
	}

	_, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, gtd.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	wantCalls := []string{"ReadExisting", "BeginTx", "GuardedUpdate", "ResolveGuardBlocked", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected the guard-blocked branch to never reach log/commit, got %v", f.calls)
	}
}

// TestBeginTaskOrchestration_GuardBlocked_ResolveNonNotFoundError verifies
// the other half of ResolveGuardBlocked's two error kinds: a non-ErrNotFound
// error (e.g. the adapter's own commit/re-read failure) gets the uniform
// "begin task <id>: commit: %w" step prefix, unlike the ErrNotFound case
// above which stays bare.
func TestBeginTaskOrchestration_GuardBlocked_ResolveNonNotFoundError(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "claude")
	wantErr := errors.New("connection reset mid-commit")
	f := &fakeBeginTaskAdapter{
		existing:     existing,
		guardBlocked: true,
		resolveErr:   wantErr,
	}

	_, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the resolve error to propagate via errors.Is, got %v", err)
	}
	if errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("non-ErrNotFound resolve error must not match errors.Is(gtd.ErrNotFound), got %v", err)
	}
	wantPrefix := fmt.Sprintf("begin task %s: commit: ", id)
	if !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Errorf("expected error to carry the uniform step prefix %q, got %q", wantPrefix, err.Error())
	}
}

func TestBeginTaskOrchestration_NormalCommit(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "claude")
	committed := fakeTask(id, "in_progress", "claude")
	f := &fakeBeginTaskAdapter{
		existing:   existing,
		commitTask: committed,
	}

	got, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if err != nil {
		t.Fatalf("BeginTaskOrchestration: %v", err)
	}
	if got != committed {
		t.Errorf("expected the committed row from Commit, got %+v", got)
	}
	wantCalls := []string{"ReadExisting", "BeginTx", "GuardedUpdate", "CreateActivityLog", "Commit", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("unexpected call sequence: %v", f.calls)
	}
}

func TestBeginTaskOrchestration_ReadExistingError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("boom")
	f := &fakeBeginTaskAdapter{readErr: wantErr}

	_, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the read error to propagate, got %v", err)
	}
	if !reflect.DeepEqual(f.calls, []string{"ReadExisting"}) {
		t.Errorf("expected no further calls after a read failure, got %v", f.calls)
	}
}

func TestBeginTaskOrchestration_BeginTxError(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "claude")
	wantErr := errors.New("conn refused")
	f := &fakeBeginTaskAdapter{existing: existing, beginTxErr: wantErr}

	_, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the begin-tx error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadExisting", "BeginTx"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected no Rollback when BeginTx itself fails (nothing was opened), got %v", f.calls)
	}
}

func TestBeginTaskOrchestration_GuardedUpdateError(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "claude")
	wantErr := errors.New("deadlock detected")
	f := &fakeBeginTaskAdapter{existing: existing, guardErr: wantErr}

	_, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the guarded-update error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadExisting", "BeginTx", "GuardedUpdate", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("unexpected call sequence: %v", f.calls)
	}
}

func TestBeginTaskOrchestration_CreateActivityLogError(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "claude")
	wantErr := errors.New("disk full")
	f := &fakeBeginTaskAdapter{existing: existing, logErr: wantErr}

	_, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the activity-log error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadExisting", "BeginTx", "GuardedUpdate", "CreateActivityLog", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected Commit to be skipped after a log failure, got %v", f.calls)
	}
}

func TestBeginTaskOrchestration_CommitError(t *testing.T) {
	id := uuid.New()
	existing := fakeTask(id, "pending", "claude")
	wantErr := errors.New("connection reset")
	f := &fakeBeginTaskAdapter{existing: existing, commitErr: wantErr}

	_, err := gtd.BeginTaskOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the commit error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadExisting", "BeginTx", "GuardedUpdate", "CreateActivityLog", "Commit", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("unexpected call sequence: %v", f.calls)
	}
}
