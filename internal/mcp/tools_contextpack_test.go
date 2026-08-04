package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/contextpack"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// newContextPackTestServer wires a Server whose contextAssembler is backed
// by the no-op StoreIface fakes in tools_contextpack_fakes_test.go. Every
// store call retrieve() makes returns an empty/not-found result, so
// Assemble() always returns a valid, empty Pack — these handler tests only
// assert on the shape of the emitted JSON envelope.
func newContextPackTestServer() *Server {
	// Every noop*Store{} literal below is a non-nil interface value, so
	// NewAssembler's nil-guard can never fire here — panic (rather than
	// silently swallowing) if that assumption ever breaks.
	assembler, err := contextpack.NewAssembler(
		noopGTDStore{}, noopDecisionStore{}, noopKnowledgeStore{}, noopAtomStore{},
		noopProceduralStore{}, noopSkillStore{}, noopOutcomeStore{}, noopReflectionStore{},
		noopBehaviorRuleStore{}, noopSessionStore{}, noopWorkSessionStore{},
	)
	if err != nil {
		panic(err)
	}
	return &Server{contextAssembler: assembler}
}

func callAssembleContext(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	r, err := s.handleAssembleContext(context.Background(), req)
	if err != nil {
		t.Fatalf("handleAssembleContext returned unexpected Go error: %v", err)
	}
	return r
}

func TestHandleAssembleContext_MissingObjective(t *testing.T) {
	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{})
	if !r.IsError {
		t.Fatalf("expected IsError=true for missing objective")
	}
	if !strings.Contains(resultText(r), "objective") {
		t.Errorf("error should mention objective, got: %s", resultText(r))
	}
}

func TestHandleAssembleContext_ObjectiveAllControlChars(t *testing.T) {
	// Stripping control chars leaves an empty string — must be treated the
	// same as a missing objective, not silently accepted.
	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{
		"objective": "\x00\x01\x02",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for objective that strips to empty")
	}
}

func TestHandleAssembleContext_ObjectiveTooLong(t *testing.T) {
	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{
		"objective": strings.Repeat("x", maxObjectiveRunes+1),
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for objective exceeding %d runes", maxObjectiveRunes)
	}
}

func TestHandleAssembleContext_PersistTrueRejected(t *testing.T) {
	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{
		"objective": "ship the assemble_context tool",
		"persist":   true,
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for persist=true")
	}
	if !strings.Contains(resultText(r), "reserved for Phase 2") {
		t.Errorf("error should mention 'reserved for Phase 2', got: %s", resultText(r))
	}
}

func TestHandleAssembleContext_NilAssembler(t *testing.T) {
	s := &Server{} // contextAssembler left nil
	r := callAssembleContext(t, s, map[string]any{
		"objective": "ship the assemble_context tool",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true when contextAssembler is nil")
	}
}

func TestHandleAssembleContext_InvalidProjectIDUUID(t *testing.T) {
	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{
		"objective":  "ship the assemble_context tool",
		"project_id": "not-a-uuid",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid project_id UUID")
	}
}

func TestHandleAssembleContext_InvalidTaskIDUUID(t *testing.T) {
	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{
		"objective": "ship the assemble_context tool",
		"task_id":   "not-a-uuid",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid task_id UUID")
	}
}

func TestHandleAssembleContext_ValidInput_TopLevelKeys(t *testing.T) {
	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{
		"objective":     "ship the assemble_context tool",
		"repo_name":     "wayneblacktea",
		"project_id":    uuid.New().String(),
		"task_id":       uuid.New().String(),
		"branch_name":   "p1-mcptool",
		"files_touched": []any{"internal/mcp/tools_contextpack.go"},
		"budget_chars":  float64(8000),
		"include_types": []any{"semantic", "bogus_type"},
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, resultText(r))
	}

	for _, key := range []string{
		"pack_id", "objective", "budget_chars", "used_chars", "items", "warnings", "omitted",
	} {
		if _, ok := body[key]; !ok {
			t.Errorf("response missing top-level key %q; body: %s", key, resultText(r))
		}
	}
	if body["pack_id"] != nil {
		t.Errorf("pack_id = %v, want null (persist=false, Phase 2 not implemented)", body["pack_id"])
	}
	if body["objective"] != "ship the assemble_context tool" {
		t.Errorf("objective = %v, want echoed request objective", body["objective"])
	}
	if body["budget_chars"] != float64(8000) {
		t.Errorf("budget_chars = %v, want 8000", body["budget_chars"])
	}
}

