package decay

import (
	"context"
	"errors"
	"testing"
	"time"
)

// stubPrunerStore is a test double for PrunerStore.
type stubPrunerStore struct {
	n   int64
	err error
	// captures the last call parameters
	lastCutoff    time.Time
	lastThreshold float64
	called        bool
}

func (s *stubPrunerStore) SoftPruneDecayed(ctx context.Context, cutoff time.Time, threshold float64) (int64, error) {
	s.called = true
	s.lastCutoff = cutoff
	s.lastThreshold = threshold
	return s.n, s.err
}

func TestPruner_Run_HappyPath(t *testing.T) {
	ks := &stubPrunerStore{n: 3}
	cs := &stubPrunerStore{n: 1}
	p := NewPruner(ks, cs)
	p.Run() // must not panic

	if !ks.called {
		t.Error("knowledge store SoftPruneDecayed was not called")
	}
	if !cs.called {
		t.Error("concepts store SoftPruneDecayed was not called")
	}
	// cutoff must be roughly 90 days ago
	expectedCutoff := time.Now().UTC().Add(-softPruneAgeCutoff)
	diff := ks.lastCutoff.Sub(expectedCutoff)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("cutoff %v not near expected %v", ks.lastCutoff, expectedCutoff)
	}
	if ks.lastThreshold != strengthThreshold {
		t.Errorf("knowledge threshold = %v, want %v", ks.lastThreshold, strengthThreshold)
	}
}

func TestPruner_Run_StoreError_Logged_NotPanic(t *testing.T) {
	ks := &stubPrunerStore{err: errors.New("DB gone")}
	cs := &stubPrunerStore{n: 2}
	p := NewPruner(ks, cs)
	// must not panic even when knowledge store errors
	p.Run()
	if !cs.called {
		t.Error("concepts store should still be called when knowledge errors")
	}
}

func TestPruner_Run_NilKnowledgeStore(t *testing.T) {
	cs := &stubPrunerStore{n: 1}
	p := NewPruner(nil, cs)
	p.Run() // must not panic with nil knowledge store
	if !cs.called {
		t.Error("concepts store should be called even when knowledge is nil")
	}
}

func TestPruner_Run_NilConceptsStore(t *testing.T) {
	ks := &stubPrunerStore{n: 1}
	p := NewPruner(ks, nil)
	p.Run() // must not panic with nil concepts store
	if !ks.called {
		t.Error("knowledge store should be called even when concepts is nil")
	}
}

func TestPruner_Threshold_Is_LowEnough(t *testing.T) {
	// Pruner threshold must match the package constant exactly.
	if strengthThreshold != 0.05 {
		t.Errorf("strengthThreshold = %v, want 0.05", strengthThreshold)
	}
}

func TestPruner_AgeCutoff_Is_AtLeast90Days(t *testing.T) {
	// 90-day floor: softPruneAgeCutoff must not be adjusted below 90 days.
	minCutoff := 90 * 24 * time.Hour
	if softPruneAgeCutoff < minCutoff {
		t.Errorf("softPruneAgeCutoff = %v, must be >= 90 days", softPruneAgeCutoff)
	}
}
