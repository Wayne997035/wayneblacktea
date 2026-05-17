package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

func callSetSessionHandoff(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleSetSessionHandoff(context.Background(), req)
	if err != nil {
		t.Fatalf("handleSetSessionHandoff error: %v", err)
	}
	return res
}

// TestSetSessionHandoff_NextActionsDecoded verifies that the response encodes
// next_actions as a decoded JSON array — not as a raw base64 string — which
// was the M-3 bug (returning jsonText(h) instead of jsonText(buildPendingHandoffView(h))).
func TestSetSessionHandoff_NextActionsDecoded(t *testing.T) {
	s := newTestWorkSessionServer(t)
	nextActionsJSON := `[{"step":1,"title":"write tests","status":"pending"}]`

	r := callSetSessionHandoff(t, s, map[string]any{
		"intent":       "continue adding tests",
		"next_actions": nextActionsJSON,
	})
	if r.IsError {
		t.Fatalf("set_session_handoff must succeed, got: %s", resultText(r))
	}

	// The response must be valid JSON.
	var view map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(r)), &view); err != nil {
		t.Fatalf("response is not valid JSON: %v — got: %s", err, resultText(r))
	}

	raw, ok := view["next_actions"]
	if !ok {
		t.Fatal("response missing next_actions field")
	}

	// next_actions must be a JSON array, not a base64 string.
	text := string(raw)
	if strings.HasPrefix(text, `"`) {
		t.Errorf("next_actions is a quoted string (likely base64), want JSON array — got: %s", text)
	}

	var actions []map[string]any
	if err := json.Unmarshal(raw, &actions); err != nil {
		t.Fatalf("next_actions cannot be unmarshalled as array: %v — got: %s", err, text)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}
	if title, _ := actions[0]["title"].(string); title != "write tests" {
		t.Errorf("action title = %q, want %q", title, "write tests")
	}
}

// TestSetSessionHandoff_EmptyNextActions verifies that an omitted next_actions
// returns an empty array (not null), keeping the contract stable for clients.
func TestSetSessionHandoff_EmptyNextActions(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callSetSessionHandoff(t, s, map[string]any{
		"intent": "wrap up for today",
	})
	if r.IsError {
		t.Fatalf("set_session_handoff without next_actions must succeed, got: %s", resultText(r))
	}

	var view map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(r)), &view); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}

	raw, ok := view["next_actions"]
	if !ok {
		t.Fatal("response missing next_actions field")
	}

	// Must be an array (empty [] is fine, null is not).
	var actions []map[string]any
	if err := json.Unmarshal(raw, &actions); err != nil {
		t.Fatalf("next_actions without input should be empty array, got: %s — err: %v", string(raw), err)
	}
}

// TestSetSessionHandoff_InvalidNextActionsJSON verifies that malformed JSON in
// next_actions is rejected with a tool error.
func TestSetSessionHandoff_InvalidNextActionsJSON(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callSetSessionHandoff(t, s, map[string]any{
		"intent":       "continue tomorrow",
		"next_actions": `{"not":"an array"}`,
	})
	if !r.IsError {
		t.Fatalf("next_actions as object (not array) must error, got: %s", resultText(r))
	}
}
