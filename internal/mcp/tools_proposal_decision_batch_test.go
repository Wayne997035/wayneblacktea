package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// M-NEW-1: MCP confirm_proposals (plural) was previously routing all accepts
// through proposal.BatchConfirm — which only flips status and never runs the
// materialiser. For TypeDecision rows this caused the same C-1 class data
// loss as the singular path round-1 bug. These tests verify the new
// batchAccept dispatch goes through acceptProposal so decisions row exists
// post-call.
// ---------------------------------------------------------------------------

const mcpBatchDecisionTitle = "Batch MCP decision: defer kafka rollout"

// createDecisionProposal inserts a TypeDecision pending_proposals row through
// the public Create API and returns its UUID. Encapsulated here so the test
// body stays focused on the assertion.
func createDecisionProposal(t *testing.T, s *Server, title string) uuid.UUID {
	t.Helper()
	payload, err := json.Marshal(proposal.DecisionProposerPayload{
		Title:        title,
		Decision:     "Hold on kafka migration until Q3.",
		Rationale:    "Current ops headcount can't absorb on-call rotation yet.",
		Alternatives: []string{"managed kafka via Confluent Cloud", "switch to NATS"},
		SessionID:    "mcp-batch-roundtrip",
		TriggerTool:  "complete_task",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	row, err := s.proposal.Create(context.Background(), proposal.CreateParams{
		Type:    proposal.TypeDecision,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	return row.ID
}

// TestHandleConfirmProposals_TypeDecision_Materialises verifies a single-id
// batch accept with TypeDecision triggers the materialiser. Uses the real
// SQLite-backed Server so the assertion goes against actual decisions table
// state — matching the round-trip test pattern in
// internal/proposal/decision_roundtrip_test.go.
func TestHandleConfirmProposals_TypeDecision_Materialises(t *testing.T) {
	s := newProposalTestServer(t)
	ctx := context.Background()

	propID := createDecisionProposal(t, s, mcpBatchDecisionTitle)

	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{propID.String()},
		"action": "accept",
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}

	// Result must report exactly one accepted entry.
	body := resultText(r)
	if !strings.Contains(body, `"accepted": 1`) && !strings.Contains(body, `"accepted":1`) {
		t.Errorf("expected accepted:1, got: %s", body)
	}

	// Critical: the decision MUST exist in the decisions table now.
	rows, err := s.sqliteDecision.All(ctx, 10)
	if err != nil {
		t.Fatalf("decisions.All: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("decisions count = %d, want 1 — batch accept must materialise TypeDecision", len(rows))
	}
	if rows[0].Title != mcpBatchDecisionTitle {
		t.Errorf("decisions[0].Title = %q, want %q", rows[0].Title, mcpBatchDecisionTitle)
	}

	// Proposal MUST be marked accepted.
	pp, err := s.proposal.Get(ctx, propID)
	if err != nil {
		t.Fatalf("proposal.Get post-accept: %v", err)
	}
	if pp.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal.Status = %q, want accepted", pp.Status)
	}
}

// TestHandleConfirmProposals_MixedBatch_DecisionAndConcept verifies a batch
// containing both TypeDecision + TypeConcept ids materialises both entities.
// This is the core round-trip property the singular-only round-2 fix missed.
func TestHandleConfirmProposals_MixedBatch_DecisionAndConcept(t *testing.T) {
	s := newProposalTestServer(t)
	ctx := context.Background()

	decID := createDecisionProposal(t, s, "Mixed batch: decision arm")

	// Add a concept proposal via direct Create. The shape mirrors what
	// AutoProposeConceptFromKnowledge would produce.
	conceptPayloadBytes, err := json.Marshal(map[string]any{
		"title":   "Cognitive load theory",
		"content": "Working memory is finite; chunk material to extend retention.",
		"tags":    []string{"learning", "psychology"},
	})
	if err != nil {
		t.Fatalf("marshal concept payload: %v", err)
	}
	conceptRow, err := s.proposal.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeConcept,
		Payload: conceptPayloadBytes,
	})
	if err != nil {
		t.Fatalf("create concept proposal: %v", err)
	}

	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{decID.String(), conceptRow.ID.String()},
		"action": "accept",
	})
	if r.IsError {
		t.Fatalf("expected success, got: %s", resultText(r))
	}

	body := resultText(r)
	if !strings.Contains(body, `"accepted": 2`) && !strings.Contains(body, `"accepted":2`) {
		t.Errorf("expected accepted:2, got: %s", body)
	}

	// Decisions table has the decision row.
	decisions, err := s.sqliteDecision.All(ctx, 10)
	if err != nil {
		t.Fatalf("decisions.All: %v", err)
	}
	if len(decisions) != 1 {
		t.Errorf("decisions count = %d, want 1", len(decisions))
	}

	// Both proposals are accepted.
	for _, id := range []uuid.UUID{decID, conceptRow.ID} {
		pp, err := s.proposal.Get(ctx, id)
		if err != nil {
			t.Fatalf("proposal.Get(%s): %v", id, err)
		}
		if pp.Status != string(proposal.StatusAccepted) {
			t.Errorf("proposal %s status = %q, want accepted", id, pp.Status)
		}
	}
}

// TestHandleConfirmProposals_BadDecisionPayload_FailsThatIDOnly verifies a
// malformed TypeDecision payload yields a per-id failure entry but does NOT
// fail the whole batch — the other ids still resolve.
func TestHandleConfirmProposals_BadDecisionPayload_FailsThatIDOnly(t *testing.T) {
	s := newProposalTestServer(t)
	ctx := context.Background()

	// Create one good + one bad decision proposal. The bad one is created
	// directly because Create accepts arbitrary JSON bytes — the SQLite
	// CHECK constraint only validates `type`, not payload structure.
	goodID := createDecisionProposal(t, s, "Good payload — should resolve")
	badRow, err := s.proposal.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeDecision,
		Payload: []byte(`{not valid json`),
	})
	if err != nil {
		t.Fatalf("create bad proposal: %v", err)
	}

	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{badRow.ID.String(), goodID.String()},
		"action": "accept",
	})
	if r.IsError {
		t.Fatalf("expected aggregate success (per-id failures only), got: %s", resultText(r))
	}

	body := resultText(r)
	// Expect 1 accepted (good) + 1 failed (bad payload).
	if !strings.Contains(body, `"accepted": 1`) && !strings.Contains(body, `"accepted":1`) {
		t.Errorf("expected accepted:1, got: %s", body)
	}
	if !strings.Contains(body, `"failed": 1`) && !strings.Contains(body, `"failed":1`) {
		t.Errorf("expected failed:1, got: %s", body)
	}

	// Good proposal must be accepted; bad proposal must remain pending.
	good, err := s.proposal.Get(ctx, goodID)
	if err != nil {
		t.Fatalf("good proposal.Get: %v", err)
	}
	if good.Status != string(proposal.StatusAccepted) {
		t.Errorf("good proposal status = %q, want accepted", good.Status)
	}
	bad, err := s.proposal.Get(ctx, badRow.ID)
	if err != nil {
		t.Fatalf("bad proposal.Get: %v", err)
	}
	if bad.Status != string(proposal.StatusPending) {
		t.Errorf("bad proposal status = %q, want still pending", bad.Status)
	}

	// Decisions table has exactly the good row.
	rows, err := s.sqliteDecision.All(ctx, 10)
	if err != nil {
		t.Fatalf("decisions.All: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("decisions count = %d, want 1 (only good payload materialised)", len(rows))
	}
}
