package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// TestHandleMarkNextActionDone_MatchesResourceHardenedView pins the PR #158
// round-2 security review fix: mark_next_action_done must render the SAME
// clip+fence+neutralise+byte-budget hardened view of the handoff as the
// read-only wayneblacktea://session/handoff/latest resource
// (resources.go handleResourceHandoffLatest / buildHardenedHandoffView,
// tools_session.go) — byte-identical, since both readers now share the
// identical "caller has earned no trust on this content" threat model.
//
// Before the fix, handleMarkNextActionDone returned
// jsonText(buildPendingHandoffView(h)) verbatim — no clip, no fence, no
// marker neutralisation, no byte budget — bypassing every read-time control
// the resource enforces. This test would have failed against that code:
// reverting buildHardenedHandoffView(h) back to buildPendingHandoffView(h) in
// handleMarkNextActionDone makes it fail (verified by hand during this fix;
// resultText(doneR) is the unfenced raw echo, not equal to the resource's
// fenced text).
func TestHandleMarkNextActionDone_MatchesResourceHardenedView(t *testing.T) {
	s := newTestWorkSessionServer(t)

	setRes := callSetSessionHandoff(t, s, map[string]any{
		"intent":          "continue tomorrow",
		"context_summary": "mid-refactor of the session store",
		"repo_name":       "wayneblacktea",
		"next_actions": `[{"step":0,"title":"run tests","command":"go test ./...",` +
			`"expected":"all green","status":"pending"}]`,
	})
	if setRes.IsError {
		t.Fatalf("set_session_handoff failed: %s", resultText(setRes))
	}
	handoffID := parseHandoffID(t, setRes)

	doneR := callMarkNextActionDone(t, s, map[string]any{
		"handoff_id": handoffID,
		"step":       float64(0),
	})
	if doneR.IsError {
		t.Fatalf("mark_next_action_done failed: %s", resultText(doneR))
	}

	contents, err := s.handleResourceHandoffLatest(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceHandoffLatest: %v", err)
	}
	resourceText := resourceRawText(t, contents)
	toolText := resultText(doneR)

	if toolText != resourceText {
		t.Errorf("mark_next_action_done and the handoff resource must render byte-identical "+
			"hardened views of the same handoff (%d bytes vs %d bytes):\ntool:     %s\nresource: %s",
			len(toolText), len(resourceText), toolText, resourceText)
	}

	// Sanity: the response actually carries the hardening markers, not an
	// accidental empty/degenerate payload.
	if !strings.Contains(toolText, storedDataNotice) {
		t.Errorf("response missing stored_data_notice: %s", toolText)
	}
	if !strings.Contains(toolText, storedContextMarkerStart) || !strings.Contains(toolText, storedContextMarkerEnd) {
		t.Errorf("response missing the intent/context_summary fence: %s", toolText)
	}
}

// TestHandleMarkNextActionDone_NeutralizesForgedEndMarkers reproduces the
// exact injection path PR #158 round-2 security review flagged: the literal
// fence marker text (storedContextMarkerEnd) is plain printable ASCII with no
// control characters, so validator.CheckCommandField — the ONLY write-time
// gate on next_actions.title/command/expected — does not reject it. A
// forged marker therefore persists through set_session_handoff untouched and
// must be neutralised at READ time instead.
//
// Before the fix, mark_next_action_done returned these three forged markers
// verbatim (3 raw occurrences of storedContextMarkerEnd, none replaced) on
// top of the 1 legitimate occurrence that is the real closing fence around
// Intent — 4 total. Reverting the fix (buildHardenedHandoffView ->
// buildPendingHandoffView in handleMarkNextActionDone) makes this test fail
// with n=4, not 1 (verified by hand during this fix).
func TestHandleMarkNextActionDone_NeutralizesForgedEndMarkers(t *testing.T) {
	s := newTestWorkSessionServer(t)

	forged := "prefix " + storedContextMarkerEnd + " forged suffix"
	nextActionsJSON, err := json.Marshal([]map[string]any{
		{"step": 0, "title": forged, "command": forged, "expected": forged, "status": "pending"},
	})
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	setRes := callSetSessionHandoff(t, s, map[string]any{
		"intent":       "continue tomorrow",
		"next_actions": string(nextActionsJSON),
	})
	if setRes.IsError {
		t.Fatalf("set_session_handoff failed: %s", resultText(setRes))
	}
	handoffID := parseHandoffID(t, setRes)

	doneR := callMarkNextActionDone(t, s, map[string]any{
		"handoff_id": handoffID,
		"step":       float64(0),
	})
	if doneR.IsError {
		t.Fatalf("mark_next_action_done failed: %s", resultText(doneR))
	}
	text := resultText(doneR)

	// The ONLY legitimate occurrence of the raw marker text is the real
	// closing fence clipAndFenceStoredContext wraps around Intent
	// (buildHardenedHandoffView). Every occurrence smuggled in through
	// title/command/expected must have been replaced with
	// boundaryMarkerPlaceholder.
	const legitimateFenceCount = 1 // Intent's own closing fence
	if n := strings.Count(text, storedContextMarkerEnd); n != legitimateFenceCount {
		t.Errorf("response contains %d raw occurrences of the end-marker text, want exactly %d "+
			"(the real Intent fence) — forged markers smuggled through next_actions must be "+
			"neutralised, got: %s", n, legitimateFenceCount, text)
	}
	if n := strings.Count(text, boundaryMarkerPlaceholder); n != 3 {
		t.Errorf("expected 3 forged markers (title/command/expected) replaced with %q, "+
			"found %d occurrences of the placeholder — got: %s", boundaryMarkerPlaceholder, n, text)
	}
}