// fakeDecisionStoreDistinguishesAllFromScoped is a decision.StoreIface fake
// used only by TestHandleAssembleContext_UnscopedRequestReachesDecisionAll:
// it returns a distinct sentinel decision from All() while every scoped
// method (ByRepo/ByProject/ByTask, inherited as no-ops from
// noopDecisionStore) stays empty. That asymmetry is what lets the test prove
// -- through the real MCP handler path, not retrieval_test.go's
// package-internal fakes -- which of the two branches an objective-only
// assemble_context call actually reaches.
type fakeDecisionStoreDistinguishesAllFromScoped struct {
	noopDecisionStore
	all []db.Decision
}

func (f fakeDecisionStoreDistinguishesAllFromScoped) All(context.Context, int32) ([]db.Decision, error) {
	return f.all, nil
}

// TestHandleAssembleContext_UnscopedRequestReachesDecisionAll locks in the
// behavior that ports.go/retrieval.go's doc comments previously
// mis-described as unreachable: assemble_context's repo_name/project_id/
// task_id are all mcp.Optional (only objective is mcp.Required, see
// tools_contextpack.go), so a caller that supplies nothing but objective —
// the tool's minimum legal call — reaches DecisionReadPort.All, not an empty
// result. Before the doc-comment fix this branch had no MCP-layer test at
// all; retrieval_test.go's TestRetrieveDecisionsUnscopedFallsBackToAll only
// exercised contextpack.Assembler.retrieve directly and said nothing about
// whether any real caller could produce that Request shape.
//
// Mutation self-proof: temporarily reverting retrieval.go's fallback (e.g.
// changing the `req.RepoName == "" && req.ProjectID == nil && req.TaskID ==
// nil` guard to `false`, so retrieveDecisions always takes the scoped path)
// makes this test fail with "expected decision ... to appear in an unscoped
// assemble_context response" because fakeDecisionStoreDistinguishesAllFromScoped's
// ByRepo/ByProject/ByTask (inherited no-ops) return nothing. That confirms
// the test is actually exercising the fallback branch, not passing
// vacuously.
func TestHandleAssembleContext_UnscopedRequestReachesDecisionAll(t *testing.T) {
	allID := uuid.New()
	assembler, err := contextpack.NewAssembler(
		noopGTDStore{},
		fakeDecisionStoreDistinguishesAllFromScoped{
			all: []db.Decision{
				{
					ID: allID, Title: "d", Decision: "do it",
					CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
				},
			},
		},
		noopKnowledgeStore{}, noopAtomStore{}, noopProceduralStore{}, noopSkillStore{},
		noopOutcomeStore{}, noopReflectionStore{}, noopBehaviorRuleStore{}, noopSessionStore{},
		noopWorkSessionStore{},
	)
	if err != nil {
		t.Fatalf("NewAssembler: %v", err)
	}
	s := &Server{contextAssembler: assembler}

	// Only "objective" set — repo_name/project_id/task_id all omitted. This
	// is a legal call: the tool schema does not require any of them.
	r := callAssembleContext(t, s, map[string]any{
		"objective": "what have we decided lately",
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(resultText(r)), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v\nbody: %s", err, resultText(r))
	}
	items, _ := body["items"].([]any)
	found := false
	for _, raw := range items {
		it, _ := raw.(map[string]any)
		if it["type"] == "decision" && it["id"] == allID.String() {
			found = true
		}
	}
	if !found {
		t.Errorf(
			"expected decision %s from decision.All to appear in an unscoped (objective-only) "+
				"assemble_context response; items: %+v", allID, items,
		)
	}
}

func TestHandleAssembleContext_BudgetCharsClamped(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  float64
	}{
		{"below_min_clamped_up", float64(1), 1000},
		{"above_max_clamped_down", float64(999999), 50000},
		{"unset_uses_default", nil, 12000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newContextPackTestServer()
			args := map[string]any{"objective": "ship the assemble_context tool"}
			if tc.input != nil {
				args["budget_chars"] = tc.input
			}
			r := callAssembleContext(t, s, args)
			if r.IsError {
				t.Fatalf("unexpected error: %s", resultText(r))
			}
			var body map[string]any
			if err := json.Unmarshal([]byte(resultText(r)), &body); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			if body["budget_chars"] != tc.want {
				t.Errorf("budget_chars = %v, want %v", body["budget_chars"], tc.want)
			}
		})
	}
}

