package mcp

import (
	"context"
	"strings"
	"testing"

	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// --- U15: MCP audit records get a per-client actor identity, not a shared
// per-process one (2026-08-20-mcp-surface-spec.md). ---

// callLogDecisionCtx is callLogDecision (tools_decision_test.go) with a
// caller-supplied context, mirroring callDeleteTaskCtx
// (tools_gtd_delete_test.go) so tests can simulate a specific (or absent)
// MCP client session.
func callLogDecisionCtx(t *testing.T, ctx context.Context, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleLogDecision(ctx, req)
	if err != nil {
		t.Fatalf("handleLogDecision returned unexpected Go error: %v", err)
	}
	return result
}

func logDecisionArgs(title string) map[string]any {
	return map[string]any{
		"title":     title,
		"context":   "why we needed this",
		"decision":  "did the thing",
		"rationale": "because reasons",
	}
}

// TestHandleLogDecision_ActorSessionID_DiffersAcrossSessions is U15's bad
// case ①: two different MCP client sessions each call log_decision once.
// Before this fix both rows shared the same s.sessionID (per-process,
// conflating every client); after, each row's actor_session_id matches ITS
// OWN calling session and the two are unequal.
func TestHandleLogDecision_ActorSessionID_DiffersAcrossSessions(t *testing.T) {
	s := newTestWorkSessionServer(t)

	ctxA := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "decision-session-A"})
	ctxB := s.MCPServer().WithContext(context.Background(), fakeClientSession{id: "decision-session-B"})

	rA := callLogDecisionCtx(t, ctxA, s, logDecisionArgs("U15 probe decision A"))
	if rA.IsError {
		t.Fatalf("session A log_decision failed: %s", resultText(rA))
	}
	rB := callLogDecisionCtx(t, ctxB, s, logDecisionArgs("U15 probe decision B"))
	if rB.IsError {
		t.Fatalf("session B log_decision failed: %s", resultText(rB))
	}

	rows, err := s.decision.All(context.Background(), 10)
	if err != nil {
		t.Fatalf("decision.All: %v", err)
	}
	actorByTitle := map[string]string{}
	for _, d := range rows {
		if d.Title == "U15 probe decision A" || d.Title == "U15 probe decision B" {
			actorByTitle[d.Title] = d.ActorSessionID.String
		}
	}
	actorA, okA := actorByTitle["U15 probe decision A"]
	actorB, okB := actorByTitle["U15 probe decision B"]
	if !okA || !okB {
		t.Fatalf("expected both probe decisions to be persisted, got actorByTitle=%v", actorByTitle)
	}
	if actorA != "decision-session-A" {
		t.Errorf("session A row actor_session_id = %q, want %q", actorA, "decision-session-A")
	}
	if actorB != "decision-session-B" {
		t.Errorf("session B row actor_session_id = %q, want %q", actorB, "decision-session-B")
	}
	if actorA == actorB {
		t.Errorf("two different MCP sessions produced the SAME actor_session_id %q — sessions are being conflated", actorA)
	}
}

// TestHandleLogDecision_ActorSessionID_NoSessionFallsBackToProcessID is U15's
// bad case ②: a direct handler call whose ctx carries no tracked MCP client
// session (e.g. a stdio transport with no session concept, or a test calling
// the handler directly). auditSessionID's fallback means the persisted
// actor_session_id is s.sessionID — NOT empty. An empty value would read as
// "the write path failed to record who did this" rather than "this call had
// no per-client session to report".
func TestHandleLogDecision_ActorSessionID_NoSessionFallsBackToProcessID(t *testing.T) {
	s := newTestWorkSessionServer(t)

	r := callLogDecisionCtx(t, context.Background(), s, logDecisionArgs("U15 probe decision no-session"))
	if r.IsError {
		t.Fatalf("log_decision failed: %s", resultText(r))
	}

	rows, err := s.decision.All(context.Background(), 10)
	if err != nil {
		t.Fatalf("decision.All: %v", err)
	}
	var found bool
	for _, d := range rows {
		if d.Title != "U15 probe decision no-session" {
			continue
		}
		found = true
		if !d.ActorSessionID.Valid || d.ActorSessionID.String == "" {
			t.Errorf("actor_session_id must not be empty/NULL for an untracked-session call, got %+v", d.ActorSessionID)
		}
		if d.ActorSessionID.String != s.sessionID {
			t.Errorf("actor_session_id = %q, want fallback to s.sessionID = %q", d.ActorSessionID.String, s.sessionID)
		}
	}
	if !found {
		t.Fatal("expected probe decision to be persisted")
	}
}

// TestDecisionLogParamsLiteralsSetActorSessionID is U15's structural gate.
//
// It scans every non-test .go file in this package for `decision.LogParams{`
// literals and requires each non-empty one to set ActorSessionID somewhere
// inside its braces. This is the only mechanism that stops an 8th
// decisions-table write point being added later without an actor identity —
// the same shape of gap this dispatch closed for the 7 that already existed.
// Mirrors TestNoRawErrorReachesToolResult's read-the-package-dir-as-text
// approach (tool_errors_test.go) rather than AST parsing: the property being
// enforced is syntactic ("this field is set inside this literal"), which is
// exactly what a reviewer checks by eye.
//
// Genuinely empty literals (`decision.LogParams{}`) are exempt — they only
// appear in tools_proposal.go's decodeDecisionParams as early-return zero
// values on a validation failure path (guarded by a returned errMsg) and are
// never persisted.
func TestDecisionLogParamsLiteralsSetActorSessionID(t *testing.T) {
	files := goSourceFilesInPackageDir(t)
	const marker = "decision.LogParams{"
	var violations []string

	for name, body := range files {
		searchFrom := 0
		for {
			rel := strings.Index(body[searchFrom:], marker)
			if rel < 0 {
				break
			}
			start := searchFrom + rel

			// Skip occurrences inside a `//` comment (e.g. a doc comment that
			// names this literal by example, as several in this package do) —
			// mirrors TestNoRawErrorReachesToolResult's line-comment skip
			// (tool_errors_test.go). lineStart..start is searched for "//"
			// rather than requiring the marker to START the line, since most
			// real occurrences are mid-line ("dp, errMsg := decision.LogParams{").
			lineStart := strings.LastIndexByte(body[:start], '\n') + 1
			if strings.Contains(body[lineStart:start], "//") {
				searchFrom = start + len(marker)
				continue
			}

			openBrace := start + len(marker) - 1

			depth := 0
			end := -1
			for p := openBrace; p < len(body); p++ {
				switch body[p] {
				case '{':
					depth++
				case '}':
					depth--
					if depth == 0 {
						end = p
					}
				}
				if end != -1 {
					break
				}
			}
			if end == -1 {
				t.Fatalf("%s: unterminated decision.LogParams{ literal starting at byte offset %d", name, start)
			}

			content := body[openBrace+1 : end]
			if strings.TrimSpace(content) != "" && !strings.Contains(content, "ActorSessionID") {
				line := strings.Count(body[:start], "\n") + 1
				violations = append(violations, name+":"+itoa(line))
			}

			searchFrom = end + 1
		}
	}

	if len(violations) > 0 {
		t.Errorf("%d decision.LogParams{ literal(s) do not set ActorSessionID — every decisions-table "+
			"write path MUST stamp the calling MCP session via s.auditSessionID(ctx) (U15, "+
			"backend-security-design.md §2 provenance integrity):", len(violations))
		for _, v := range violations {
			t.Error("  " + v)
		}
	}
}
