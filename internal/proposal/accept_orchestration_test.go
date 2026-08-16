package proposal_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// fakeAcceptAdapter is a scripted proposal.AcceptAdapter used to exercise
// AcceptOrchestration's control flow in isolation, without a real database.
// Each *Err/return field controls one adapter method's outcome; calls
// records every method invocation in order so tests can assert both the
// returned value and that later steps are skipped when an earlier one
// blocks or fails.
type fakeAcceptAdapter struct {
	pending *db.PendingProposal
	readErr error

	prepared   any
	prepareErr error

	beginTxErr error

	created        any
	materializeErr error

	// materializeGotPrepared records the "prepared" argument (PrepareOutOfBand's
	// return value) that AcceptOrchestration actually passed to Materialize, so
	// tests can assert the data-flow guarantee, not just the call-order
	// guarantee captured by calls below. GTD-1f42debd: previously this argument
	// was silently discarded by Materialize — a regression that dropped,
	// reordered, or nil'd it before this fix would have left every test in this
	// file green.
	materializeGotPrepared any

	resolved   *db.PendingProposal
	resolveErr error

	commitErr error

	calls []string
}

func (f *fakeAcceptAdapter) ReadPending(context.Context) (*db.PendingProposal, error) {
	f.calls = append(f.calls, "ReadPending")
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.pending, nil
}

func (f *fakeAcceptAdapter) PrepareOutOfBand(context.Context, *db.PendingProposal) (any, error) {
	f.calls = append(f.calls, "PrepareOutOfBand")
	if f.prepareErr != nil {
		return nil, f.prepareErr
	}
	return f.prepared, nil
}

func (f *fakeAcceptAdapter) BeginTx(context.Context) error {
	f.calls = append(f.calls, "BeginTx")
	return f.beginTxErr
}

func (f *fakeAcceptAdapter) Materialize(_ context.Context, _ *db.PendingProposal, prepared any) (any, error) {
	f.calls = append(f.calls, "Materialize")
	f.materializeGotPrepared = prepared
	if f.materializeErr != nil {
		return nil, f.materializeErr
	}
	return f.created, nil
}

func (f *fakeAcceptAdapter) ResolveAccepted(context.Context) (*db.PendingProposal, error) {
	f.calls = append(f.calls, "ResolveAccepted")
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return f.resolved, nil
}

func (f *fakeAcceptAdapter) Commit(context.Context) error {
	f.calls = append(f.calls, "Commit")
	return f.commitErr
}

func (f *fakeAcceptAdapter) Rollback(context.Context) {
	f.calls = append(f.calls, "Rollback")
}

// fakePending builds a minimal *db.PendingProposal for orchestration-level
// assertions.
func fakePending(id uuid.UUID, status string) *db.PendingProposal {
	return &db.PendingProposal{ID: id, Type: "task", Status: status}
}

func TestAcceptOrchestration_AlreadyResolved(t *testing.T) {
	id := uuid.New()
	pending := fakePending(id, "accepted")
	f := &fakeAcceptAdapter{pending: pending}

	_, _, err := proposal.AcceptOrchestration(context.Background(), id, f)
	if !errors.Is(err, proposal.ErrAlreadyResolved) {
		t.Fatalf("expected ErrAlreadyResolved, got %v", err)
	}
	wantCalls := []string{"ReadPending"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected no further calls once status check fails, got %v", f.calls)
	}
}

func TestAcceptOrchestration_ReadPendingError(t *testing.T) {
	id := uuid.New()
	wantErr := errors.New("boom")
	f := &fakeAcceptAdapter{readErr: wantErr}

	_, _, err := proposal.AcceptOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the read error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadPending"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected no BeginTx after a read failure, got %v", f.calls)
	}
}

// TestAcceptOrchestration_PrepareOutOfBandError verifies ADR 0003's G1
// "先算後寫" structural guarantee: when the out-of-band step (e.g.
// TypeKnowledge's embedding call) fails, BeginTx must never be reached — a
// failing external I/O call must never leave a transaction open.
func TestAcceptOrchestration_PrepareOutOfBandError(t *testing.T) {
	id := uuid.New()
	pending := fakePending(id, "pending")
	wantErr := errors.New("embedding service unreachable")
	f := &fakeAcceptAdapter{pending: pending, prepareErr: wantErr}

	_, _, err := proposal.AcceptOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the prepare error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadPending", "PrepareOutOfBand"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected no BeginTx (and thus no open tx) when PrepareOutOfBand fails, got %v", f.calls)
	}
}

