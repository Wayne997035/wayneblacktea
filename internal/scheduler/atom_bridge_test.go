package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Extended stub: adds ListByDigestStatus and CountByDigestStatus overrides
// ---------------------------------------------------------------------------

// bridgeAtomStore extends stubAtomStore with configurable
// CountByDigestStatus and ListByDigestStatus responses.
type bridgeAtomStore struct {
	countByStatus    int64
	countByStatusErr error
	listByStatus     []atom.Atom
	listByStatusErr  error
	setStatusCalls   []setStatusCall
	setStatusErr     error
}

type setStatusCall struct {
	atomID uuid.UUID
	status string
}

var _ atom.StoreIface = (*bridgeAtomStore)(nil)

func (s *bridgeAtomStore) AddAtom(_ context.Context, p atom.AddAtomParams) (*atom.Atom, error) {
	return &atom.Atom{ID: uuid.New(), Content: p.Content, Tags: p.Tags, CreatedAt: time.Now()}, nil
}
func (s *bridgeAtomStore) AddLink(_ context.Context, _ atom.AddLinkParams) error { return nil }
func (s *bridgeAtomStore) ListByParent(_ context.Context, _ string, _ uuid.UUID) ([]atom.Atom, error) {
	return nil, nil
}

func (s *bridgeAtomStore) Traverse(_ context.Context, _ uuid.UUID, _ int) (*atom.TraverseResult, error) {
	return &atom.TraverseResult{}, nil
}

func (s *bridgeAtomStore) Search(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]atom.Atom, error) {
	return nil, nil
}
func (s *bridgeAtomStore) PruneAtoms(_ context.Context, _ time.Time) (int64, error) { return 0, nil }
func (s *bridgeAtomStore) SetDigestStatus(_ context.Context, id uuid.UUID, status string, _ string) error {
	s.setStatusCalls = append(s.setStatusCalls, setStatusCall{atomID: id, status: status})
	return s.setStatusErr
}

func (s *bridgeAtomStore) CountByDigestStatus(_ context.Context, _ *uuid.UUID, _ string) (int64, error) {
	return s.countByStatus, s.countByStatusErr
}

func (s *bridgeAtomStore) ListByDigestStatus(_ context.Context, _ *uuid.UUID, _ string, _ int) ([]atom.Atom, error) {
	return s.listByStatus, s.listByStatusErr
}

func (s *bridgeAtomStore) CountTotal(_ context.Context, _ *uuid.UUID) (int64, error) {
	return 0, nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func makeConsolidatedAtom(content string, tags []string) atom.Atom {
	status := "consolidated"
	return atom.Atom{
		ID:           uuid.New(),
		Content:      content,
		Tags:         tags,
		DigestStatus: &status,
		CreatedAt:    time.Now(),
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestRunAtomBridge_SkipBelowThreshold verifies that the job logs and
// returns when fewer than 10 consolidated atoms exist.
func TestRunAtomBridge_SkipBelowThreshold(t *testing.T) {
	atomStore := &bridgeAtomStore{countByStatus: 5}
	propStore := &stubProposalStore{}
	wsID := uuid.New()

	deps := atomBridgeDeps{store: atomStore, proposal: propStore, workspaceID: &wsID}
	runAtomBridge(deps)

	if len(propStore.created) != 0 {
		t.Errorf("expected 0 proposals, got %d", len(propStore.created))
	}
	if len(atomStore.setStatusCalls) != 0 {
		t.Errorf("expected 0 SetDigestStatus calls, got %d", len(atomStore.setStatusCalls))
	}
}

// TestRunAtomBridge_QualityGateReject verifies atoms below the quality gate
// (too short, too few tags) are skipped.
func TestRunAtomBridge_QualityGateReject(t *testing.T) {
	tooShort := makeConsolidatedAtom("short", []string{"tag1", "tag2"})
	tooFewTags := makeConsolidatedAtom(strings.Repeat("x", 100), []string{"onetag"})
	qualifies := makeConsolidatedAtom(strings.Repeat("q", 100), []string{"tag1", "tag2"})

	atomStore := &bridgeAtomStore{
		countByStatus: 10,
		listByStatus:  []atom.Atom{tooShort, tooFewTags, qualifies},
	}
	propStore := &stubProposalStore{}
	wsID := uuid.New()

	deps := atomBridgeDeps{store: atomStore, proposal: propStore, workspaceID: &wsID}
	runAtomBridge(deps)

	if len(propStore.created) != 1 {
		t.Errorf("expected 1 proposal (only qualifying atom), got %d", len(propStore.created))
	}
	// Only the qualifying atom should have been promoted.
	if len(atomStore.setStatusCalls) != 1 {
		t.Errorf("expected 1 SetDigestStatus call, got %d", len(atomStore.setStatusCalls))
	}
	if atomStore.setStatusCalls[0].status != "promoted" {
		t.Errorf("expected status='promoted', got %q", atomStore.setStatusCalls[0].status)
	}
}

// TestRunAtomBridge_HappyPath verifies a qualifying atom creates a TypeKnowledge
// proposal with ProposedBy='atom_bridge' and sets digest_status='promoted'.
func TestRunAtomBridge_HappyPath(t *testing.T) {
	richContent := strings.Repeat("integration testing best practices: ", 5)
	a := makeConsolidatedAtom(richContent, []string{"testing", "go", "containers"})

	atomStore := &bridgeAtomStore{
		countByStatus: 15,
		listByStatus:  []atom.Atom{a},
	}
	propStore := &stubProposalStore{}
	wsID := uuid.New()

	deps := atomBridgeDeps{store: atomStore, proposal: propStore, workspaceID: &wsID}
	runAtomBridge(deps)

	if len(propStore.created) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(propStore.created))
	}
	created := propStore.created[0]
	if created.Type != string(proposal.TypeKnowledge) {
		t.Errorf("expected type='knowledge', got %q", created.Type)
	}

	// Verify ProposedBy='atom_bridge' (captured via stubProposalStore.proposedBys).
	if len(propStore.proposedBys) != 1 || propStore.proposedBys[0] != "atom_bridge" {
		t.Errorf("expected ProposedBy='atom_bridge', got %v", propStore.proposedBys)
	}

	var kp proposal.KnowledgePayload
	if err := json.Unmarshal(created.Payload, &kp); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if kp.Content != richContent {
		t.Errorf("payload content mismatch: got %q", kp.Content)
	}

	// SetDigestStatus should have been called with 'promoted'.
	if len(atomStore.setStatusCalls) != 1 {
		t.Fatalf("expected 1 SetDigestStatus call, got %d", len(atomStore.setStatusCalls))
	}
	if atomStore.setStatusCalls[0].status != "promoted" {
		t.Errorf("expected status='promoted', got %q", atomStore.setStatusCalls[0].status)
	}
	if atomStore.setStatusCalls[0].atomID != a.ID {
		t.Errorf("SetDigestStatus called on wrong atom: got %v, want %v", atomStore.setStatusCalls[0].atomID, a.ID)
	}
}

// TestRunAtomBridge_NilDepGracefulSkip verifies that nil store or proposal
// dep causes the job to return without panic.
func TestRunAtomBridge_NilDepGracefulSkip(t *testing.T) {
	wsID := uuid.New()

	tests := []struct {
		name string
		deps atomBridgeDeps
	}{
		{"nil store", atomBridgeDeps{store: nil, proposal: &stubProposalStore{}, workspaceID: &wsID}},
		{"nil proposal", atomBridgeDeps{store: &bridgeAtomStore{countByStatus: 20}, proposal: nil, workspaceID: &wsID}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic.
			runAtomBridge(tc.deps)
		})
	}
}

