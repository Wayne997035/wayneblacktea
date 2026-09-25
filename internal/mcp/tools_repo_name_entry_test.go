package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// Each unreachable* store embeds its interface as nil: any method call
// panics, so a test that returns cleanly proves the handler rejected the
// input before reaching the store.
type (
	unreachableDecisionStore    struct{ decision.StoreIface }
	unreachableSessionStore     struct{ session.StoreIface }
	unreachableWorkSessionStore struct{ worksession.StoreIface }
	unreachableVisionStore      struct{ vision.StoreIface }
	unreachableProceduralStore  struct{ procedural.StoreIface }
)

// plantLegacyHandoffRepoName overwrites every session_handoffs.repo_name
// with value, bypassing the write-time rule — the shape of a row written
// before [F0925-29], which the read path must still clip and neutralise.
func plantLegacyHandoffRepoName(t *testing.T, db *wbtsqlite.DB, value string) {
	t.Helper()
	if err := db.ExecContext(context.Background(), `UPDATE session_handoffs SET repo_name = ?1`, value); err != nil {
		t.Fatalf("plant legacy handoff repo_name: %v", err)
	}
}

func newUnreachableStoreServer() *Server {
	return &Server{
		decision:    unreachableDecisionStore{},
		session:     unreachableSessionStore{},
		workSession: unreachableWorkSessionStore{},
		vision:      unreachableVisionStore{},
		procedural:  unreachableProceduralStore{},
	}
}

// TestRepoNameEntryChecks pins that every MCP tool writing a repo_name
// rejects a value breaking the workspace repo name rule with an error that
// states the rule, before any store call.
func TestRepoNameEntryChecks(t *testing.T) {
	t.Parallel() // [F0925-29]
	const bad = "workspace/.hidden"
	cases := []struct {
		name    string
		handler func(*Server, context.Context, mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error)
		args    map[string]any
	}{
		{"log_decision", (*Server).handleLogDecision, map[string]any{
			"title": "t", "context": "c", "decision": "d", "rationale": "r", "repo_name": bad,
		}},
		{"set_session_handoff", (*Server).handleSetSessionHandoff, map[string]any{
			"intent": "i", "context_summary": "c", "repo_name": bad,
		}},
		{"start_work", (*Server).handleStartWork, map[string]any{
			"repo_name": bad, "title": "t", "goal": "g",
		}},
		{"add_vision_item", (*Server).handleAddVisionItem, map[string]any{
			"title": "t", "why_blocked": "w", "repo_name": bad,
		}},
		{"record_procedure", (*Server).handleAddProcedural, map[string]any{
			"title": "t", "when_to_use": "w", "approach_md": "a", "repo_name": bad,
		}},
		{"confirm_plan", (*Server).handleConfirmPlan, map[string]any{
			"phases": `[{"title":"p1"}]`, "repo_name": bad,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := mcpmsg.CallToolRequest{}
			req.Params.Arguments = tc.args
			result, err := tc.handler(newUnreachableStoreServer(), context.Background(), req)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if !result.IsError || !strings.Contains(resultText(result), "repo_name must be") {
				t.Errorf("%s(repo_name=%q): want tool error stating the rule, got %q", tc.name, bad, resultText(result))
			}
		})
	}
}
