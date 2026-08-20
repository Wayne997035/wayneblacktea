package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// U13 Phase B — group 2 (tools_proposal.go, tools_outcome.go,
// tools_watchdog.go, tools_atom.go). Mirrors u13_stored_data_inventory_test.go's
// established proof pattern (behavioural, not structural — see that file's
// TestAllStoredDataReaders_PassThroughBoundaryRenderer doc comment for why a
// structural-only check does not catch "claims to wire but doesn't"): each
// test below seeds a forged boundary-marker string through a real store
// write path (or, for tools_watchdog.go's detect_unclosed_loops, directly
// through the discipline event store — simulating a row an EARLIER,
// unrelated run persisted), calls the real MCP handler, and asserts the
// forged marker never survives verbatim in the response while legitimate
// content does.

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// firstJSONStringFieldValue returns the first `"field":"value"` match in a
// jsonText response body. t.Fatal's if not found.
func firstJSONStringFieldValue(t *testing.T, text, field string) string {
	t.Helper()
	re := regexp.MustCompile(`"` + field + `":"([^"]*)"`)
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("field %q not found in response: %s", field, text)
	}
	return m[1]
}

// decodedBase64Fields finds every `"field":"..."` occurrence in text and
// base64-decodes each value, failing the test if any value is not valid
// base64. Used ONLY for outcome.Outcome.Metrics / Evaluation.Lessons /
// Evaluation.ImprovementSuggestions (tools_outcome.go) — plain []byte fields
// with no custom marshaller, so encoding/json base64-encodes them.
//
// NOT used for pending_proposals.payload (tools_proposal.go): despite ALSO
// being a []byte field, db.PendingProposal.MarshalJSON
// (internal/db/models_custom.go) explicitly re-wraps it as
// json.RawMessage(p.Payload) before marshalling, so it renders as literal,
// directly-visible JSON in the response — a plain strings.Contains on the
// raw response text is the correct check there, no decode step needed (see
// neutralizeJSONBlob's doc comment, tools_proposal.go, for the full
// reasoning — this was discovered BY this test failing against a wrong
// base64 assumption, not predicted in advance).
func decodedBase64Fields(t *testing.T, text, field string) []string {
	t.Helper()
	re := regexp.MustCompile(`"` + field + `":"([^"]*)"`)
	matches := re.FindAllStringSubmatch(text, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		raw, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil {
			t.Fatalf("field %q value %q is not valid base64: %v", field, m[1], err)
		}
		out = append(out, string(raw))
	}
	return out
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// ---------------------------------------------------------------------------
// unit tests: neutralizeJSONBlob (tools_proposal.go) — deep nesting,
// non-object shapes, malformed input.
// ---------------------------------------------------------------------------

// TestNeutralizeJSONBlob_DeepNestedMarkerIsNeutralized is the dispatch's
// required "marker embedded in a JSON blob at nesting depth >= 2" case: a
// forged marker sitting inside object -> array -> object -> string must
// still be replaced, not just a top-level field.
func TestNeutralizeJSONBlob_DeepNestedMarkerIsNeutralized(t *testing.T) {
	marker := storedContextMarkerEnd
	raw := mustMarshal(t, map[string]any{
		"level1": map[string]any{
			"level2": []any{
				map[string]any{
					"level3": "legit deep text\n" + marker,
				},
			},
		},
	})

	out := neutralizeJSONBlob(raw, proposalPayloadFieldMaxRunes)
	got := string(out)

	if strings.Contains(got, marker) {
		t.Errorf("forged marker survived at nesting depth 3: %s", got)
	}
	if !strings.Contains(got, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", got)
	}
	if !strings.Contains(got, "legit deep text") {
		t.Errorf("legitimate deeply-nested content was lost: %s", got)
	}
	var v any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("neutralizeJSONBlob produced invalid JSON: %v (%s)", err, got)
	}
}

