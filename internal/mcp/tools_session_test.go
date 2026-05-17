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

// --- parseAndValidateNextActions constraint tests ---

func TestParseAndValidateNextActions_TooManyItems(t *testing.T) {
	// Build 51 items — one over the maxNextActionItems = 50 cap.
	items := make([]map[string]any, 51)
	for i := range items {
		items[i] = map[string]any{"title": "item", "status": "pending"}
	}
	raw, _ := json.Marshal(items)
	_, msg := parseAndValidateNextActions(string(raw))
	if msg == "" {
		t.Fatal("expected error for 51 items")
	}
	if !strings.Contains(msg, "50") {
		t.Errorf("error should mention 50, got: %s", msg)
	}
}

func TestParseAndValidateNextActions_ExactlyFiftyItems(t *testing.T) {
	items := make([]map[string]any, 50)
	for i := range items {
		items[i] = map[string]any{"title": "item", "status": "pending"}
	}
	raw, _ := json.Marshal(items)
	_, msg := parseAndValidateNextActions(string(raw))
	if msg != "" {
		t.Fatalf("50 items should be accepted, got: %s", msg)
	}
}

func TestParseAndValidateNextActions_TitleTooLong(t *testing.T) {
	longTitle := strings.Repeat("あ", 501) // 501 runes, each is multi-byte
	raw, _ := json.Marshal([]map[string]any{{"title": longTitle, "status": "pending"}})
	_, msg := parseAndValidateNextActions(string(raw))
	if msg == "" {
		t.Fatal("expected error for title exceeding 500 runes")
	}
	if !strings.Contains(msg, "title") {
		t.Errorf("error should mention title, got: %s", msg)
	}
}

func TestParseAndValidateNextActions_InvalidRefTaskID(t *testing.T) {
	raw, _ := json.Marshal([]map[string]any{{
		"title":       "do something",
		"status":      "pending",
		"ref_task_id": "not-a-uuid",
	}})
	_, msg := parseAndValidateNextActions(string(raw))
	if msg == "" {
		t.Fatal("expected error for invalid ref_task_id UUID")
	}
	if !strings.Contains(msg, "ref_task_id") {
		t.Errorf("error should mention ref_task_id, got: %s", msg)
	}
}

func TestParseAndValidateNextActions_ValidRefTaskID(t *testing.T) {
	raw, _ := json.Marshal([]map[string]any{{
		"title":       "do something",
		"status":      "pending",
		"ref_task_id": "123e4567-e89b-12d3-a456-426614174000",
	}})
	actions, msg := parseAndValidateNextActions(string(raw))
	if msg != "" {
		t.Fatalf("valid UUID ref_task_id should pass, got: %s", msg)
	}
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
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
