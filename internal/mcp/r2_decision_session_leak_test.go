package mcp

import (
	"context"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// --- PR160 round-2 security review, M-3: list_decisions must not leak
// another MCP client session's actor_session_id back to the caller. ---
//
// decision 91ff27d6's accepted residual risk is "a caller who already knows
// a session ID can impersonate it" — Mcp-Session-Id is client-supplied and
// server only validates its format. This bug turned that into something
// materially cheaper: ANY caller could learn a live session ID for free by
// calling a core read tool (list_decisions), no prior knowledge required.
// The fix (internal/db/models_custom.go, Decision.MarshalJSON) closes the
// read side at the type level; this test is the end-to-end proof the actual
// MCP surface, not just json.Marshal in isolation, is closed.

// callListDecisionsCtx is callListDecisions (tools_decision_test.go) with a
// caller-supplied context, mirroring callLogDecisionCtx (u15_actor_session_
// test.go) so this test can read back as a specific MCP client session.
func callListDecisionsCtx(t *testing.T, ctx context.Context, s *Server, args map[string]any) string {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleListDecisions(ctx, req)
	if err != nil {
		t.Fatalf("handleListDecisions returned unexpected Go error: %v", err)
	}
	if result.IsError {
		t.Fatalf("list_decisions failed: %s", resultText(result))
	}
	return resultText(result)
}

// TestListDecisions_DoesNotLeakOtherSessionActorSessionID pins a security
// review finding: session A writes a
// decision, session B (a different MCP client) calls list_decisions and
// must NOT be able to read session A's actor_session_id — in any form,
// anywhere in the response text — while the write-side audit trail (the DB
// row itself) is unaffected.
func TestListDecisions_DoesNotLeakOtherSessionActorSessionID(t *testing.T) {
	s := newTestWorkSessionServer(t)

	const sessionA = "mcp-session-1111-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	ctxA := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: sessionA})
	ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "mcp-session-2222-bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"})

	rA := callLogDecisionCtx(t, ctxA, s, logDecisionArgs("R2 leak probe: session A's decision"))
	if rA.IsError {
		t.Fatalf("session A log_decision failed: %s", resultText(rA))
	}

	// Bad case: session B lists decisions and must not see session A's
	// actor_session_id anywhere in the raw response text.
	listText := callListDecisionsCtx(t, ctxB, s, map[string]any{})
	if strings.Contains(listText, sessionA) {
		t.Errorf("session B's list_decisions response leaks session A's actor_session_id %q:\n%s", sessionA, listText)
	}
	if strings.Contains(listText, "actor_session_id") {
		t.Errorf("session B's list_decisions response contains the actor_session_id key at all:\n%s", listText)
	}
	if strings.Contains(listText, "confirmed_by_human") {
		t.Errorf("session B's list_decisions response contains the confirmed_by_human key (PR160 M-2):\n%s", listText)
	}

	// Positive control: the write-side audit trail is untouched — the row
	// itself still records sessionA via the Go store read path (raw-SQL
	// read-back is covered separately by internal/decision's testcontainers
	// PG + SQLite tests, backend-security-design.md §6.5).
	rows, err := s.decision.All(context.Background(), 10)
	if err != nil {
		t.Fatalf("decision.All: %v", err)
	}
	var found bool
	for _, d := range rows {
		if d.Title != "R2 leak probe: session A's decision" {
			continue
		}
		found = true
		if !d.ActorSessionID.Valid || d.ActorSessionID.String != sessionA {
			t.Errorf("audit trail broken: stored ActorSessionID = %+v, want Valid=true String=%q", d.ActorSessionID, sessionA)
		}
	}
	if !found {
		t.Fatal("expected probe decision to be persisted")
	}
}