// TestRunAtomBridge_CJKRuneAware verifies MAJOR-3: CJK atoms whose byte length
// is >= 80 but rune count < 80 are correctly rejected by the quality gate.
// Also verifies that a 100-rune CJK atom's title is truncated without panic.
func TestRunAtomBridge_CJKRuneAware(t *testing.T) {
	// 27 CJK runes × 3 bytes each = 81 bytes but only 27 runes → reject.
	cjkShort := strings.Repeat("測", 27) // 81 bytes, 27 runes
	// 100 CJK runes → title should be truncated to 80 runes without panic.
	cjkLong := strings.Repeat("漢", 100)

	shortAtom := makeConsolidatedAtom(cjkShort, []string{"tag1", "tag2"})
	longAtom := makeConsolidatedAtom(cjkLong, []string{"tag1", "tag2"})

	atomStore := &bridgeAtomStore{
		countByStatus: 10,
		listByStatus:  []atom.Atom{shortAtom, longAtom},
	}
	propStore := &stubProposalStore{}
	wsID := uuid.New()

	deps := atomBridgeDeps{store: atomStore, proposal: propStore, workspaceID: &wsID}
	// Must not panic (title[:80] on CJK would panic without rune-aware fix).
	runAtomBridge(deps)

	// Only the long atom (100 runes) should be promoted; short (27 runes) is rejected.
	if len(propStore.created) != 1 {
		t.Fatalf("expected 1 proposal (long CJK atom only), got %d", len(propStore.created))
	}
	if len(atomStore.setStatusCalls) != 1 || atomStore.setStatusCalls[0].atomID != longAtom.ID {
		t.Errorf("expected SetDigestStatus for long CJK atom only, got %+v", atomStore.setStatusCalls)
	}
}

// TestRunAtomBridge_CreateProposalError verifies that a Create error causes
// the atom to be skipped (no SetDigestStatus call) but does not panic.
func TestRunAtomBridge_CreateProposalError(t *testing.T) {
	richContent := strings.Repeat("proposal error scenario content ", 3)
	a := makeConsolidatedAtom(richContent, []string{"err", "testing"})

	atomStore := &bridgeAtomStore{
		countByStatus: 10,
		listByStatus:  []atom.Atom{a},
	}
	propStore := &stubProposalStore{createErr: context.DeadlineExceeded}
	wsID := uuid.New()

	deps := atomBridgeDeps{store: atomStore, proposal: propStore, workspaceID: &wsID}
	runAtomBridge(deps)

	if len(atomStore.setStatusCalls) != 0 {
		t.Errorf("expected 0 SetDigestStatus calls on create error, got %d", len(atomStore.setStatusCalls))
	}
}
