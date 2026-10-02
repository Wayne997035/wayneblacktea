package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// trackingDecisionStore records the List call made by handleListDecisions,
// allowing tests to assert the ListParams built from MCP tool arguments
// without a real DB. All/ByRepo/ByProject/ByTask/SearchByCosine are no-ops —
// handleListDecisions no longer calls them directly (P3.0a Stage B routes
// list_decisions exclusively through List).
type trackingDecisionStore struct {
	listCalled     bool
	lastListParams decision.ListParams
	listResult     []db.Decision
	listErr        error
	lastLogged     decision.LogParams
}

func (d *trackingDecisionStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	d.lastLogged = p
	return &db.Decision{ID: uuid.New()}, nil
}

func (d *trackingDecisionStore) All(_ context.Context, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) ByRepo(_ context.Context, _ string, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) ByProject(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) ByTask(_ context.Context, _ uuid.UUID, _ int32) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) SearchByCosine(_ context.Context, _ []float32, _ int) ([]db.Decision, error) {
	return nil, nil
}

func (d *trackingDecisionStore) List(_ context.Context, p decision.ListParams) ([]db.Decision, error) {
	d.listCalled = true
	d.lastListParams = p
	return d.listResult, d.listErr
}

// Compile-time: trackingDecisionStore must satisfy decision.StoreIface.
var _ decision.StoreIface = (*trackingDecisionStore)(nil)

// callListDecisions invokes handleListDecisions with the given args.
func callListDecisions(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleListDecisions(context.Background(), req)
	if err != nil {
		t.Fatalf("handleListDecisions returned unexpected error: %v", err)
	}
	return result
}

// TestHandleListDecisions_EmptyRepoName verifies that an empty repo_name
// (with no project_id) is treated the same as "neither" — a workspace-wide
// List call with both filters empty (P3.0a Stage B truth table row "neither").
func TestHandleListDecisions_EmptyRepoName(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"repo_name": "",
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.listCalled {
		t.Fatal("expected decision.List() to be called")
	}
	if dec.lastListParams.ProjectID != nil || dec.lastListParams.RepoName != "" {
		t.Errorf("expected workspace-wide ListParams (no project, no repo), got %+v", dec.lastListParams)
	}
	// Response must be valid JSON.
	if txt := resultText(r); txt != "" {
		var out any
		if err := json.Unmarshal([]byte(txt), &out); err != nil {
			t.Errorf("response is not valid JSON: %v", err)
		}
	}
}

// TestHandleListDecisions_NonEmptyRepoName verifies "repo only" — the
// ListParams built from a non-empty repo_name carries RepoName and no
// ProjectID (P3.0a Stage B truth table row "repo only").
func TestHandleListDecisions_NonEmptyRepoName(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"repo_name": "wayneblacktea",
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.listCalled {
		t.Fatal("expected decision.List() to be called")
	}
	if dec.lastListParams.RepoName != "wayneblacktea" {
		t.Errorf("ListParams.RepoName = %q, want %q", dec.lastListParams.RepoName, "wayneblacktea")
	}
	if dec.lastListParams.ProjectID != nil {
		t.Errorf("ListParams.ProjectID = %v, want nil", dec.lastListParams.ProjectID)
	}
}

// TestHandleListDecisions_OmittedRepoName verifies a missing repo_name key
// (not present in args at all) is also treated as "neither" (P3.0a Stage B
// truth table row "neither").
func TestHandleListDecisions_OmittedRepoName(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{}) // no repo_name key
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if !dec.listCalled {
		t.Fatal("expected decision.List() to be called")
	}
	if dec.lastListParams.ProjectID != nil || dec.lastListParams.RepoName != "" {
		t.Errorf("expected workspace-wide ListParams (no project, no repo), got %+v", dec.lastListParams)
	}
}

// TestHandleListDecisions_InvalidProjectUUID verifies the truth table row
// "invalid project UUID -> tool error, store never called".
func TestHandleListDecisions_InvalidProjectUUID(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"project_id": "not-a-valid-uuid",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid project_id UUID, got result: %s", resultText(r))
	}
	if txt := resultText(r); txt != errMsgInvalidProjectIDUUID {
		t.Errorf("unexpected error message: %q", txt)
	}
	if dec.listCalled {
		t.Error("expected decision.List() NOT to be called when project_id is malformed")
	}
}

