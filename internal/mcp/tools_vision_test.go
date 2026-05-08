package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// ----- helpers -----

func callAddVision(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleAddVisionItem(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAddVisionItem error: %v", err)
	}
	return result
}

func callListVision(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleListVisionItems(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListVisionItems error: %v", err)
	}
	return result
}

func callUpdateVision(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleUpdateVisionItem(context.Background(), req)
	if err != nil {
		t.Fatalf("handleUpdateVisionItem error: %v", err)
	}
	return result
}

func callPromoteVision(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handlePromoteVisionToTask(context.Background(), req)
	if err != nil {
		t.Fatalf("handlePromoteVisionToTask error: %v", err)
	}
	return result
}

// extractID parses the first UUID from a ToolResult JSON body.
func extractVisionID(t *testing.T, r *mcpmsg.CallToolResult) uuid.UUID {
	t.Helper()
	raw := resultText(r)
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("extractVisionID: parse JSON: %v\nbody: %s", err, raw)
	}
	rawID, ok := m["id"].(string)
	if !ok {
		t.Fatalf("extractVisionID: no 'id' field in: %s", raw)
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		t.Fatalf("extractVisionID: parse UUID %q: %v", rawID, err)
	}
	return id
}

// ---- add_vision_item ----

func TestHandleAddVisionItem_MissingTitle(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddVision(t, s, map[string]any{
		"why_blocked": "Need to investigate",
	})
	if !r.IsError {
		t.Error("expected error for missing title")
	}
	if !strings.Contains(resultText(r), "title") {
		t.Errorf("error should mention 'title', got: %s", resultText(r))
	}
}

func TestHandleAddVisionItem_MissingWhyBlocked(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddVision(t, s, map[string]any{
		"title": "Some future idea",
	})
	if !r.IsError {
		t.Error("expected error for missing why_blocked")
	}
	if !strings.Contains(resultText(r), "why_blocked") {
		t.Errorf("error should mention 'why_blocked', got: %s", resultText(r))
	}
}

func TestHandleAddVisionItem_InvalidDependsOnJSON(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddVision(t, s, map[string]any{
		"title":       "Some idea",
		"why_blocked": "Need something first",
		"depends_on":  "{not valid json array}",
	})
	if !r.IsError {
		t.Error("expected error for invalid depends_on JSON")
	}
}

func TestHandleAddVisionItem_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callAddVision(t, s, map[string]any{
		"title":             "Add semantic search",
		"why_blocked":       "Needs vector DB first",
		"depends_on":        `["task-abc"]`,
		"parent_initiative": "search-revamp",
		"repo_name":         "wayneblacktea",
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "search-revamp") {
		t.Errorf("response should contain parent_initiative, got: %s", resultText(r))
	}
}

// ---- list_vision_items ----

func TestHandleListVisionItems_InvalidStatus(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListVision(t, s, map[string]any{
		"status": "invalid_status_value",
	})
	if !r.IsError {
		t.Error("expected error for invalid status")
	}
}

func TestHandleListVisionItems_EmptyResult(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callListVision(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
	// Should return JSON array (possibly empty).
	raw := resultText(r)
	if !strings.HasPrefix(strings.TrimSpace(raw), "[") {
		t.Errorf("expected JSON array, got: %s", raw)
	}
}

func TestHandleListVisionItems_FilterByStatus(t *testing.T) {
	s := newTestWorkSessionServer(t)

	// Add an item first.
	callAddVision(t, s, map[string]any{
		"title":       "Future idea",
		"why_blocked": "Some reason",
	})

	r := callListVision(t, s, map[string]any{
		"status": "open",
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
}

// ---- update_vision_item ----

func TestHandleUpdateVisionItem_MissingID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateVision(t, s, map[string]any{
		"status": "discussing",
	})
	if !r.IsError {
		t.Error("expected error for missing id")
	}
}

func TestHandleUpdateVisionItem_InvalidID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateVision(t, s, map[string]any{
		"id":     "not-a-uuid",
		"status": "discussing",
	})
	if !r.IsError {
		t.Error("expected error for invalid UUID")
	}
}

func TestHandleUpdateVisionItem_InvalidStatus(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateVision(t, s, map[string]any{
		"id":     uuid.New().String(),
		"status": "bad_status",
	})
	if !r.IsError {
		t.Error("expected error for invalid status")
	}
}

func TestHandleUpdateVisionItem_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callUpdateVision(t, s, map[string]any{
		"id":     uuid.New().String(),
		"status": "discussing",
	})
	if !r.IsError {
		t.Error("expected not-found error")
	}
	if !strings.Contains(resultText(r), "not found") {
		t.Errorf("error should say 'not found', got: %s", resultText(r))
	}
}

func TestHandleUpdateVisionItem_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)

	// Add an item.
	added := callAddVision(t, s, map[string]any{
		"title":       "Update test vision",
		"why_blocked": "For testing",
	})
	if added.IsError {
		t.Fatalf("add failed: %s", resultText(added))
	}
	id := extractVisionID(t, added)

	// Update it.
	r := callUpdateVision(t, s, map[string]any{
		"id":         id.String(),
		"status":     string(vision.VisionStatusDiscussing),
		"context_md": "We discussed this on 2026-05-08.",
	})
	if r.IsError {
		t.Fatalf("update failed: %s", resultText(r))
	}
	if !strings.Contains(resultText(r), "discussing") {
		t.Errorf("response should contain 'discussing', got: %s", resultText(r))
	}
}

// ---- promote_vision_to_task ----

func TestHandlePromoteVisionToTask_MissingID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callPromoteVision(t, s, map[string]any{})
	if !r.IsError {
		t.Error("expected error for missing id")
	}
}

func TestHandlePromoteVisionToTask_InvalidID(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callPromoteVision(t, s, map[string]any{
		"id": "not-a-uuid",
	})
	if !r.IsError {
		t.Error("expected error for invalid UUID")
	}
}

func TestHandlePromoteVisionToTask_NotFound(t *testing.T) {
	s := newTestWorkSessionServer(t)
	r := callPromoteVision(t, s, map[string]any{
		"id": uuid.New().String(),
	})
	if !r.IsError {
		t.Error("expected not-found error")
	}
}

func TestHandlePromoteVisionToTask_HappyPath(t *testing.T) {
	s := newTestWorkSessionServer(t)

	// Add a vision item.
	added := callAddVision(t, s, map[string]any{
		"title":       "Promote to task vision",
		"why_blocked": "Was blocked, now ready",
	})
	if added.IsError {
		t.Fatalf("add failed: %s", resultText(added))
	}
	id := extractVisionID(t, added)

	// Promote.
	r := callPromoteVision(t, s, map[string]any{
		"id":          id.String(),
		"title":       "Promoted task title",
		"description": "This was a vision item.",
		"priority":    float64(2),
	})
	if r.IsError {
		t.Fatalf("promote failed: %s", resultText(r))
	}

	raw := resultText(r)
	if !strings.Contains(raw, "promoted") {
		t.Errorf("response should show status=promoted, got: %s", raw)
	}
	// Result should have both task and vision_item keys.
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if _, ok := result["task"]; !ok {
		t.Error("result should have 'task' key")
	}
	if _, ok := result["vision_item"]; !ok {
		t.Error("result should have 'vision_item' key")
	}
}
