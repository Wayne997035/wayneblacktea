package ai_test

import (
	"testing"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
)

// makeAtoms returns a slice of n AtomCandidate values for testing.
func makeAtoms(n int) []localai.AtomCandidate {
	out := make([]localai.AtomCandidate, n)
	for i := range out {
		out[i] = localai.AtomCandidate{Content: "fact", Keywords: []string{"k"}, Tags: []string{"t"}}
	}
	return out
}

// makeLinks returns a slice of n LinkCandidate values for testing.
func makeLinks(n int) []localai.LinkCandidate {
	out := make([]localai.LinkCandidate, n)
	for i := range out {
		out[i] = localai.LinkCandidate{FromIdx: 0, ToIdx: 1, LinkType: "same_entity", Confidence: 0.9}
	}
	return out
}

// TestCapAtomizeResult_AtomsBelowCap verifies that a result with exactly MaxAtoms
// atoms is not truncated.
func TestCapAtomizeResult_AtomsBelowCap(t *testing.T) {
	r := &localai.AtomizeResult{
		Atoms: makeAtoms(localai.MaxAtoms),
		Links: makeLinks(1),
	}
	localai.CapAtomizeResult(r)
	if len(r.Atoms) != localai.MaxAtoms {
		t.Errorf("expected %d atoms, got %d", localai.MaxAtoms, len(r.Atoms))
	}
}

// TestCapAtomizeResult_AtomsExceedCap verifies that a result with more than
// MaxAtoms atoms is truncated to exactly MaxAtoms.
func TestCapAtomizeResult_AtomsExceedCap(t *testing.T) {
	overCount := localai.MaxAtoms + 50
	r := &localai.AtomizeResult{
		Atoms: makeAtoms(overCount),
		Links: makeLinks(1),
	}
	localai.CapAtomizeResult(r)
	if len(r.Atoms) != localai.MaxAtoms {
		t.Errorf("expected atoms capped to %d, got %d", localai.MaxAtoms, len(r.Atoms))
	}
}

// TestCapAtomizeResult_LinksExceedCap verifies that a result with more than
// MaxLinks links is truncated to exactly MaxLinks.
func TestCapAtomizeResult_LinksExceedCap(t *testing.T) {
	overCount := localai.MaxLinks + 100
	r := &localai.AtomizeResult{
		Atoms: makeAtoms(1),
		Links: makeLinks(overCount),
	}
	localai.CapAtomizeResult(r)
	if len(r.Links) != localai.MaxLinks {
		t.Errorf("expected links capped to %d, got %d", localai.MaxLinks, len(r.Links))
	}
}

// TestCapAtomizeResult_BothExceedCap verifies that both atoms and links are
// independently capped when both exceed their limits.
func TestCapAtomizeResult_BothExceedCap(t *testing.T) {
	r := &localai.AtomizeResult{
		Atoms: makeAtoms(localai.MaxAtoms + 500),
		Links: makeLinks(localai.MaxLinks + 1000),
	}
	localai.CapAtomizeResult(r)
	if len(r.Atoms) != localai.MaxAtoms {
		t.Errorf("expected atoms capped to %d, got %d", localai.MaxAtoms, len(r.Atoms))
	}
	if len(r.Links) != localai.MaxLinks {
		t.Errorf("expected links capped to %d, got %d", localai.MaxLinks, len(r.Links))
	}
}

// TestCapAtomizeResult_Empty verifies that an empty result is not modified.
func TestCapAtomizeResult_Empty(t *testing.T) {
	r := &localai.AtomizeResult{}
	localai.CapAtomizeResult(r)
	if len(r.Atoms) != 0 {
		t.Errorf("expected 0 atoms, got %d", len(r.Atoms))
	}
	if len(r.Links) != 0 {
		t.Errorf("expected 0 links, got %d", len(r.Links))
	}
}