// TestHandleListDecisions_ProjectWinsOverRepo verifies the truth table row
// "project + repo both given -> project wins" — RepoName must be cleared
// from ListParams once a valid project_id is present.
func TestHandleListDecisions_ProjectWinsOverRepo(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}
	projectID := uuid.New()

	r := callListDecisions(t, s, map[string]any{
		"project_id": projectID.String(),
		"repo_name":  "wayneblacktea",
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if dec.lastListParams.ProjectID == nil || *dec.lastListParams.ProjectID != projectID {
		t.Errorf("ListParams.ProjectID = %v, want %s", dec.lastListParams.ProjectID, projectID)
	}
	if dec.lastListParams.RepoName != "" {
		t.Errorf("ListParams.RepoName = %q, want empty (project must win over repo_name)", dec.lastListParams.RepoName)
	}
}

// TestHandleListDecisions_NonexistentProjectReturnsEmpty verifies the truth
// table row "project not owned / nonexistent -> returns [] (not an error)" —
// when the store returns an empty result for a well-formed-but-unmatched
// project_id, handleListDecisions must NOT turn that into a tool error.
//
// [F0930-13] Unmarshals into the "decisions" object field, not a bare array
// — handleListDecisions' response shape changed from a bare array to an
// object ({"decisions":[...],"returned",...}) so list_decisions can carry
// the same returned/limit/offset/has_more/truncated_by_budget envelope the
// other three list tools already have.
func TestHandleListDecisions_NonexistentProjectReturnsEmpty(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{listResult: nil} // store found nothing
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{
		"project_id": uuid.New().String(),
	})
	if r.IsError {
		t.Fatalf("expected IsError=false for nonexistent project, got: %s", resultText(r))
	}
	var out struct {
		Decisions []db.Decision `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("response is not a valid JSON object with a decisions field: %v (%s)", err, resultText(r))
	}
	if len(out.Decisions) != 0 {
		t.Errorf("expected empty decisions array, got %d rows", len(out.Decisions))
	}
}

// TestHandleListDecisions_IncludeAutoOmittedDefaultsFalse verifies the truth
// table row "include_auto omitted or non-bool -> treated as false".
func TestHandleListDecisions_IncludeAutoOmittedDefaultsFalse(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	callListDecisions(t, s, map[string]any{}) // include_auto absent
	if dec.lastListParams.IncludeAuto {
		t.Error("expected IncludeAuto=false when include_auto is omitted")
	}
}

// TestHandleListDecisions_IncludeAutoNonBoolDefaultsFalse verifies the truth
// table row "include_auto omitted or non-bool -> treated as false" for the
// non-bool case specifically (fail-closed, not fail-open).
func TestHandleListDecisions_IncludeAutoNonBoolDefaultsFalse(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	callListDecisions(t, s, map[string]any{"include_auto": "true"}) // string, not bool
	if dec.lastListParams.IncludeAuto {
		t.Error("expected IncludeAuto=false when include_auto is a non-bool value")
	}
}

// TestHandleListDecisions_NilResultReturnsEmptyArrayNotNull verifies the
// nil-slice-vs-JSON-null gap: when the store returns a nil []db.Decision (0
// rows — see decision.Store.List / sqlite DecisionStore.List, both leave the
// slice nil until a row is appended), handleListDecisions must serialize the
// "decisions" field as an empty JSON array, not the literal 4-byte text
// "null".
//
// json.Unmarshal([]byte("null"), &slice) leaves slice nil with len==0 — the
// exact same shape as unmarshaling "[]" — so asserting on len(out) after
// unmarshal (as TestHandleListDecisions_NonexistentProjectReturnsEmpty does)
// cannot distinguish the two. This test asserts the raw text instead.
//
// [F0930-13] Checks `"decisions":[]` / `"decisions":null` substrings, not
// the whole-body `== "[]"` equality this test used before the response
// shape changed from a bare array to an object.
func TestHandleListDecisions_NilResultReturnsEmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{listResult: nil}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	raw := resultText(r)
	if !strings.Contains(raw, `"decisions":[]`) {
		t.Errorf("raw body = %q, want it to contain %q", raw, `"decisions":[]`)
	}
	if strings.Contains(raw, `"decisions":null`) {
		t.Errorf("raw body = %q, decisions must not serialize to JSON null", raw)
	}
}

// TestHandleListDecisions_IncludeAutoTrue verifies the truth table row
// "include_auto=true -> returns manual + auto" at the ListParams-plumbing
// level (actual filtering is a store-layer concern, covered by store tests).
func TestHandleListDecisions_IncludeAutoTrue(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	callListDecisions(t, s, map[string]any{"include_auto": true})
	if !dec.lastListParams.IncludeAuto {
		t.Error("expected IncludeAuto=true when include_auto=true")
	}
}

// TestListDecisions_DefaultAndMaxLimit pins [F0930-13]'s tightened
// default/max (10/40, down from 20/100 — D5/D13). Asserted against the
// response's own "limit" field, not the store's ListParams.Limit (which now
// carries a +1 over-fetch for has_more detection — the same trick
// handleListTasks/handleListProjects/handleListGoals already use).
func TestListDecisions_DefaultAndMaxLimit(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	var out struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, resultText(r))
	}
	if out.Limit != defaultListDecisionsLimit {
		t.Errorf("default limit = %d, want %d", out.Limit, defaultListDecisionsLimit)
	}

	rMax := callListDecisions(t, s, map[string]any{"limit": float64(999)})
	if rMax.IsError {
		t.Fatalf("unexpected error result: %s", resultText(rMax))
	}
	var outMax struct {
		Limit int `json:"limit"`
	}
	if err := json.Unmarshal([]byte(resultText(rMax)), &outMax); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, resultText(rMax))
	}
	if outMax.Limit != maxListDecisionsLimit {
		t.Errorf("limit=999 should clamp to %d (the new max), got %d — not the old 100", maxListDecisionsLimit, outMax.Limit)
	}
}

// TestListDecisions_ResponseIsObject pins [F0930-13]'s breaking wire-format
// change: list_decisions used to return a bare JSON array
// (TestHandleListDecisions_NilResultReturnsEmptyArrayNotNull's pre-F0930-13
// history) and now returns an object carrying the same
// decisions/returned/limit/offset/has_more/truncated_by_budget envelope the
// other three list tools already had.
func TestListDecisions_ResponseIsObject(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callListDecisions(t, s, map[string]any{})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	raw := resultText(r)
	for _, field := range []string{`"decisions"`, `"returned"`, `"limit"`, `"offset"`, `"has_more"`, `"truncated_by_budget"`} {
		if !strings.Contains(raw, field) {
			t.Errorf("response missing %s — want an object envelope like the other three list tools: %s", field, raw)
		}
	}
	if trimmed := strings.TrimSpace(raw); strings.HasPrefix(trimmed, "[") {
		t.Errorf("response is a bare array, want an object: %s", raw)
	}
}

// TestListDecisions_RuneBudgetCJKWorstCase is [F0930-13]/[F0930-11]'s named
// CJK worst-case acceptance test for list_decisions, mirroring
// TestListTasks_RuneBudgetCJKWorstCase (tools_gtd_test.go) — a page of
// CJK-heavy decisions must be cut by the shared listRuneBudget before the
// row-count clamp (maxListDecisionsLimit=40) would otherwise stop it. Fields
// are sized below decisionBodyMaxRunes/decisionTitleMaxRunes on purpose
// (~4,000 runes/row) so several rows accumulate before the budget cuts —
// exercising the "page too big" truncation path, distinct from the
// single-row-alone-exceeds-budget edge case list_tasks' sibling test covers.
func TestListDecisions_RuneBudgetCJKWorstCase(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	ctx := context.Background()
	title := strings.Repeat("決", 500)
	body := strings.Repeat("測", 1000)
	for i := 0; i < 20; i++ {
		if _, err := s.decision.Log(ctx, decision.LogParams{
			Title:        title,
			Context:      body,
			Decision:     body,
			Rationale:    body,
			Alternatives: body,
			Source:       decision.SourceManual,
		}); err != nil {
			t.Fatalf("decision.Log %d: %v", i, err)
		}
	}

	r := callListDecisions(t, s, map[string]any{"limit": float64(maxListDecisionsLimit)})
	if r.IsError {
		t.Fatalf("expected success, got error: %s", resultText(r))
	}
	var out struct {
		Decisions         json.RawMessage `json:"decisions"`
		Returned          int             `json:"returned"`
		HasMore           bool            `json:"has_more"`
		TruncatedByBudget bool            `json:"truncated_by_budget"`
	}
	if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
		t.Fatalf("unmarshal response: %v (%s)", err, resultText(r))
	}
	if out.Returned >= maxListDecisionsLimit {
		t.Errorf("returned = %d, want fewer than the requested %d — a page of ~4,000-rune CJK "+
			"decisions must not all fit in the %d-rune budget", out.Returned, maxListDecisionsLimit, listRuneBudget)
	}
	if !out.HasMore {
		t.Error("has_more = false — 20 decisions were seeded, fewer must have been returned")
	}
	if !out.TruncatedByBudget {
		t.Error("truncated_by_budget = false — a page smaller than the row-count clamp must say why")
	}
	if runes := utf8.RuneCountInString(string(out.Decisions)); runes > listRuneBudget {
		t.Errorf("decisions array is %d runes, want at most the %d-rune budget "+
			"(mutation: remove the budget check and this goes red)", runes, listRuneBudget)
	}
}

// TestListDecisions_PaginationAdvancesPastOversizedRow is F197-L2's
// integration acceptance: a decision whose body alone exceeds listRuneBudget
// must not stall pagination. list_decisions is the only MCP path that reads
// a decision's full body text (no get_decision), so the row can never be
// truncated at this layer — walking limit=1 across offset=0,1,2 must return
// exactly 1 decision per page and collect all 3 seeded decisions, never
// landing on a returned=0 page stuck at the oversized row's offset.
func TestListDecisions_PaginationAdvancesPastOversizedRow(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	ctx := context.Background()

	oversizedBody := strings.Repeat("測", decisionBodyMaxRunes) // alone exceeds listRuneBudget
	seedTitles := []string{"normal decision one", "oversized decision", "normal decision two"}
	bodies := []string{"c", oversizedBody, "c"}
	for i, title := range seedTitles {
		if _, err := s.decision.Log(ctx, decision.LogParams{
			Title: title, Context: bodies[i], Decision: "d", Rationale: "r", Source: decision.SourceManual,
		}); err != nil {
			t.Fatalf("seed decision %q: %v", title, err)
		}
	}

	seenTitles := map[string]bool{}
	for offset := 0; offset < 3; offset++ {
		r := callListDecisions(t, s, map[string]any{"limit": float64(1), "offset": float64(offset)})
		if r.IsError {
			t.Fatalf("offset=%d: unexpected error: %s", offset, resultText(r))
		}
		var out struct {
			Decisions []struct {
				Title string `json:"title"`
			} `json:"decisions"`
			Returned int `json:"returned"`
		}
		if err := json.Unmarshal([]byte(resultText(r)), &out); err != nil {
			t.Fatalf("offset=%d: unmarshal: %v (%s)", offset, err, resultText(r))
		}
		if out.Returned != 1 || len(out.Decisions) != 1 {
			t.Fatalf("offset=%d: returned = %d, want 1 — pagination must advance past the oversized "+
				"row, not stall at a returned=0 page", offset, out.Returned)
		}
		seenTitles[out.Decisions[0].Title] = true
	}
	for _, title := range seedTitles {
		if !seenTitles[title] {
			t.Errorf("decision %q was never returned across 3 pages of limit=1 — pagination got stuck", title)
		}
	}
}

// callLogDecision invokes handleLogDecision with the given args.
func callLogDecision(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleLogDecision(context.Background(), req)
	if err != nil {
		t.Fatalf("handleLogDecision returned unexpected Go error: %v", err)
	}
	return result
}

// TestHandleLogDecision_InvalidTaskIDUUID verifies that a malformed task_id
// UUID causes handleLogDecision to return a tool-level error result
// (IsError=true) rather than a Go error, matching the MCP contract.
func TestHandleLogDecision_InvalidTaskIDUUID(t *testing.T) {
	t.Parallel()
	s := &Server{decision: &trackingDecisionStore{}}

	r := callLogDecision(t, s, map[string]any{
		"title":     "ADR: use SQLite for local dev",
		"context":   "we want zero-dependency local setup",
		"decision":  "ship SQLite backend",
		"rationale": "no Postgres required",
		"task_id":   "not-a-valid-uuid",
	})
	if !r.IsError {
		t.Fatalf("expected IsError=true for invalid task_id UUID, got result: %s", resultText(r))
	}
	if txt := resultText(r); txt != errMsgInvalidTaskIDUUID {
		t.Errorf("unexpected error message: %q", txt)
	}
}

// TestHandleLogDecision_ForgedSourceArgIgnored verifies the provenance
// contract for the manual MCP path (P3.0a producer #1): a caller cannot
// influence decisions.source by adding an arbitrary "source" argument.
// handleLogDecision never reads a "source" key at all — Source is bound to
// decision.SourceManual as a path constant — so a forged "source":"auto" arg
// (attempting to masquerade a manual log_decision call as system-inferred)
// is silently ignored and the persisted LogParams.Source stays "manual".
func TestHandleLogDecision_ForgedSourceArgIgnored(t *testing.T) {
	t.Parallel()
	dec := &trackingDecisionStore{}
	s := &Server{decision: dec}

	r := callLogDecision(t, s, map[string]any{
		"title":     "ADR: use SQLite for local dev",
		"context":   "we want zero-dependency local setup",
		"decision":  "ship SQLite backend",
		"rationale": "no Postgres required",
		"source":    "auto", // forged: not a real tool arg, must be ignored
	})
	if r.IsError {
		t.Fatalf("unexpected error result: %s", resultText(r))
	}
	if dec.lastLogged.Source != decision.SourceManual {
		t.Errorf("Source = %q, want %q (forged arg must not override the path constant)",
			dec.lastLogged.Source, decision.SourceManual)
	}
}

// TestLogDecision_AlternativesTagNoiseNamesField pins AC-4 / F0911-04:
// alternatives is not one of CheckDecisionNoise's four gated fields
// (title/context/decision/rationale — see CheckField's callers in
// internal/validator/noise.go), so a tag-noisy alternatives value sails
// past handleLogDecision's front gate and reaches the store. Before
// F0911-04 the SQLite harness wrote the row silently; now
// sqlite.DecisionStore.Log rejects it the same way pgx already did, and
// F0911-01 surfaces the field name and excerpt instead of the flat
// "logging decision failed".
func TestLogDecision_AlternativesTagNoiseNamesField(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callLogDecision(t, s, map[string]any{
		"title":        "ADR: use SQLite for local dev",
		"context":      "we want zero-dependency local setup",
		"decision":     "ship SQLite backend",
		"rationale":    "no Postgres required",
		"alternatives": "A </invoke> B",
	})
	if !r.IsError {
		t.Fatalf("expected tag-noise rejection, got success: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "alternatives") {
		t.Errorf("error should name the field, got: %s", text)
	}
	if !strings.Contains(text, `near "`) {
		t.Errorf("error should include the bounded excerpt, got: %s", text)
	}
}

// TestLogDecision_AlternativesTagNoise_CleanNotAnError is AC-4's negative
// case: ordinary alternatives text must not trip the new check.
func TestLogDecision_AlternativesTagNoise_CleanNotAnError(t *testing.T) {
	t.Parallel()
	s := newTestWorkSessionServer(t)
	r := callLogDecision(t, s, map[string]any{
		"title":        "ADR: use SQLite for local dev",
		"context":      "we want zero-dependency local setup",
		"decision":     "ship SQLite backend",
		"rationale":    "no Postgres required",
		"alternatives": "A vs B",
	})
	if r.IsError {
		t.Fatalf("clean alternatives must not be rejected, got: %s", resultText(r))
	}
}