func TestAcceptOrchestration_BeginTxError(t *testing.T) {
	id := uuid.New()
	pending := fakePending(id, "pending")
	wantErr := errors.New("conn refused")
	f := &fakeAcceptAdapter{pending: pending, beginTxErr: wantErr}

	_, _, err := proposal.AcceptOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the begin-tx error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadPending", "PrepareOutOfBand", "BeginTx"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected no Rollback when BeginTx itself fails (nothing was opened), got %v", f.calls)
	}
}

func TestAcceptOrchestration_MaterializeError(t *testing.T) {
	id := uuid.New()
	pending := fakePending(id, "pending")
	wantErr := errors.New("constraint violation")
	f := &fakeAcceptAdapter{pending: pending, materializeErr: wantErr}

	_, _, err := proposal.AcceptOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the materialize error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadPending", "PrepareOutOfBand", "BeginTx", "Materialize", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected Rollback to fire after a materialize failure, got %v", f.calls)
	}
}

func TestAcceptOrchestration_ResolveAcceptedError(t *testing.T) {
	id := uuid.New()
	pending := fakePending(id, "pending")
	wantErr := errors.New("row vanished mid-tx")
	f := &fakeAcceptAdapter{pending: pending, resolveErr: wantErr}

	_, _, err := proposal.AcceptOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the resolve error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadPending", "PrepareOutOfBand", "BeginTx", "Materialize", "ResolveAccepted", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected Rollback to fire after a resolve failure, got %v", f.calls)
	}
}

func TestAcceptOrchestration_CommitError(t *testing.T) {
	id := uuid.New()
	pending := fakePending(id, "pending")
	wantErr := errors.New("connection reset")
	f := &fakeAcceptAdapter{pending: pending, commitErr: wantErr}

	_, _, err := proposal.AcceptOrchestration(context.Background(), id, f)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected the commit error to propagate, got %v", err)
	}
	wantCalls := []string{"ReadPending", "PrepareOutOfBand", "BeginTx", "Materialize", "ResolveAccepted", "Commit", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("expected Rollback to fire even after a commit failure (defer is unconditional), got %v", f.calls)
	}
}

// TestAcceptOrchestration_HappyPath also verifies that the deferred Rollback
// still fires after a successful Commit (AcceptOrchestration has no
// committed flag — Rollback is always deferred unconditionally, relying on
// both pgx.Tx and database/sql.Tx guaranteeing a post-Commit Rollback is a
// harmless no-op rather than a double-close error).
func TestAcceptOrchestration_HappyPath(t *testing.T) {
	id := uuid.New()
	pending := fakePending(id, "pending")
	resolved := fakePending(id, "accepted")
	createdEntity := &db.Task{ID: uuid.New(), Title: "materialised task"}
	f := &fakeAcceptAdapter{
		pending:  pending,
		prepared: "opaque-prepared-value",
		created:  createdEntity,
		resolved: resolved,
	}

	gotResolved, gotCreated, err := proposal.AcceptOrchestration(context.Background(), id, f)
	if err != nil {
		t.Fatalf("AcceptOrchestration: %v", err)
	}
	if gotResolved != resolved {
		t.Errorf("expected the resolved row from ResolveAccepted, got %+v", gotResolved)
	}
	if gotCreated != createdEntity {
		t.Errorf("expected the created entity from Materialize, got %+v", gotCreated)
	}
	// GTD-1f42debd: assert the data-flow guarantee, not just the call-order
	// guarantee below — bound to f.prepared (the fixture's own field) rather
	// than a re-typed "opaque-prepared-value" literal, so this only fails when
	// the value AcceptOrchestration hands to Materialize actually diverges
	// from what PrepareOutOfBand produced, not whenever someone legitimately
	// changes the fixture's prepared value.
	if f.materializeGotPrepared != f.prepared {
		t.Errorf("Materialize received prepared=%v, want the PrepareOutOfBand output %v", f.materializeGotPrepared, f.prepared)
	}
	wantCalls := []string{"ReadPending", "PrepareOutOfBand", "BeginTx", "Materialize", "ResolveAccepted", "Commit", "Rollback"}
	if !reflect.DeepEqual(f.calls, wantCalls) {
		t.Errorf("unexpected call sequence: %v", f.calls)
	}
}