func TestHandleAssembleContext_FilesTouchedCapAndLengthGuard(t *testing.T) {
	// 51 entries, plus one oversized entry — the request must still succeed
	// (excess/oversized entries are dropped, not rejected).
	files := make([]any, 0, 52)
	for range 51 {
		files = append(files, "file.go")
	}
	files = append(files, strings.Repeat("x", maxFileTouchedRune+1))

	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{
		"objective":     "ship the assemble_context tool",
		"files_touched": files,
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
}

func TestHandleAssembleContext_IncludeTypesCapAndLengthGuard(t *testing.T) {
	// 40 entries (over maxIncludeTypes=32) plus one oversized entry (over
	// maxIncludeTypesRunes=200) — adversarial LLM-supplied array input must
	// be bounded, not rejected outright (backend-security-design.md §2.1).
	types := make([]any, 0, 41)
	for range 40 {
		types = append(types, "semantic")
	}
	types = append(types, strings.Repeat("x", maxIncludeTypesRunes+1))

	s := newContextPackTestServer()
	r := callAssembleContext(t, s, map[string]any{
		"objective":     "ship the assemble_context tool",
		"include_types": types,
	})
	if r.IsError {
		t.Fatalf("unexpected error: %s", resultText(r))
	}
}

func TestStringArrayArg_CapsCount(t *testing.T) {
	args := map[string]any{"key": []any{"a", "b", "c", "d"}}
	got := stringArrayArg(args, "key", 2, 0)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("stringArrayArg with maxCount=2 = %v, want [a b]", got)
	}
}

func TestStringArrayArg_DropsOversizedElementsNotWholeRequest(t *testing.T) {
	args := map[string]any{"key": []any{"ok", strings.Repeat("x", 10)}}
	got := stringArrayArg(args, "key", 0, 5)
	if len(got) != 1 || got[0] != "ok" {
		t.Errorf("stringArrayArg with maxRunes=5 = %v, want [ok] (oversized element dropped, not the whole array rejected)", got)
	}
}

func TestFilterKnownContextPackTypes(t *testing.T) {
	got := filterKnownContextPackTypes([]string{"semantic", "bogus", "outcomes", ""})
	want := []string{"semantic", "outcomes"}
	if len(got) != len(want) {
		t.Fatalf("filterKnownContextPackTypes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("filterKnownContextPackTypes[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if filterKnownContextPackTypes(nil) != nil {
		t.Error("filterKnownContextPackTypes(nil) should return nil")
	}
}

func TestClampBudgetChars(t *testing.T) {
	cases := []struct {
		in   int32
		want int
	}{
		{0, defaultBudgetChars},
		{-5, defaultBudgetChars},
		{500, minBudgetChars},
		{60000, maxBudgetChars},
		{20000, 20000},
	}
	for _, tc := range cases {
		if got := clampBudgetChars(tc.in); got != tc.want {
			t.Errorf("clampBudgetChars(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestStripControlChars(t *testing.T) {
	got := stripControlChars("hello\x00 world\x1b[31m")
	if strings.ContainsAny(got, "\x00\x1b") {
		t.Errorf("stripControlChars left control chars: %q", got)
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Errorf("stripControlChars dropped legitimate text: %q", got)
	}
}
