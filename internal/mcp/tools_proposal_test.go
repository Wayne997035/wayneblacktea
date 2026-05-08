package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callConfirmProposals is a helper that invokes handleConfirmProposals with
// the given args and fails the test if the invocation itself returns an error.
func callConfirmProposals(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleConfirmProposals(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConfirmProposals error: %v", err)
	}
	return result
}

// newProposalTestServer creates a minimal Server backed by SQLite for proposal tool tests.
func newProposalTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestWorkSessionServer(t)
}

// ---- input validation tests ----

func TestHandleConfirmProposals_InvalidAction(t *testing.T) {
	s := newProposalTestServer(t)
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{uuid.New().String()},
		"action": "destroy",
	})
	if !r.IsError {
		t.Error("expected error for invalid action")
	}
	if !strings.Contains(resultText(r), "accept") {
		t.Errorf("error should mention 'accept', got: %s", resultText(r))
	}
}

func TestHandleConfirmProposals_EmptyIDs(t *testing.T) {
	s := newProposalTestServer(t)
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{},
		"action": "accept",
	})
	if !r.IsError {
		t.Error("expected error for empty ids")
	}
}

func TestHandleConfirmProposals_TooManyIDs(t *testing.T) {
	s := newProposalTestServer(t)
	ids := make([]any, 101)
	for i := range ids {
		ids[i] = uuid.New().String()
	}
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    ids,
		"action": "reject",
	})
	if !r.IsError {
		t.Error("expected error for >100 ids")
	}
}

func TestHandleConfirmProposals_InvalidUUID(t *testing.T) {
	s := newProposalTestServer(t)
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{"not-a-uuid"},
		"action": "accept",
	})
	if !r.IsError {
		t.Error("expected error for invalid UUID")
	}
}

func TestHandleConfirmProposals_NonStringIDElement(t *testing.T) {
	s := newProposalTestServer(t)
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{42}, // integer, not string
		"action": "accept",
	})
	if !r.IsError {
		t.Error("expected error for non-string id element")
	}
}

// ---- happy path: proposal created then confirmed ----

func TestHandleConfirmProposals_RejectPending(t *testing.T) {
	s := newProposalTestServer(t)
	ctx := context.Background()

	// Create a proposal first via handleProposeGoal.
	propReq := mcpmsg.CallToolRequest{}
	propReq.Params.Arguments = map[string]any{
		"title": "Batch test goal",
		"area":  "engineering",
	}
	propResult, err := s.handleProposeGoal(ctx, propReq)
	if err != nil || propResult.IsError {
		t.Fatalf("handleProposeGoal: err=%v isError=%v text=%s", err, propResult.IsError, resultText(propResult))
	}

	// Extract the proposal ID from the JSON result. The indented JSON produced
	// by jsonText uses `"id": "<uuid>"` (space after colon).
	text := resultText(propResult)
	// Try both compact and indented forms.
	var rawID string
	for _, prefix := range []string{`"id": "`, `"id":"`} {
		start := strings.Index(text, prefix)
		if start == -1 {
			continue
		}
		start += len(prefix)
		end := strings.Index(text[start:], `"`)
		if end == -1 {
			continue
		}
		candidate := text[start : start+end]
		if _, err := uuid.Parse(candidate); err == nil {
			rawID = candidate
			break
		}
	}
	if rawID == "" {
		t.Fatalf("could not find valid UUID id in response: %s", text)
	}

	// Batch confirm (reject) the proposal.
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{rawID},
		"action": "reject",
	})
	if r.IsError {
		t.Errorf("expected success, got error: %s", resultText(r))
	}
	result := resultText(r)
	// jsonText uses indented JSON so check both compact and indented forms.
	if !strings.Contains(result, `"accepted": 1`) && !strings.Contains(result, `"accepted":1`) {
		t.Errorf("expected accepted:1 in result, got: %s", result)
	}
}

func TestHandleConfirmProposals_ExactlyMaxIDs_NoProposals(t *testing.T) {
	// Exactly 100 random UUIDs — none are in the store so all will fail,
	// but validation must pass (not a 400-equivalent tool error on count).
	s := newProposalTestServer(t)
	ids := make([]any, 100)
	for i := range ids {
		ids[i] = uuid.New().String()
	}
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    ids,
		"action": "accept",
	})
	// Should not be a validation error (count is OK); the result will show
	// all failed because the IDs don't exist.
	if r.IsError {
		t.Errorf("100 IDs should not trigger validation error, got: %s", resultText(r))
	}
	result := resultText(r)
	// jsonText uses indented JSON so check both compact and indented forms.
	if !strings.Contains(result, `"failed": 100`) && !strings.Contains(result, `"failed":100`) {
		t.Errorf("expected failed:100 in result, got: %s", result)
	}
}
