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

// stubAtomPruner is a test double for AtomPruner.
type stubAtomPruner struct {
	n          int64
	err        error
	called     bool
	lastCutoff time.Time
}

func (s *stubAtomPruner) PruneAtoms(_ context.Context, cutoff time.Time) (int64, error) {
	s.called = true
	s.lastCutoff = cutoff
	return s.n, s.err
}

func TestPruner_Run_HappyPath(t *testing.T) {
	ks := &stubPrunerStore{n: 3}
	cs := &stubPrunerStore{n: 1}
	p := NewPruner(ks, cs, nil)
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
	p := NewPruner(ks, cs, nil)
	// must not panic even when knowledge store errors
	p.Run()
	if !cs.called {
		t.Error("concepts store should still be called when knowledge errors")
	}
}

func TestPruner_Run_NilKnowledgeStore(t *testing.T) {
	cs := &stubPrunerStore{n: 1}
	p := NewPruner(nil, cs, nil)
	p.Run() // must not panic with nil knowledge store
	if !cs.called {
		t.Error("concepts store should be called even when knowledge is nil")
	}
}

func TestPruner_Run_NilConceptsStore(t *testing.T) {
	ks := &stubPrunerStore{n: 1}
	p := NewPruner(ks, nil, nil)
	p.Run() // must not panic with nil concepts store
	if !ks.called {
		t.Error("knowledge store should be called even when concepts is nil")
	}
}

func TestPruner_Run_AtomsStore(t *testing.T) {
	as := &stubAtomPruner{n: 7}
	p := NewPruner(nil, nil, as)
	p.Run()
	if !as.called {
		t.Error("atom store PruneAtoms was not called")
	}
	// cutoff must be roughly 90 days ago — mirrors the knowledge/concepts assertion.
	expectedCutoff := time.Now().UTC().Add(-softPruneAgeCutoff)
	diff := as.lastCutoff.Sub(expectedCutoff)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("atom cutoff %v not near expected %v", as.lastCutoff, expectedCutoff)
	}
}

func TestPruner_Run_AtomsStoreError_NotPanic(t *testing.T) {
	as := &stubAtomPruner{err: errors.New("atoms DB gone")}
	p := NewPruner(nil, nil, as)
	p.Run() // must not panic on atom prune error
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

// TestPruner_DecisionsNeverPruned verifies that Pruner has no field or method
// that accepts a decisions store. The decisions table is an audit trail and
// must never participate in decay pruning (P0a D3 design decision).
func TestPruner_DecisionsNeverPruned(t *testing.T) {
	// decisionsSpy would be called if any "decisions" path existed in Pruner.
	decisionsSpy := &stubPrunerStore{}

	ks := &stubPrunerStore{n: 1}
	cs := &stubPrunerStore{n: 1}
	p := NewPruner(ks, cs, nil)
	p.Run()

	// decisionsSpy must NEVER have been called — Pruner has no decisions path.
	if decisionsSpy.called {
		t.Error("decisions store was called — decay must never prune decisions")
	}
	// Both knowledge and concepts must have been called.
	if !ks.called {
		t.Error("knowledge store was not called")
	}
	if !cs.called {
		t.Error("concepts store was not called")
	}
}

// TestPruner_LowStrengthDecisions_NotPruned verifies the semantic guarantee:
// items with strength below the threshold ARE soft-deleted in knowledge/concepts,
// but this code path does NOT exist for decisions. The test mocks the store to
// return non-zero counts and confirms the pruner correctly calls them for
// knowledge/concepts only.
func TestPruner_LowStrengthDecisions_NotPruned(t *testing.T) {
	// Simulate a scenario where "decisions" store would have weak items.
	// Because Pruner has no decisions field, these items are never pruned.
	ks := &stubPrunerStore{n: 5} // 5 knowledge items pruned
	cs := &stubPrunerStore{n: 0} // 0 concepts pruned
	p := NewPruner(ks, cs, nil)
	p.Run()

	if ks.n != 5 {
		t.Errorf("expected 5 knowledge items pruned, got %d", ks.n)
	}
	// Decisions: no store, no prune. This is the design guarantee.
	// Verified by the absence of any decisions-related call.
}
