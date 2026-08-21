package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callResolveHandoff invokes resolve_handoff with the given handoff_id.
func callResolveHandoff(t *testing.T, s *Server, handoffID string) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = map[string]any{"handoff_id": handoffID}
	res, err := s.handleResolveHandoff(context.Background(), req)
	if err != nil {
		t.Fatalf("handleResolveHandoff error: %v", err)
	}
	return res
}

// handoffIDFromSetResponse extracts the "id" field from set_session_handoff's
// response JSON.
func handoffIDFromSetResponse(t *testing.T, r *mcpmsg.CallToolResult) string {
	t.Helper()
	var view map[string]json.RawMessage
	if err := json.Unmarshal([]byte(resultText(r)), &view); err != nil {
		t.Fatalf("unmarshal set_session_handoff response: %v", err)
	}
	var id string
	if err := json.Unmarshal(view["id"], &id); err != nil {
		t.Fatalf("parse handoff id: %v", err)
	}
	return id
}

// TestHandoffFullSequence_BodySurvivesResolve_SQLite is U8's acceptance
// criterion (F1 / Category R, 2026-08-20-mcp-surface-spec.md): the mandated
// session-start sequence (mcpInstructions' "## Session start" line)
// resolves a pending handoff before any client is told to read its body via
// the wayneblacktea://session/handoff/latest resource. Before this fix,
// LatestHandoff's WHERE resolved_at IS NULL filter made that body
// unreachable — reading the resource right after resolve_handoff returned
// {"handoff_present":false} even though the row still existed.
//
// Exercises the LITERAL sequence from the spec: set_session_handoff ->
// get_today_context -> resolve_handoff -> read the resource. get_today_context
// is a bystander here (it reads via LatestHandoff for its own pending_handoff
// flag, deliberately unchanged by this fix — see handleResourceHandoffLatest's
// doc comment) but is included for fidelity to the real-world call order and
// to confirm it introduces no side effect that would mask the bug.
func TestHandoffFullSequence_BodySurvivesResolve_SQLite(t *testing.T) {
	s := newTestResourceServer(t)

	setR := callSetSessionHandoff(t, s, map[string]any{
		"intent": "finish the U8 fix and write its test",
	})
	if setR.IsError {
		t.Fatalf("set_session_handoff failed: %s", resultText(setR))
	}
	handoffID := handoffIDFromSetResponse(t, setR)

	todayReq := mcpmsg.CallToolRequest{}
	todayR, err := s.handleGetTodayContext(context.Background(), todayReq)
	if err != nil {
		t.Fatalf("handleGetTodayContext error: %v", err)
	}
	if todayR.IsError {
		t.Fatalf("get_today_context failed: %s", resultText(todayR))
	}

	resolveR := callResolveHandoff(t, s, handoffID)
	if resolveR.IsError {
		t.Fatalf("resolve_handoff failed: %s", resultText(resolveR))
	}

	contents, err := s.handleResourceHandoffLatest(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceHandoffLatest: %v", err)
	}
	var got handoffResource
	parseResourceJSON(t, contents, &got)

	if !got.HandoffPresent {
		t.Fatal("handoff_present = false after resolve_handoff — the bad case: the body became " +
			"unreachable the moment the mandated session-start sequence resolved it")
	}
	if got.Intent == "" {
		t.Error("intent is empty — the body did not survive resolve_handoff")
	}
	if !got.Resolved {
		t.Error("resolved = false, want true — resolve_handoff was called on this exact handoff")
	}
	if got.ID == nil || got.ID.String() != handoffID {
		t.Errorf("returned handoff id = %v, want the one just resolved (%s)", got.ID, handoffID)
	}
}

// TestHandoffFullSequence_BodySurvivesResolve_Postgres is the Postgres half
// of TestHandoffFullSequence_BodySurvivesResolve_SQLite (backend-security-
// design.md §6.5: every PG-vs-SQLite-differing code path gets both a SQLite
// test and a real testcontainers PG test). Only wires s.session — the
// get_today_context bystander step needs every other store wired too, which
// is orthogonal to what this test proves (get_today_context's own read path
// is unchanged by U8, see handleResourceHandoffLatest's doc comment), so
// this variant exercises the minimal essential sequence: set -> resolve ->
// resource-read.
func TestHandoffFullSequence_BodySurvivesResolve_Postgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	wsID := uuid.New() // fresh, isolated workspace
	s := &Server{session: session.NewStore(mcpPlanTestPgPool, &wsID)}

	setR := callSetSessionHandoff(t, s, map[string]any{
		"intent": "finish the U8 fix and write its test",
	})
	if setR.IsError {
		t.Fatalf("set_session_handoff failed: %s", resultText(setR))
	}
	handoffID := handoffIDFromSetResponse(t, setR)

	resolveR := callResolveHandoff(t, s, handoffID)
	if resolveR.IsError {
		t.Fatalf("resolve_handoff failed: %s", resultText(resolveR))
	}

	contents, err := s.handleResourceHandoffLatest(context.Background(), mcpmsg.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("handleResourceHandoffLatest: %v", err)
	}
	var got handoffResource
	parseResourceJSON(t, contents, &got)

	if !got.HandoffPresent {
		t.Fatal("handoff_present = false after resolve_handoff — the bad case: the body became " +
			"unreachable the moment the mandated session-start sequence resolved it")
	}
	if got.Intent == "" {
		t.Error("intent is empty — the body did not survive resolve_handoff")
	}
	if !got.Resolved {
		t.Error("resolved = false, want true — resolve_handoff was called on this exact handoff")
	}
	if got.ID == nil || got.ID.String() != handoffID {
		t.Errorf("returned handoff id = %v, want the one just resolved (%s)", got.ID, handoffID)
	}
}
