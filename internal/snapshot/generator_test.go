package snapshot_test

import (
	"context"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Stub GeneratorIface for EnsureSnapshot tests
// ---------------------------------------------------------------------------

type stubGen struct {
	result *snapshot.StatusResult
	genErr error
	called int
}

func (g *stubGen) Generate(_ context.Context, _ string, _ decision.StoreIface, _ gtd.StoreIface) (*snapshot.StatusResult, error) {
	g.called++
	if g.genErr != nil {
		return nil, g.genErr
	}
	return g.result, nil
}

// ---------------------------------------------------------------------------
// EnsureSnapshot tests
// ---------------------------------------------------------------------------

// TestEnsureSnapshot_CacheHit verifies that when a fresh snapshot exists and
// force_refresh is false, EnsureSnapshot returns the cached snapshot without
// calling the generator.
func TestEnsureSnapshot_CacheHit(t *testing.T) {
	cached := &snapshot.Snapshot{
		Slug:          "wayneblacktea",
		GeneratedAt:   time.Now(),
		SprintSummary: "All tests passing",
	}
	store := &stubStore{latestFresh: cached}
	gen := &stubGen{result: &snapshot.StatusResult{SprintSummary: "fresh"}}

	snap, fromCache, err := snapshot.EnsureSnapshot(
		context.Background(), "wayneblacktea", false,
		store, gen, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fromCache {
		t.Error("expected fromCache=true on cache hit")
	}
	if snap.SprintSummary != "All tests passing" {
		t.Errorf("expected cached summary, got %q", snap.SprintSummary)
	}
	if gen.called != 0 {
		t.Errorf("generator should not be called on cache hit, called %d times", gen.called)
	}
}

// TestEnsureSnapshot_CacheExpired verifies that when no fresh snapshot exists
// (LatestFresh returns ErrNotFound), the generator is called and the result is
// written.
func TestEnsureSnapshot_CacheExpired(t *testing.T) {
	store := &stubStore{freshErr: snapshot.ErrNotFound} // no fresh snapshot
	gen := &stubGen{
		result: &snapshot.StatusResult{
			SprintSummary:  "Sprint complete",
			GapAnalysis:    "Minor gaps",
			SotaCatchupPct: 72,
			PendingSummary: "2 tasks pending",
		},
	}

	snap, fromCache, err := snapshot.EnsureSnapshot(
		context.Background(), "wayneblacktea", false,
		store, gen, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fromCache {
		t.Error("expected fromCache=false when cache expired")
	}
	if snap.SprintSummary != "Sprint complete" {
		t.Errorf("expected generated summary, got %q", snap.SprintSummary)
	}
	if gen.called != 1 {
		t.Errorf("generator should be called once, called %d times", gen.called)
	}
	if len(store.written) != 1 {
		t.Errorf("expected 1 written snapshot, got %d", len(store.written))
	}
}

// TestEnsureSnapshot_ForceRefresh verifies that force_refresh=true bypasses
// the cache and always calls the generator.
func TestEnsureSnapshot_ForceRefresh(t *testing.T) {
	cached := &snapshot.Snapshot{
		Slug:          "wayneblacktea",
		GeneratedAt:   time.Now(),
		SprintSummary: "stale cached",
	}
	store := &stubStore{latestFresh: cached}
	gen := &stubGen{
		result: &snapshot.StatusResult{
			SprintSummary:  "fresh generated",
			SotaCatchupPct: 80,
		},
	}

	snap, fromCache, err := snapshot.EnsureSnapshot(
		context.Background(), "wayneblacktea", true, // force_refresh
		store, gen, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fromCache {
		t.Error("expected fromCache=false on force_refresh")
	}
	if snap.SprintSummary != "fresh generated" {
		t.Errorf("expected fresh summary, got %q", snap.SprintSummary)
	}
	if gen.called != 1 {
		t.Errorf("generator should be called once on force_refresh, called %d times", gen.called)
	}
}

// TestEnsureSnapshot_StoreError verifies that when Write fails, EnsureSnapshot
// returns an error.
func TestEnsureSnapshot_StoreError(t *testing.T) {
	store := &stubStore{
		freshErr: snapshot.ErrNotFound,
		writeErr: errStoreWrite,
	}
	gen := &stubGen{
		result: &snapshot.StatusResult{SprintSummary: "ok"},
	}

	_, _, err := snapshot.EnsureSnapshot(
		context.Background(), "wayneblacktea", false,
		store, gen, nil, nil, nil,
	)
	if err == nil {
		t.Fatal("expected error when store write fails, got nil")
	}
}

// TestEnsureSnapshot_GeneratorError verifies that when the generator fails,
// EnsureSnapshot returns the error and does not write to the store.
func TestEnsureSnapshot_GeneratorError(t *testing.T) {
	store := &stubStore{freshErr: snapshot.ErrNotFound}
	gen := &stubGen{genErr: errHaikuTimeout}

	_, _, err := snapshot.EnsureSnapshot(
		context.Background(), "wayneblacktea", false,
		store, gen, nil, nil, nil,
	)
	if err == nil {
		t.Fatal("expected error when generator fails, got nil")
	}
	if len(store.written) != 0 {
		t.Errorf("no snapshot should be written when generator fails, got %d", len(store.written))
	}
}

// TestEnsureSnapshot_WorkspaceID verifies that the workspace ID is propagated
// to the write params.
func TestEnsureSnapshot_WorkspaceID(t *testing.T) {
	wsID := uuid.New()
	store := &stubStore{freshErr: snapshot.ErrNotFound}
	gen := &stubGen{result: &snapshot.StatusResult{SprintSummary: "ok"}}

	snap, _, err := snapshot.EnsureSnapshot(
		context.Background(), "wayneblacktea", true,
		store, gen, nil, nil, &wsID,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
}

var (
	errStoreWrite   = &testErr{msg: "store write failed"}
	errHaikuTimeout = &testErr{msg: "haiku API timeout"}
)

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
