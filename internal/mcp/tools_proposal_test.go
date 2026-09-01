package mcp

import (
	"context"
	"encoding/json"
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

// ---- decodeGoalParams / decodeProjectParams: empty-title + priority range ----
//
// [F981-05] mirrors internal/proposal/accept_decode_length_test.go's coverage
// of the seam-side decoders — decodeGoalParams/decodeProjectParams (this
// file) previously lacked the empty-title rejection both have, and
// decodeProjectParams also lacked the priority 1-5 range check
// (backend-security-design.md §2.1: LLM tool input is hostile; these
// decoders run on a payload an agent controls via propose_goal/
// propose_project).

func TestDecodeGoalParams_EmptyTitle(t *testing.T) {
	cases := []struct {
		name       string
		payload    map[string]any
		wantErr    bool
		wantSubstr string
	}{
		{
			name:    "non-empty title → ok",
			payload: map[string]any{"title": "Become CEO", "area": "career"},
			wantErr: false,
		},
		{
			name:       "empty title → rejected",
			payload:    map[string]any{"title": "", "area": "career"},
			wantErr:    true,
			wantSubstr: "goal payload missing title",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, errMsg := decodeGoalParams(raw)
			if tc.wantErr {
				if errMsg == "" {
					t.Fatalf("decodeGoalParams: want error, got none")
				}
				if !strings.Contains(errMsg, tc.wantSubstr) {
					t.Errorf("errMsg = %q, want substring %q", errMsg, tc.wantSubstr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("decodeGoalParams: unexpected error: %s", errMsg)
			}
		})
	}
}

func TestDecodeProjectParams_EmptyTitleAndPriorityRange(t *testing.T) {
	cases := []struct {
		name       string
		payload    map[string]any
		wantErr    bool
		wantSubstr string
	}{
		{
			name:    "within limits → ok",
			payload: map[string]any{"name": "proj", "title": "Project", "area": "projects"},
			wantErr: false,
		},
		{
			name:       "empty title → rejected",
			payload:    map[string]any{"name": "proj", "title": ""},
			wantErr:    true,
			wantSubstr: "project payload missing title",
		},
		{
			name:       "priority out of range (too high) → rejected",
			payload:    map[string]any{"title": "ok", "priority": 6},
			wantErr:    true,
			wantSubstr: "project priority must be 1-5",
		},
		{
			name:       "priority out of range (negative) → rejected",
			payload:    map[string]any{"title": "ok", "priority": -1},
			wantErr:    true,
			wantSubstr: "project priority must be 1-5",
		},
		{
			name:    "priority 1 → ok (boundary)",
			payload: map[string]any{"title": "ok", "priority": 1},
			wantErr: false,
		},
		{
			name:    "priority 5 → ok (boundary)",
			payload: map[string]any{"title": "ok", "priority": 5},
			wantErr: false,
		},
		{
			name:    "priority 0 (unset) → ok",
			payload: map[string]any{"title": "ok"},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, errMsg := decodeProjectParams(raw)
			if tc.wantErr {
				if errMsg == "" {
					t.Fatalf("decodeProjectParams: want error, got none")
				}
				if !strings.Contains(errMsg, tc.wantSubstr) {
					t.Errorf("errMsg = %q, want substring %q", errMsg, tc.wantSubstr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("decodeProjectParams: unexpected error: %s", errMsg)
			}
		})
	}
}
