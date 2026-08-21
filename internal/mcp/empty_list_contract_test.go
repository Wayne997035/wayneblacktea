package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callGetDueReviews invokes get_due_reviews. handleGetDueReviews takes a raw
// mcp.CallToolRequest (not a seam-typed Args struct — it ignores all
// arguments), so this mirrors callListDecisions/callAddKnowledge's direct-call
// style rather than the seam-routed callTool[T] helper.
func callGetDueReviews(t *testing.T, s *Server) *mcpmsg.CallToolResult {
	t.Helper()
	result, err := s.handleGetDueReviews(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleGetDueReviews returned unexpected Go error: %v", err)
	}
	return result
}

// callListKnowledgeContract invokes list_knowledge with the given args.
// Named with a Contract suffix because tools_knowledge_test.go may grow its
// own callListKnowledge helper independently; this file's helpers are scoped
// to the empty-list contract table below.
func callListKnowledgeContract(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleListKnowledge(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListKnowledge returned unexpected Go error: %v", err)
	}
	return result
}

// callListPendingProposalsContract invokes list_pending_proposals.
func callListPendingProposalsContract(t *testing.T, s *Server) *mcpmsg.CallToolResult {
	t.Helper()
	result, err := s.handleListPendingProposals(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleListPendingProposals returned unexpected Go error: %v", err)
	}
	return result
}

// emptyListContractCase is one row of TestEmptyListContract_MCP_SQLite: one
// MCP tool, invoked against a freshly opened empty SQLite database, checked
// for the "0 rows -> JSON [] never null" contract.
//
// Exactly one of wantBareArray / wantField is set per row:
//   - wantBareArray: the tool's entire response text (trimmed) must equal "[]".
//   - wantField: the response is a JSON object with an embedded list field;
//     the raw text must contain `"<wantField>":[]` and must NOT contain
//     `"<wantField>":null`.
//
// Both check styles compare the raw response TEXT, never a decoded slice's
// len() — json.Unmarshal("null", &s) and json.Unmarshal("[]", &s) both leave a
// zero-length slice, so len() cannot tell the two apart on the wire (this is
// the exact gap that let the DueReviews SQLite bug ship — see
// internal/storage/sqlite/learning_test.go's
// TestLearningStore_EmptyTable, which predates this file and only asserts
// len(reviews)==0).
type emptyListContractCase struct {
	name          string
	seed          func(t *testing.T, s *Server) // optional fixture setup before run
	run           func(t *testing.T, s *Server) *mcpmsg.CallToolResult
	wantBareArray bool
	wantField     string
}

