package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// PR #152 round 8 Major M-R8-1 / m-R8-2: a retry against a TERMINAL outcome
// that repeats the same result/notes/metrics but supplies a genuinely new
// session_id must fall through to the supersede branch (M-R8-1's fix to
// isIdempotentReplay, internal/outcome/lifecycle.go) — not be misclassified
// as an idempotent replay — AND the new row's work_session<->outcome link
// must actually be set via SetOutcomeLink (m-R8-2). Before the fix,
// isIdempotentReplay ignored WorkSessionID entirely, so this exact call was
// treated as a no-op replay: no new row, and — because
// ActionReplayedIdempotent is in handleRecordOutcome's SetOutcomeLink/
// atomize skip list (tools_outcome.go) — SetOutcomeLink never even ran,
// silently losing the link with zero signal to the caller.
// ---------------------------------------------------------------------------

// mustUnmarshalOutcomeResult decodes an MCP record_outcome response body,
// failing the test immediately on error. Extracted so the terminal-retry
// test below stays under the gocyclo threshold.
func mustUnmarshalOutcomeResult(t *testing.T, r *mcpmsg.CallToolResult) outcome.Outcome {
	t.Helper()
	if r.IsError {
		t.Fatalf("record_outcome returned an error: %s", resultText(r))
	}
	var o outcome.Outcome
	if err := json.Unmarshal([]byte(resultText(r)), &o); err != nil {
		t.Fatalf("unmarshal outcome: %v", err)
	}
	return o
}

// TestHandleRecordOutcome_TerminalRetry_NewSessionID_SupersedesAndLinksSession
// runs end-to-end through the real record_outcome MCP tool against a real
// SQLite-backed server: record a terminal outcome with NO session linked,
// then retry with the SAME result/notes/metrics but a NEW session_id.
func TestHandleRecordOutcome_TerminalRetry_NewSessionID_SupersedesAndLinksSession(t *testing.T) {
	s := newTestWorkSessionServer(t)
	entityID := uuid.New()

	first := mustUnmarshalOutcomeResult(t, callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      finalResultSuccess,
		"notes":       "shipped fine",
	}))
	if first.WorkSessionID != nil {
		t.Fatalf("precondition failed: first outcome already has a session link: %v", first.WorkSessionID)
	}

	startR := callStartWork(t, s, map[string]any{"repo_name": "terminal-retry-repo", "title": "t", "goal": "g"})
	sessID := startSessionID(t, startR)

	second := mustUnmarshalOutcomeResult(t, callRecordOutcome(t, s, map[string]any{
		"entity_type": entityTypeTask,
		"entity_id":   entityID.String(),
		"result":      finalResultSuccess,
		"notes":       "shipped fine",
		"session_id":  sessID,
	}))

	if second.ID == first.ID {
		t.Fatal("a retry carrying a genuinely NEW session_id must create a NEW row (M-R8-1 regression), not reuse the first")
	}
	if second.SupersedesID == nil || *second.SupersedesID != first.ID {
		t.Errorf("SupersedesID = %v, want %s", second.SupersedesID, first.ID)
	}
	if second.WorkSessionID == nil || second.WorkSessionID.String() != sessID {
		t.Errorf("new row's WorkSessionID = %v, want %s", second.WorkSessionID, sessID)
	}

	sessIDParsed, err := uuid.Parse(sessID)
	if err != nil {
		t.Fatalf("parse session id: %v", err)
	}
	sess, err := s.workSession.GetByID(context.Background(), s.workspaceUUIDVal(), sessIDParsed)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if sess.OutcomeID == nil || *sess.OutcomeID != second.ID {
		t.Errorf("SetOutcomeLink did not fire (m-R8-2 regression): session.outcome_id = %v, want %s", sess.OutcomeID, second.ID)
	}

	// The prior row must remain untouched — supersede is explicit, never a
	// silent overwrite.
	prior, err := s.outcome.GetOutcomeByID(context.Background(), first.ID, s.workspaceUUID())
	if err != nil {
		t.Fatalf("GetOutcomeByID(first): %v", err)
	}
	if prior.WorkSessionID != nil {
		t.Errorf("prior row's WorkSessionID was mutated by the supersede call: %v", prior.WorkSessionID)
	}
}
