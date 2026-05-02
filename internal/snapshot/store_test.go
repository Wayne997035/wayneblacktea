package snapshot_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
)

// ---------------------------------------------------------------------------
// Stub store for unit testing
// ---------------------------------------------------------------------------

type stubStore struct {
	written     []*snapshot.Snapshot
	latestFresh *snapshot.Snapshot
	latestSlugs []string
	writeErr    error
	freshErr    error
	slugsErr    error
}

func (s *stubStore) Write(_ context.Context, p snapshot.WriteParams) (*snapshot.Snapshot, error) {
	if s.writeErr != nil {
		return nil, s.writeErr
	}
	snap := &snapshot.Snapshot{
		Slug:           p.Slug,
		GeneratedAt:    time.Now(),
		SprintSummary:  p.SprintSummary,
		GapAnalysis:    p.GapAnalysis,
		SotaCatchupPct: p.SotaCatchupPct,
		PendingSummary: p.PendingSummary,
		Source:         p.Source,
	}
	s.written = append(s.written, snap)
	s.latestFresh = snap // auto-update for subsequent LatestFresh calls
	return snap, nil
}

func (s *stubStore) LatestFresh(_ context.Context, _ string, _ time.Duration) (*snapshot.Snapshot, error) {
	if s.freshErr != nil {
		return nil, s.freshErr
	}
	if s.latestFresh == nil {
		return nil, snapshot.ErrNotFound
	}
	return s.latestFresh, nil
}

func (s *stubStore) LatestSlugs(_ context.Context) ([]string, error) {
	if s.slugsErr != nil {
		return nil, s.slugsErr
	}
	return s.latestSlugs, nil
}

// ---------------------------------------------------------------------------
// Tests for IsNotFound
// ---------------------------------------------------------------------------

func TestIsNotFound_WithErrNotFound(t *testing.T) {
	if !snapshot.IsNotFound(snapshot.ErrNotFound) {
		t.Error("IsNotFound(ErrNotFound) should be true")
	}
}

func TestIsNotFound_WithWrappedErr(t *testing.T) {
	wrapped := errors.New("snapshot: not found in store")
	if !snapshot.IsNotFound(wrapped) {
		t.Error("IsNotFound should match wrapped error containing ErrNotFound message")
	}
}

func TestIsNotFound_WithDifferentErr(t *testing.T) {
	if snapshot.IsNotFound(errors.New("some other error")) {
		t.Error("IsNotFound should return false for unrelated errors")
	}
}

func TestIsNotFound_WithNilErr(t *testing.T) {
	if snapshot.IsNotFound(nil) {
		t.Error("IsNotFound(nil) should return false")
	}
}