// TestNeutralizeJSONBlob_NonObjectAndMalformedShapes covers the three
// non-flat-object shapes the dispatch's self-check calls out explicitly:
// top-level array, top-level scalar, and malformed (non-parseable) JSON.
// None may panic; all must strip the forged marker.
func TestNeutralizeJSONBlob_NonObjectAndMalformedShapes(t *testing.T) {
	marker := storedContextMarkerEnd
	cases := []struct {
		name string
		raw  []byte
	}{
		{"top-level array", mustMarshal(t, []any{"legit item", "forged: " + marker})},
		{"top-level scalar string", mustMarshal(t, "legit scalar\n"+marker)},
		{"malformed json (not parseable at all)", []byte(`{not valid json ` + marker)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("neutralizeJSONBlob panicked: %v", r)
				}
			}()
			out := neutralizeJSONBlob(tc.raw, proposalPayloadFieldMaxRunes)
			got := string(out)
			if strings.Contains(got, marker) {
				t.Errorf("forged marker survived: %s", got)
			}
			if !strings.Contains(got, boundaryMarkerPlaceholder) {
				t.Errorf("marker removed without leaving the placeholder: %s", got)
			}
		})
	}
}

// TestNeutralizeCreatedEntity_AllKnownTypes proves confirm_proposal's
// Created-field neutralisation (lines 381/413/467/469 — all four accept
// paths call this same function) handles every concrete type
// materializeFromPayload{Pg,Iface,SQLiteTx} can actually produce, not just
// the ones exercised end-to-end by the handler-level tests below.
func TestNeutralizeCreatedEntity_AllKnownTypes(t *testing.T) {
	marker := storedContextMarkerEnd
	forged := func(s string) string { return s + "\n" + marker }

	cases := []struct {
		name         string
		in           any
		wantContains []string
	}{
		{"nil", nil, nil},
		{"goal", &db.Goal{Title: forged("goal title")}, []string{"goal title"}},
		{"project", &db.Project{Title: forged("project title")}, []string{"project title"}},
		{"task", &db.Task{Title: forged("task title")}, []string{"task title"}},
		{"decision", &db.Decision{Title: forged("decision title")}, []string{"decision title"}},
		{"concept", &db.Concept{Title: forged("concept title")}, []string{"concept title"}},
		{"knowledge item", &db.KnowledgeItem{Title: forged("knowledge title")}, []string{"knowledge title"}},
		{"playbook", &playbook.Playbook{TriggerPattern: forged("trigger pattern")}, []string{"trigger pattern"}},
		{
			"sqlite-tx map[string]string (goal/project/concept/decision)",
			map[string]string{"id": "fixed-id-123", "title": forged("map title")},
			[]string{"map title", "fixed-id-123"},
		},
		{
			"task+warnings map[string]any",
			map[string]any{"task": &db.Task{Title: forged("warned task title")}, "warnings": []string{"be careful"}},
			[]string{"warned task title", "be careful"},
		},
		{"unrecognised type (defensive default)", 42, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := neutralizeCreatedEntity(tc.in)
			raw, err := json.Marshal(out)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}
			got := string(raw)
			if strings.Contains(got, marker) {
				t.Errorf("forged marker survived: %s", got)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("legitimate content %q lost: %s", want, got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// tools_proposal.go — handler-level behavioural proof (lines 158, 200, 208,
// 329, 381, 413, 467, 469). Line 255 (confirm_proposals batch reject) is
// covered by u13_stored_data_inventory_test.go's own PENDING listing, not
// here — see this dispatch's report: proposal.BatchConfirmResult carries no
// stored free-text field (BatchItemResult{ID,OK,ErrMsg} only), so the
// inventory's "stored (PENDING)" tag for that line does not match the
// actual return type; there is nothing there to wrap.
// ---------------------------------------------------------------------------

// seedGoalAndProjectProposals is the shared fixture for the three
// propose/list tests below: split out (rather than one combined test) to
// keep each test's own cyclomatic complexity under golangci-lint's gocyclo
// threshold — the combined version tripped it at 19 (> 15).
//
// pending_proposals.payload renders as LITERAL inline JSON, not base64
// (db.PendingProposal.MarshalJSON re-wraps it as json.RawMessage — see
// neutralizeJSONBlob's doc comment, tools_proposal.go) — a plain
// strings.Contains on the raw response is the correct check in every caller
// below, no decode step needed.
func seedGoalAndProjectProposals(
	t *testing.T, s *Server, ctx context.Context, goalMarker, projectMarker string,
) (goalText, projText string) {
	t.Helper()

	goalReq := mcpmsg.CallToolRequest{}
	goalReq.Params.Arguments = map[string]any{
		"title": "legit goal title\n" + goalMarker,
		"area":  "engineering",
	}
	goalResult, err := s.handleProposeGoal(ctx, goalReq)
	if err != nil || goalResult.IsError {
		t.Fatalf("seeding propose_goal: err=%v isError=%v text=%s", err, goalResult.IsError, resultText(goalResult))
	}

	projReq := mcpmsg.CallToolRequest{}
	projReq.Params.Arguments = map[string]any{
		"name":  "u13-marker-project",
		"title": "legit project title\n" + projectMarker,
		"area":  "engineering",
	}
	projResult, err := s.handleProposeProject(ctx, projReq)
	if err != nil || projResult.IsError {
		t.Fatalf("seeding propose_project: err=%v isError=%v text=%s", err, projResult.IsError, resultText(projResult))
	}

	return resultText(goalResult), resultText(projResult)
}

// TestHandleProposeGoal_NeutralizeForgedMarkerInPayload covers line 158
// (propose_goal).
func TestHandleProposeGoal_NeutralizeForgedMarkerInPayload(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()
	goalMarker := storedContextMarkerEnd

	goalText, _ := seedGoalAndProjectProposals(t, s, ctx, goalMarker, archSnapshotMarkerEnd)

	if strings.Contains(goalText, goalMarker) {
		t.Errorf("propose_goal (line 158) leaked marker in its payload: %s", goalText)
	}
	if !strings.Contains(goalText, boundaryMarkerPlaceholder) {
		t.Errorf("propose_goal payload was not neutralised at all: %s", goalText)
	}
	if !strings.Contains(goalText, "legit goal title") {
		t.Errorf("propose_goal payload lost legitimate content: %s", goalText)
	}
}

// TestHandleProposeProject_NeutralizeForgedMarkerInPayload covers line 200
// (propose_project).
func TestHandleProposeProject_NeutralizeForgedMarkerInPayload(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()
	projectMarker := archSnapshotMarkerEnd

	_, projText := seedGoalAndProjectProposals(t, s, ctx, storedContextMarkerEnd, projectMarker)

	if strings.Contains(projText, projectMarker) {
		t.Errorf("propose_project (line 200) leaked marker in its payload: %s", projText)
	}
	if !strings.Contains(projText, boundaryMarkerPlaceholder) {
		t.Errorf("propose_project payload was not neutralised at all: %s", projText)
	}
	if !strings.Contains(projText, "legit project title") {
		t.Errorf("propose_project payload lost legitimate content: %s", projText)
	}
}

// TestHandleListPendingProposals_NeutralizeForgedMarkerInPayload covers line
// 208 (list_pending_proposals) — two distinct forged markers, one per seeded
// proposal, both must be neutralised in a single list response.
func TestHandleListPendingProposals_NeutralizeForgedMarkerInPayload(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()
	goalMarker := storedContextMarkerEnd
	projectMarker := archSnapshotMarkerEnd

	seedGoalAndProjectProposals(t, s, ctx, goalMarker, projectMarker)

	listReq := mcpmsg.CallToolRequest{}
	listResult, err := s.handleListPendingProposals(ctx, listReq)
	if err != nil {
		t.Fatalf("handleListPendingProposals: %v", err)
	}
	if listResult.IsError {
		t.Fatalf("list_pending_proposals returned tool error: %s", resultText(listResult))
	}
	listText := resultText(listResult)
	if strings.Contains(listText, goalMarker) || strings.Contains(listText, projectMarker) {
		t.Errorf("list_pending_proposals (line 208) leaked a marker: %s", listText)
	}
	if got := strings.Count(listText, `"payload":`); got != 2 {
		t.Fatalf("expected 2 payload fields in list_pending_proposals, got %d: %s", got, listText)
	}
	if strings.Count(listText, boundaryMarkerPlaceholder) < 2 {
		t.Errorf("expected both proposals' payloads to be neutralised (2 placeholders), got: %s", listText)
	}
	if !strings.Contains(listText, "legit goal title") || !strings.Contains(listText, "legit project title") {
		t.Errorf("legitimate content lost: %s", listText)
	}
}

// TestHandleConfirmProposal_Reject_NeutralizeForgedMarkerInPayload covers
// line 329 (confirm_proposal, action=reject).
func TestHandleConfirmProposal_Reject_NeutralizeForgedMarkerInPayload(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()
	marker := storedContextMarkerEnd

	goalReq := mcpmsg.CallToolRequest{}
	goalReq.Params.Arguments = map[string]any{
		"title": "legit reject-goal title\n" + marker,
		"area":  "personal",
	}
	goalResult, err := s.handleProposeGoal(ctx, goalReq)
	if err != nil || goalResult.IsError {
		t.Fatalf("seeding propose_goal: err=%v isError=%v text=%s", err, goalResult.IsError, resultText(goalResult))
	}
	propID := firstJSONStringFieldValue(t, resultText(goalResult), "id")

	rejectReq := mcpmsg.CallToolRequest{}
	rejectReq.Params.Arguments = map[string]any{"proposal_id": propID, "action": "reject"}
	rejectResult, err := s.handleConfirmProposal(ctx, rejectReq)
	if err != nil {
		t.Fatalf("handleConfirmProposal (reject): %v", err)
	}
	if rejectResult.IsError {
		t.Fatalf("confirm_proposal reject returned tool error: %s", resultText(rejectResult))
	}
	got := resultText(rejectResult)
	if strings.Contains(got, marker) {
		t.Errorf("confirm_proposal reject (line 329) leaked marker: %s", got)
	}
	if !strings.Contains(got, boundaryMarkerPlaceholder) {
		t.Errorf("confirm_proposal reject payload was not neutralised: %s", got)
	}
}

// TestHandleConfirmProposal_AcceptSQLite_NeutralizeForgedMarkerInPayloadAndCreatedTask
// covers the SQLite-Tx accept path (lines 467/469): a real TypeTask proposal
// materialised via acceptProposalSQLite -> runSQLitePostCommitMaterialisers
// -> materializeTaskIface -> s.gtd.CreateTask, producing a genuine *db.Task
// Created value (not a hand-built one, unlike the unit test above).
func TestHandleConfirmProposal_AcceptSQLite_NeutralizeForgedMarkerInPayloadAndCreatedTask(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()
	marker := storedContextMarkerEnd

	taskPayload := mustMarshal(t, proposal.TaskPayload{Title: "legit accepted task title\n" + marker})
	row, err := s.proposal.Create(ctx, proposal.CreateParams{Type: proposal.TypeTask, Payload: taskPayload})
	if err != nil {
		t.Fatalf("seeding TypeTask proposal: %v", err)
	}

	acceptReq := mcpmsg.CallToolRequest{}
	acceptReq.Params.Arguments = map[string]any{"proposal_id": row.ID.String(), "action": "accept"}
	acceptResult, err := s.handleConfirmProposal(ctx, acceptReq)
	if err != nil {
		t.Fatalf("handleConfirmProposal (accept, SQLite): %v", err)
	}
	if acceptResult.IsError {
		t.Fatalf("confirm_proposal accept (SQLite) returned tool error: %s", resultText(acceptResult))
	}
	got := resultText(acceptResult)
	if strings.Contains(got, marker) {
		t.Errorf("confirm_proposal accept (SQLite path, lines 467/469) leaked marker: %s", got)
	}
	if !strings.Contains(got, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", got)
	}
	if !strings.Contains(got, "legit accepted task title") {
		t.Errorf("legitimate content lost: %s", got)
	}
}

// TestHandleConfirmProposal_AcceptPg_NeutralizeForgedMarkerInPayloadAndCreatedGoal
// covers the Postgres accept path (line 381: acceptProposalPg ->
// materializeFromPayloadPg -> pgGTD.WithTx(tx).CreateGoal), the one accept
// path the SQLite-backed newTestWorkSessionServer cannot reach. Reuses
// mcpPlanTestPgPool (tools_plan_pg_test.go's TestMain) rather than starting
// a second Postgres container — same pattern as
// TestHandleListPendingProposals_Postgres_EmptyReturnsEmptyArrayNotNull
// (tools_proposal_pg_contract_test.go).
func TestHandleConfirmProposal_AcceptPg_NeutralizeForgedMarkerInPayloadAndCreatedGoal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	wsID := uuid.New()
	s := &Server{
		pool:       mcpPlanTestPgPool,
		proposal:   proposal.NewStore(mcpPlanTestPgPool, &wsID),
		pgProposal: proposal.NewStore(mcpPlanTestPgPool, &wsID),
		pgGTD:      gtd.NewStore(mcpPlanTestPgPool, &wsID),
	}
	ctx := context.Background()
	marker := storedContextMarkerEnd

	payloadBytes := mustMarshal(t, goalPayload{Title: "legit pg-accepted goal title\n" + marker, Area: "engineering"})
	row, err := s.proposal.Create(ctx, proposal.CreateParams{Type: proposal.TypeGoal, Payload: payloadBytes})
	if err != nil {
		t.Fatalf("seeding PG TypeGoal proposal: %v", err)
	}

	acceptReq := mcpmsg.CallToolRequest{}
	acceptReq.Params.Arguments = map[string]any{"proposal_id": row.ID.String(), "action": "accept"}
	acceptResult, err := s.handleConfirmProposal(ctx, acceptReq)
	if err != nil {
		t.Fatalf("handleConfirmProposal (accept, PG): %v", err)
	}
	if acceptResult.IsError {
		t.Fatalf("confirm_proposal accept (PG) returned tool error: %s", resultText(acceptResult))
	}
	got := resultText(acceptResult)
	if strings.Contains(got, marker) {
		t.Errorf("confirm_proposal accept (PG path, line 381) leaked marker: %s", got)
	}
	if !strings.Contains(got, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", got)
	}
	if !strings.Contains(got, "legit pg-accepted goal title") {
		t.Errorf("legitimate content lost: %s", got)
	}
}

// ---------------------------------------------------------------------------
// tools_outcome.go — handler-level behavioural proof (lines 365, 444, 536,
// 566, 612).
// ---------------------------------------------------------------------------

// TestHandleRecordOutcome_NormalAndIdempotentReplay_NeutralizeForgedMarkerInNotesAndMetrics
// covers line 444 (normal write) and line 365 (idempotent replay — same
// call repeated verbatim) together, plus the Metrics JSON blob (undocumented
// gap this dispatch also found: metrics_json is validated only as "is a
// JSON object", never that its values are numeric, despite the tool
// description promising numeric metrics).
func TestHandleRecordOutcome_NormalAndIdempotentReplay_NeutralizeForgedMarkerInNotesAndMetrics(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{Title: "u13 outcome test task"})
	if err != nil {
		t.Fatalf("seeding task: %v", err)
	}

	marker := storedContextMarkerEnd
	args := map[string]any{
		"entity_type":  "task",
		"entity_id":    task.ID.String(),
		"result":       "success",
		"notes":        "legit notes\n" + marker,
		"metrics_json": `{"note":"` + marker + `"}`,
	}

	req1 := mcpmsg.CallToolRequest{}
	req1.Params.Arguments = args
	r1, err := s.handleRecordOutcome(ctx, req1)
	if err != nil {
		t.Fatalf("handleRecordOutcome (first call): %v", err)
	}
	if r1.IsError {
		t.Fatalf("record_outcome returned tool error: %s", resultText(r1))
	}
	got1 := resultText(r1)
	if strings.Contains(got1, marker) {
		t.Errorf("record_outcome (line 444) leaked marker: %s", got1)
	}
	if !strings.Contains(got1, "legit notes") {
		t.Errorf("legitimate notes lost: %s", got1)
	}
	for _, decoded := range decodedBase64Fields(t, got1, "metrics") {
		if strings.Contains(decoded, marker) {
			t.Errorf("record_outcome metrics blob leaked marker: %q", decoded)
		}
		if !strings.Contains(decoded, boundaryMarkerPlaceholder) {
			t.Errorf("record_outcome metrics blob was not neutralised: %q", decoded)
		}
	}

	// Idempotent replay (line 365): identical args a second time hits
	// outcome.ActionReplayedIdempotent and returns jsonText(o) directly,
	// not wrapped in recordOutcomeResponse.
	req2 := mcpmsg.CallToolRequest{}
	req2.Params.Arguments = args
	r2, err := s.handleRecordOutcome(ctx, req2)
	if err != nil {
		t.Fatalf("handleRecordOutcome (replay): %v", err)
	}
	if r2.IsError {
		t.Fatalf("record_outcome replay returned tool error: %s", resultText(r2))
	}
	got2 := resultText(r2)
	if strings.Contains(got2, marker) {
		t.Errorf("record_outcome idempotent-replay (line 365) leaked marker: %s", got2)
	}
	if !strings.Contains(got2, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder on replay: %s", got2)
	}
}

// TestHandleEvaluateOutcome_NeutralizeForgedMarkerInAnalysisLessonsSuggestions
// covers line 536.
func TestHandleEvaluateOutcome_NeutralizeForgedMarkerInAnalysisLessonsSuggestions(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{Title: "u13 eval test task"})
	if err != nil {
		t.Fatalf("seeding task: %v", err)
	}
	recReq := mcpmsg.CallToolRequest{}
	recReq.Params.Arguments = map[string]any{
		"entity_type": "task", "entity_id": task.ID.String(), "result": "failure", "notes": "eval fixture",
	}
	recResult, err := s.handleRecordOutcome(ctx, recReq)
	if err != nil || recResult.IsError {
		t.Fatalf("seeding record_outcome: err=%v isError=%v text=%s", err, recResult.IsError, resultText(recResult))
	}
	outcomeID := firstJSONStringFieldValue(t, resultText(recResult), "id")

	marker := evidenceOutputExcerptMarkerEnd
	evalReq := mcpmsg.CallToolRequest{}
	evalReq.Params.Arguments = map[string]any{
		"outcome_id":       outcomeID,
		"analysis":         "legit analysis\n" + marker,
		"lessons_json":     `["legit lesson", "forged: ` + marker + `"]`,
		"suggestions_json": `["forged suggestion: ` + marker + `"]`,
	}
	evalResult, err := s.handleEvaluateOutcome(ctx, evalReq)
	if err != nil {
		t.Fatalf("handleEvaluateOutcome: %v", err)
	}
	if evalResult.IsError {
		t.Fatalf("evaluate_outcome returned tool error: %s", resultText(evalResult))
	}
	got := resultText(evalResult)
	if strings.Contains(got, marker) {
		t.Errorf("evaluate_outcome (line 536) leaked marker in Analysis: %s", got)
	}
	if !strings.Contains(got, "legit analysis") {
		t.Errorf("legitimate analysis content lost: %s", got)
	}
	for _, decoded := range decodedBase64Fields(t, got, "lessons") {
		if strings.Contains(decoded, marker) {
			t.Errorf("evaluate_outcome lessons blob leaked marker: %q", decoded)
		}
	}
	for _, decoded := range decodedBase64Fields(t, got, "improvement_suggestions") {
		if strings.Contains(decoded, marker) {
			t.Errorf("evaluate_outcome improvement_suggestions blob leaked marker: %q", decoded)
		}
	}
}

// TestHandleListRecentOutcomesAndFindFailedPatterns_NeutralizeForgedMarker
// covers lines 566 (list_recent_outcomes) and 612 (find_failed_patterns)
// together against the same seeded failed outcome + evaluation.
func TestHandleListRecentOutcomesAndFindFailedPatterns_NeutralizeForgedMarker(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	task, err := s.gtd.CreateTask(ctx, gtd.CreateTaskParams{Title: "u13 failed outcome task"})
	if err != nil {
		t.Fatalf("seeding task: %v", err)
	}

	marker := sessionSummaryMarkerEnd
	recReq := mcpmsg.CallToolRequest{}
	recReq.Params.Arguments = map[string]any{
		"entity_type": "task", "entity_id": task.ID.String(), "result": "failure",
		"notes": "legit failure notes\n" + marker,
	}
	recResult, err := s.handleRecordOutcome(ctx, recReq)
	if err != nil || recResult.IsError {
		t.Fatalf("seeding record_outcome: err=%v isError=%v text=%s", err, recResult.IsError, resultText(recResult))
	}
	outcomeID := firstJSONStringFieldValue(t, resultText(recResult), "id")

	evalReq := mcpmsg.CallToolRequest{}
	evalReq.Params.Arguments = map[string]any{
		"outcome_id":   outcomeID,
		"analysis":     "legit eval analysis\n" + marker,
		"lessons_json": `["forged: ` + marker + `"]`,
	}
	if _, err := s.handleEvaluateOutcome(ctx, evalReq); err != nil {
		t.Fatalf("seeding handleEvaluateOutcome: %v", err)
	}

	listReq := mcpmsg.CallToolRequest{}
	listResult, err := s.handleListRecentOutcomes(ctx, listReq)
	if err != nil {
		t.Fatalf("handleListRecentOutcomes: %v", err)
	}
	if listResult.IsError {
		t.Fatalf("list_recent_outcomes returned tool error: %s", resultText(listResult))
	}
	listText := resultText(listResult)
	if strings.Contains(listText, marker) {
		t.Errorf("list_recent_outcomes (line 566) leaked marker: %s", listText)
	}
	if !strings.Contains(listText, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", listText)
	}

	failReq := mcpmsg.CallToolRequest{}
	failResult, err := s.handleFindFailedPatterns(ctx, failReq)
	if err != nil {
		t.Fatalf("handleFindFailedPatterns: %v", err)
	}
	if failResult.IsError {
		t.Fatalf("find_failed_patterns returned tool error: %s", resultText(failResult))
	}
	failText := resultText(failResult)
	if strings.Contains(failText, marker) {
		t.Errorf("find_failed_patterns (line 612) leaked marker: %s", failText)
	}
	if !strings.Contains(failText, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", failText)
	}
	if !strings.Contains(failText, "legit failure notes") || !strings.Contains(failText, "legit eval analysis") {
		t.Errorf("legitimate content lost: %s", failText)
	}
}

// ---------------------------------------------------------------------------
// tools_watchdog.go — handler-level behavioural proof (lines 126, 570).
// ---------------------------------------------------------------------------

// TestHandleAnalyzeAgentBehavior_NeutralizeForgedMarkerInFindingDetail
// covers line 126, via detectRepeatedCorrections (>=3 decisions sharing the
// same title within 7 days) — the only one of the 8 persisted detectors
// naturally triggerable without backdating timestamps that a store-layer
// write can't set directly (every other detector's cutoff is on the OLD
// side of a time window; this one is the one whose window keeps RECENT
// rows).
func TestHandleAnalyzeAgentBehavior_NeutralizeForgedMarkerInFindingDetail(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	marker := storedContextMarkerEnd
	forgedTitle := "Repeated decision\n" + marker
	for i := 0; i < 3; i++ {
		if _, err := s.decision.Log(ctx, decision.LogParams{
			Title:     forgedTitle,
			Context:   "ctx",
			Decision:  "decision text",
			Rationale: "rationale text",
			Source:    decision.SourceManual,
		}); err != nil {
			t.Fatalf("seeding decision %d: %v", i, err)
		}
	}

	req := mcpmsg.CallToolRequest{}
	result, err := s.handleAnalyzeAgentBehavior(ctx, req)
	if err != nil {
		t.Fatalf("handleAnalyzeAgentBehavior: %v", err)
	}
	if result.IsError {
		t.Fatalf("analyze_agent_behavior returned tool error: %s", resultText(result))
	}
	got := resultText(result)
	if strings.Contains(got, marker) {
		t.Errorf("analyze_agent_behavior (line 126) leaked marker in Finding.Detail: %s", got)
	}
	if !strings.Contains(got, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", got)
	}
	if !strings.Contains(got, "Repeated decision") {
		t.Errorf("legitimate content lost: %s", got)
	}
}

// TestHandleDetectUnclosedLoops_NeutralizeForgedMarkerInDetail covers line
// 570. Seeds a discipline_events_m8 row DIRECTLY via disciplineEventStore.
// Insert with an UNNEUTRALISED forged marker in Detail — simulating exactly
// what an earlier analyze_agent_behavior run would have persisted, since
// insertWatchdogEvent's write path is intentionally untouched by this
// dispatch (see tools_watchdog.go's package doc comment on the
// neutralisation block: read-time-only, copy-not-mutate). Proves the marker
// is stripped on THIS read too, independent of whatever
// TestHandleAnalyzeAgentBehavior... above already proved for the
// insert-time response.
func TestHandleDetectUnclosedLoops_NeutralizeForgedMarkerInDetail(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	marker := archSnapshotMarkerEnd
	detail := mustMarshal(t, map[string]any{
		"task_id":     uuid.New().String(),
		"title":       "legit stuck task\n" + marker,
		"updated_at":  time.Now().UTC().Format(time.RFC3339),
		"stuck_hours": 4,
	})
	if err := s.disciplineEventStore.Insert(ctx, watchdog.InsertParams{
		WorkspaceID: s.workspaceUUID(),
		EventType:   watchdog.EventTypeStuckTask,
		Severity:    watchdog.SeverityWarn,
		Detail:      detail,
	}); err != nil {
		t.Fatalf("seeding discipline event: %v", err)
	}

	req := mcpmsg.CallToolRequest{}
	result, err := s.handleDetectUnclosedLoops(ctx, req)
	if err != nil {
		t.Fatalf("handleDetectUnclosedLoops: %v", err)
	}
	if result.IsError {
		t.Fatalf("detect_unclosed_loops returned tool error: %s", resultText(result))
	}
	got := resultText(result)
	if strings.Contains(got, marker) {
		t.Errorf("detect_unclosed_loops (line 570) leaked marker in Detail: %s", got)
	}
	if !strings.Contains(got, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", got)
	}
	if !strings.Contains(got, "legit stuck task") {
		t.Errorf("legitimate content lost: %s", got)
	}
}

// ---------------------------------------------------------------------------
// tools_atom.go — handler-level behavioural proof (lines 97, 124).
// ---------------------------------------------------------------------------

// TestHandleSearchAtomsAndTraverseAtoms_NeutralizeForgedMarkerInContentAndTags
// covers lines 124 (search_atoms) and 97 (traverse_atoms), plus the
// additional Keywords/Tags fields this dispatch found are the same class of
// gap as Content (ai.Atomizer LLM output, entry-count-capped but not
// length-capped — see wrapUntrustedAtom's doc comment, tools_atom.go).
func TestHandleSearchAtomsAndTraverseAtoms_NeutralizeForgedMarkerInContentAndTags(t *testing.T) {
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	marker := evidenceOutputExcerptMarkerStart
	forgedContent := "legit atom content u13searchmarker\n" + marker
	forgedTag := "tag-" + marker

	a, err := s.atom.AddAtom(ctx, atom.AddAtomParams{
		WorkspaceID: s.workspaceUUID(),
		ParentTable: "test_fixture",
		ParentID:    uuid.New(),
		Content:     forgedContent,
		Keywords:    []string{"u13searchmarker"},
		Tags:        []string{forgedTag},
	})
	if err != nil {
		t.Fatalf("seeding atom: %v", err)
	}

	searchReq := mcpmsg.CallToolRequest{}
	searchReq.Params.Arguments = map[string]any{"query": "u13searchmarker"}
	searchResult, err := s.handleSearchAtoms(ctx, searchReq)
	if err != nil {
		t.Fatalf("handleSearchAtoms: %v", err)
	}
	if searchResult.IsError {
		t.Fatalf("search_atoms returned tool error: %s", resultText(searchResult))
	}
	searchText := resultText(searchResult)
	if strings.Contains(searchText, marker) {
		t.Errorf("search_atoms (line 124) leaked marker: %s", searchText)
	}
	if !strings.Contains(searchText, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", searchText)
	}
	if !strings.Contains(searchText, "legit atom content") {
		t.Errorf("legitimate content lost: %s", searchText)
	}

	traverseReq := mcpmsg.CallToolRequest{}
	traverseReq.Params.Arguments = map[string]any{"start_atom_id": a.ID.String()}
	traverseResult, err := s.handleTraverseAtoms(ctx, traverseReq)
	if err != nil {
		t.Fatalf("handleTraverseAtoms: %v", err)
	}
	if traverseResult.IsError {
		t.Fatalf("traverse_atoms returned tool error: %s", resultText(traverseResult))
	}
	traverseText := resultText(traverseResult)
	if strings.Contains(traverseText, marker) {
		t.Errorf("traverse_atoms (line 97) leaked marker: %s", traverseText)
	}
	if !strings.Contains(traverseText, boundaryMarkerPlaceholder) {
		t.Errorf("marker removed without leaving the placeholder: %s", traverseText)
	}
}