// TestEmptyListContract_MCP_SQLite is the single table-driven contract test
// scanning every MCP list-returning tool this repo currently exposes for the
// "0 rows -> JSON [] never null" contract, backed by a real, empty SQLite
// database (:memory:-backed via newTestWorkSessionServer) rather than fakes.
//
// Real stores are used deliberately: get_due_reviews, list_knowledge, and
// list_pending_proposals have NO nil-guard of their own in their MCP handler
// (tools_learning.go / tools_knowledge.go / tools_proposal.go) — they
// serialize whatever the store layer returns. A fake store seeded with a nil
// field would either mask a real store-layer bug (if the fake happened to
// pre-seed []T{} instead of nil) or fail for the wrong reason entirely (the
// fake's own zero-value nil field, not the production store code). Only a
// real, empty store exercises the actual production code path end to end.
//
// New list-returning MCP tool -> add one row here. That is the entire point
// of this table: a future omission is caught by "this tool has no row here",
// not by a fresh manual review pass. Per-endpoint ad hoc tests are not
// sufficient — they only cover the endpoints someone remembered to write one
// for, and a missing test is indistinguishable from a passing one.
//
// Dual-backend: the Postgres half of this same contract, for the tools that
// differ by backend (get_due_reviews, list_pending_proposals), lives in
// tools_learning_pg_test.go / tools_proposal_pg_contract_test.go, reusing the
// existing mcpPlanTestPgPool testcontainers pool from tools_plan_pg_test.go.
func TestEmptyListContract_MCP_SQLite(t *testing.T) {
	cases := []emptyListContractCase{
		{
			// THE FIX under test in this PR: internal/storage/sqlite/learning.go
			// DueReviews previously returned `var out []learning.DueReview`
			// unguarded (SQLite-only bug; internal/learning/store.go's Postgres
			// DueReviews already used make([]DueReview, 0, len(rows))).
			name:          "get_due_reviews",
			run:           callGetDueReviews,
			wantBareArray: true,
		},
		{
			name:          "list_goals",
			run:           func(t *testing.T, s *Server) *mcpmsg.CallToolResult { return callListGoals(t, s, map[string]any{}) },
			wantBareArray: true,
		},
		{
			name:          "list_projects",
			run:           func(t *testing.T, s *Server) *mcpmsg.CallToolResult { return callListProjects(t, s, map[string]any{}) },
			wantBareArray: true,
		},
		{
			name:          "list_decisions",
			run:           func(t *testing.T, s *Server) *mcpmsg.CallToolResult { return callListDecisions(t, s, map[string]any{}) },
			wantBareArray: true,
		},
		{
			name: "list_knowledge",
			run: func(t *testing.T, s *Server) *mcpmsg.CallToolResult {
				return callListKnowledgeContract(t, s, map[string]any{})
			},
			wantBareArray: true,
		},
		{
			// Second gap found while building this table (beyond the assigned
			// DueReviews bug): handleListPendingProposals (tools_proposal.go)
			// passes the store's []db.PendingProposal straight to jsonText with
			// no guard of its own, and neither proposal.Store.ListPending (PG,
			// internal/proposal/store.go) nor sqlite.ProposalStore.ListPending
			// (internal/storage/sqlite/proposal.go) guarded nil -> [] either —
			// unlike ListAll, whose two callers (HTTP ListProposals handler,
			// watchdog's internal iteration) each already tolerate a nil slice.
			// Fixed in the same store-layer pattern as DueReviews (this PR).
			name:          "list_pending_proposals",
			run:           callListPendingProposalsContract,
			wantBareArray: true,
		},
		{
			// Nested field: response is {"tasks": [...], "summary":..., ...},
			// not a bare array. summary=false selects the branch that includes
			// full Task objects (see TestListTasks_SummaryFalse_EmptyDB_
			// ReturnsEmptyArrayNotNull in tools_gtd_test.go, which already
			// covers this specific row — duplicated here for table completeness
			// per this file's "one place scans every endpoint" goal).
			name: "list_tasks",
			run: func(t *testing.T, s *Server) *mcpmsg.CallToolResult {
				return callListTasks(t, s, map[string]any{"summary": false})
			},
			wantField: "tasks",
		},
		func() emptyListContractCase {
			// Nested field: response is {"project":..., "recent_decisions": []}.
			// Requires a project to exist (get_project 404s on unknown names),
			// but zero decisions have been logged against it. Name generated
			// once here (not from t.Name(), which contains "/" for subtests
			// and is not a valid project name slug — see seedProject callers
			// elsewhere in this package for the "proj-"+uuid convention).
			projectName := "proj-" + uuid.NewString()[:8]
			return emptyListContractCase{
				name: "get_project",
				seed: func(t *testing.T, s *Server) { seedProject(t, s, projectName) },
				run: func(t *testing.T, s *Server) *mcpmsg.CallToolResult {
					return callGetProject(t, s, map[string]any{"name": projectName})
				},
				wantField: "recent_decisions",
			}
		}(),
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestWorkSessionServer(t)
			if tc.seed != nil {
				tc.seed(t, s)
			}
			r := tc.run(t, s)
			if r.IsError {
				t.Fatalf("%s: unexpected tool error: %s", tc.name, resultText(r))
			}
			raw := resultText(r)

			if tc.wantField != "" {
				wantOK := `"` + tc.wantField + `":[]`
				wantBad := `"` + tc.wantField + `":null`
				if !strings.Contains(raw, wantOK) {
					t.Errorf("%s: expected raw JSON to contain %s, got: %s", tc.name, wantOK, raw)
				}
				if strings.Contains(raw, wantBad) {
					t.Errorf("%s: %q must never serialize as null, got: %s", tc.name, tc.wantField, raw)
				}
				return
			}

			if got := strings.TrimSpace(raw); got != "[]" {
				t.Errorf("%s: raw body = %q, want exactly %q (nil slice must not serialize to JSON null)", tc.name, got, "[]")
			}
		})
	}
}
