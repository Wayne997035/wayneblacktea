package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
)

// ---------------------------------------------------------------------------
// Stub snapshot.StoreIface
// ---------------------------------------------------------------------------

type stubSnapshotStore struct {
	slugs       []string
	slugsErr    error
	writeCount  int
	writeErr    error
	latestFresh *snapshot.Snapshot
	freshErr    error
}

func (s *stubSnapshotStore) Write(_ context.Context, _ snapshot.WriteParams) (*snapshot.Snapshot, error) {
	s.writeCount++
	if s.writeErr != nil {
		return nil, s.writeErr
	}
	return &snapshot.Snapshot{
		Slug:          "test-slug",
		GeneratedAt:   time.Now(),
		SprintSummary: "generated",
	}, nil
}

func (s *stubSnapshotStore) LatestFresh(_ context.Context, _ string, _ time.Duration) (*snapshot.Snapshot, error) {
	if s.freshErr != nil {
		return nil, s.freshErr
	}
	if s.latestFresh == nil {
		return nil, snapshot.ErrNotFound
	}
	return s.latestFresh, nil
}

func (s *stubSnapshotStore) LatestSlugs(_ context.Context) ([]string, error) {
	if s.slugsErr != nil {
		return nil, s.slugsErr
	}
	return s.slugs, nil
}

// ---------------------------------------------------------------------------
// Stub snapshot.GeneratorIface
// ---------------------------------------------------------------------------

type stubSnapshotGen struct {
	result *snapshot.StatusResult
	genErr error
	called int
}

func (g *stubSnapshotGen) Generate(
	_ context.Context,
	_ string,
	_ decision.StoreIface,
	_ gtd.StoreIface,
) (*snapshot.StatusResult, error) {
	g.called++
	if g.genErr != nil {
		return nil, g.genErr
	}
	return g.result, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRunStatusSnapshot_HappyPath verifies that when slugs are available,
// each slug gets a snapshot generated and written.
func TestRunStatusSnapshot_HappyPath(t *testing.T) {
	store := &stubSnapshotStore{
		slugs:    []string{"wayneblacktea", "chat-gateway"},
		freshErr: snapshot.ErrNotFound, // force regeneration
	}
	gen := &stubSnapshotGen{
		result: &snapshot.StatusResult{
			SprintSummary:  "Sprint 5 complete",
			GapAnalysis:    "Integration tests pending",
			SotaCatchupPct: 75,
			PendingSummary: "PR #42 needs review",
		},
	}

	deps := statusSnapshotDeps{
		gtd:         &stubGTDStore{},
		decision:    &stubDecisionStore{},
		store:       store,
		generator:   gen,
		workspaceID: nil,
	}

	runStatusSnapshot(deps)

	if gen.called != 2 {
		t.Errorf("expected generator called 2 times (one per slug), got %d", gen.called)
	}
	if store.writeCount != 2 {
		t.Errorf("expected 2 writes, got %d", store.writeCount)
	}
}

// TestRunStatusSnapshot_FirstRunFallback verifies that when no slugs exist
// in the store (first Saturday run), the primary slug is used.
func TestRunStatusSnapshot_FirstRunFallback(t *testing.T) {
	store := &stubSnapshotStore{
		slugs:    nil, // no existing slugs
		freshErr: snapshot.ErrNotFound,
	}
	gen := &stubSnapshotGen{
		result: &snapshot.StatusResult{SprintSummary: "first run"},
	}

	deps := statusSnapshotDeps{
		gtd:       &stubGTDStore{},
		decision:  &stubDecisionStore{},
		store:     store,
		generator: gen,
	}

	runStatusSnapshot(deps)

	if gen.called != 1 {
		t.Errorf("expected 1 generator call for first-run fallback, got %d", gen.called)
	}
	if store.writeCount != 1 {
		t.Errorf("expected 1 write for first-run fallback, got %d", store.writeCount)
	}
}

// TestRunStatusSnapshot_LatestSlugsError verifies that when LatestSlugs fails,
// the job logs and skips without panicking.
func TestRunStatusSnapshot_LatestSlugsError(t *testing.T) {
	store := &stubSnapshotStore{
		slugsErr: errors.New("db connection lost"),
	}
	gen := &stubSnapshotGen{result: &snapshot.StatusResult{}}

	deps := statusSnapshotDeps{
		gtd:       &stubGTDStore{},
		decision:  &stubDecisionStore{},
		store:     store,
		generator: gen,
	}

	runStatusSnapshot(deps) // must not panic

	if gen.called != 0 {
		t.Errorf("generator should not be called when LatestSlugs errors, got %d", gen.called)
	}
}

// TestRunStatusSnapshot_GeneratorError verifies that when the generator fails
// for one slug, the job continues for remaining slugs.
func TestRunStatusSnapshot_GeneratorError(t *testing.T) {
	store := &stubSnapshotStore{
		slugs:    []string{"wayneblacktea", "chat-gateway"},
		freshErr: snapshot.ErrNotFound,
	}
	gen := &stubSnapshotGen{
		genErr: errors.New("haiku API error"),
	}

	deps := statusSnapshotDeps{
		gtd:       &stubGTDStore{},
		decision:  &stubDecisionStore{},
		store:     store,
		generator: gen,
	}

	runStatusSnapshot(deps) // must not panic

	// Generator was called for each slug but all failed.
	if gen.called != 2 {
		t.Errorf("expected generator called for each slug, got %d", gen.called)
	}
	// No writes on generator failure.
	if store.writeCount != 0 {
		t.Errorf("expected 0 writes on generator failure, got %d", store.writeCount)
	}
}

// TestListSlugsForSnapshot_WithExistingSlugs verifies that existing slugs are
// returned when available.
func TestListSlugsForSnapshot_WithExistingSlugs(t *testing.T) {
	store := &stubSnapshotStore{slugs: []string{"wayneblacktea", "chatbot-go"}}
	deps := statusSnapshotDeps{store: store}

	slugs, err := listSlugsForSnapshot(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slugs) != 2 {
		t.Errorf("expected 2 slugs, got %d", len(slugs))
	}
}

// TestListSlugsForSnapshot_EmptyFallback verifies that the primary slug is
// returned when no existing slugs are found.
func TestListSlugsForSnapshot_EmptyFallback(t *testing.T) {
	store := &stubSnapshotStore{slugs: nil}
	deps := statusSnapshotDeps{store: store}

	slugs, err := listSlugsForSnapshot(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(slugs) != 1 || slugs[0] != "wayneblacktea" {
		t.Errorf("expected fallback slug %q, got %v", "wayneblacktea", slugs)
	}
}