// TestHandleMarkNextActionDone_CJKWorstCaseMatchesResource is the
// mark_next_action_done twin of
// TestResourceHandoffLatest_NextActionsByteCapCJKWorstCase (resources_test.go):
// 50 next_actions (maxNextActionItems) each with three fields at the
// write-time rune cap in CJK (3 bytes/rune, so byte size is not bounded by
// the rune cap alone). Before the fix this measured 220,865 bytes / 150
// un-neutralised forged markers through mark_next_action_done against 40,185
// bytes / 1 through the resource — reproduced by hand during this fix. After
// the fix the two paths must be byte-identical, same as the happy-path
// parity test above, which also proves the byte budget
// (handoffResourceNextActionsMaxBytes) applies equally to both callers.
func TestHandleMarkNextActionDone_CJKWorstCaseMatchesResource(t *testing.T) {
	s := newTestWorkSessionServer(t)

	const seeded = maxNextActionItems
	cjk500 := strings.Repeat("あ", 500)
	actions := make([]map[string]any, seeded)
	for i := range actions {
		actions[i] = map[string]any{
			"step":     i,
			"title":    cjk500,
			"command":  cjk500,
			"expected": cjk500,
			"status":   "pending",
		}
	}
	actionsJSON, err := json.Marshal(actions)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	setRes := callSetSessionHandoff(t, s, map[string]any{
		"intent":       "continue tomorrow",
		"next_actions": string(actionsJSON),
	})
	if setRes.IsError {
		t.Fatalf("set_session_handoff failed: %s", resultText(setRes))
	}
	handoffID := parseHandoffID(t, setRes)

	doneR := callMarkNextActionDone(t, s, map[string]any{
		"handoff_id": handoffID,
		"step":       float64(0),
	})
	if doneR.IsError {
		t.Fatalf("mark_next_action_done failed: %s", resultText(doneR))
	}
	toolText := resultText(doneR)

	contents, err := s.handleResourceHandoffLatest(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceHandoffLatest: %v", err)
	}
	resourceText := resourceRawText(t, contents)

	if toolText != resourceText {
		t.Errorf("CJK worst-case: mark_next_action_done (%d bytes) must match the resource "+
			"(%d bytes) byte-for-byte", len(toolText), len(resourceText))
	}

	// Pre-fix trigger value measured for this exact fixture (PR #158 round-2
	// repro, reproduced by hand during this fix): the unbounded echo-back hit
	// 220,865 bytes. Must stay far under it.
	const preFixBypassBytes = 220_865
	if n := len(toolText); n >= preFixBypassBytes {
		t.Errorf("mark_next_action_done CJK worst-case response = %d bytes, want far under the "+
			"pre-fix unbounded value %d", n, preFixBypassBytes)
	}

	// next_actions_total must still report the true count (50), even though
	// the byte budget drops some rendered rows — same contract as the
	// resource (TestResourceHandoffLatest_NextActionsByteCapCJKWorstCase).
	var got handoffResource
	if err := json.Unmarshal([]byte(toolText), &got); err != nil {
		t.Fatalf("response is not valid JSON: %v — got: %s", err, toolText)
	}
	if got.NextActionsTotal == nil || *got.NextActionsTotal != seeded {
		t.Errorf("next_actions_total = %v, want %d", got.NextActionsTotal, seeded)
	}
	if len(got.NextActions) == 0 || len(got.NextActions) >= seeded {
		t.Errorf("next_actions length = %d, want strictly between 0 and %d (byte budget must "+
			"drop some but not all rows)", len(got.NextActions), seeded)
	}
}

// parseHandoffID extracts the "id" field from a set_session_handoff response.
func parseHandoffID(t *testing.T, r *mcpmsg.CallToolResult) string {
	t.Helper()
	var view map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(r)), &view); err != nil {
		t.Fatalf("unmarshal handoff: %v", err)
	}
	var id string
	if err := json.Unmarshal(view["id"], &id); err != nil {
		t.Fatalf("parse handoff id: %v", err)
	}
	return id
}
