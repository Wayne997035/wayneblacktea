package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callConfirmProposal invokes handleConfirmProposal with the given proposal_id
// and action, failing the test on invocation error.
func callConfirmProposal(t *testing.T, s *Server, proposalID, action string) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"proposal_id": proposalID,
		"action":      action,
	}
	result, err := s.handleConfirmProposal(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConfirmProposal error: %v", err)
	}
	return result
}

// parseTestUUID parses a UUID string and fails the test if it is invalid.
func parseTestUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parseTestUUID(%q): %v", s, err)
	}
	return id
}

// createKnowledgeProposal inserts a TypeKnowledge pending_proposals row and
// returns its UUID string. Mirrors createDecisionProposal in
// tools_proposal_decision_batch_test.go.
func createKnowledgeProposal(t *testing.T, s *Server, title, content string, tags []string) string {
	t.Helper()
	payload, err := json.Marshal(proposal.KnowledgePayload{
		Title:   title,
		Content: content,
		Tags:    tags,
	})
	if err != nil {
		t.Fatalf("marshal knowledge payload: %v", err)
	}
	row, err := s.proposal.Create(context.Background(), proposal.CreateParams{
		Type:       proposal.TypeKnowledge,
		Payload:    payload,
		ProposedBy: "reflection-cron",
	})
	if err != nil {
		t.Fatalf("create knowledge proposal: %v", err)
	}
	return row.ID.String()
}

// TestConfirmProposal_TypeKnowledge_Materialises verifies that accepting a
// TypeKnowledge proposal via the MCP tool creates a knowledge_items row of
// type "til" and marks the proposal accepted (AC1).
func TestConfirmProposal_TypeKnowledge_Materialises(t *testing.T) {
	s := newProposalTestServer(t)
	ctx := context.Background()

	propID := createKnowledgeProposal(t, s,
		"Ebbinghaus forgetting curve",
		"Memory decays exponentially without spaced repetition review.",
		[]string{"memory", "learning"},
	)

	result := callConfirmProposal(t, s, propID, "accept")
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(result))
	}

	body := resultText(result)
	// Response must include the knowledge item title.
	if !strings.Contains(body, "Ebbinghaus forgetting curve") {
		t.Errorf("response missing knowledge item title: %s", body)
	}

	// Proposal must be marked accepted.
	pp, err := s.proposal.Get(ctx, parseTestUUID(t, propID))
	if err != nil {
		t.Fatalf("proposal.Get post-accept: %v", err)
	}
	if pp.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal.Status = %q, want accepted", pp.Status)
	}
}

// TestConfirmProposal_TypeKnowledge_BadPayload verifies a malformed knowledge
// payload returns a tool error and leaves the proposal in pending state.
func TestConfirmProposal_TypeKnowledge_BadPayload(t *testing.T) {
	s := newProposalTestServer(t)
	ctx := context.Background()

	// Insert a knowledge proposal with invalid JSON payload directly.
	row, err := s.proposal.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeKnowledge,
		Payload: []byte(`{not valid json`),
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	result := callConfirmProposal(t, s, row.ID.String(), "accept")
	if !result.IsError {
		t.Errorf("expected tool error for malformed payload, got success: %s", resultText(result))
	}

	// Proposal must remain pending.
	pp, err := s.proposal.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("proposal.Get: %v", err)
	}
	if pp.Status != string(proposal.StatusPending) {
		t.Errorf("proposal.Status = %q, want pending after bad payload", pp.Status)
	}
}

// TestConfirmProposal_TypeKnowledge_MissingTitle verifies a knowledge payload
// with empty title returns a tool error.
func TestConfirmProposal_TypeKnowledge_MissingTitle(t *testing.T) {
	s := newProposalTestServer(t)
	ctx := context.Background()

	row, err := s.proposal.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeKnowledge,
		Payload: []byte(`{"title":"","content":"some content"}`),
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	result := callConfirmProposal(t, s, row.ID.String(), "accept")
	if !result.IsError {
		t.Errorf("expected tool error for missing title, got success: %s", resultText(result))
	}

	// Proposal must remain pending.
	pp, err := s.proposal.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("proposal.Get: %v", err)
	}
	if pp.Status != string(proposal.StatusPending) {
		t.Errorf("proposal.Status = %q, want still pending after missing title", pp.Status)
	}
}

// TestConfirmProposals_TypeKnowledge_BatchAccept verifies that batch accepting
// a TypeKnowledge proposal marks it accepted.
func TestConfirmProposals_TypeKnowledge_BatchAccept(t *testing.T) {
	s := newProposalTestServer(t)
	ctx := context.Background()

	propID := createKnowledgeProposal(t, s,
		"Batch knowledge TIL",
		"Knowledge items created via batch MCP confirm.",
		nil,
	)

	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{propID},
		"action": "accept",
	})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}

	body := resultText(r)
	if !strings.Contains(body, `"accepted": 1`) && !strings.Contains(body, `"accepted":1`) {
		t.Errorf("expected accepted:1, got: %s", body)
	}

	// Proposal must be marked accepted.
	pp, err := s.proposal.Get(ctx, parseTestUUID(t, propID))
	if err != nil {
		t.Fatalf("proposal.Get: %v", err)
	}
	if pp.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal.Status = %q, want accepted", pp.Status)
	}
}
