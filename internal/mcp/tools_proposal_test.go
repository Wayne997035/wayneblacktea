package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// callConfirmProposals is a helper that invokes handleConfirmProposals with
// the given args and fails the test if the invocation itself returns an error.
func callConfirmProposals(t *testing.T, s *Server, args map[string]any) *mcpmsg.CallToolResult {
	t.Helper()
	req := mcpmsg.CallToolRequest{}
	req.Params.Arguments = args
	result, err := s.handleConfirmProposals(context.Background(), req)
	if err != nil {
		t.Fatalf("handleConfirmProposals error: %v", err)
	}
	return result
}

// newProposalTestServer creates a minimal Server backed by SQLite for proposal tool tests.
func newProposalTestServer(t *testing.T) *Server {
	t.Helper()
	return newTestWorkSessionServer(t)
}

// ---- input validation tests ----

func TestHandleConfirmProposals_InvalidAction(t *testing.T) {
	t.Parallel()
	s := newProposalTestServer(t)
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{uuid.New().String()},
		"action": "destroy",
	})
	if !r.IsError {
		t.Error("expected error for invalid action")
	}
	if !strings.Contains(resultText(r), "accept") {
		t.Errorf("error should mention 'accept', got: %s", resultText(r))
	}
}

func TestHandleConfirmProposals_EmptyIDs(t *testing.T) {
	t.Parallel()
	s := newProposalTestServer(t)
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{},
		"action": "accept",
	})
	if !r.IsError {
		t.Error("expected error for empty ids")
	}
}

func TestHandleConfirmProposals_TooManyIDs(t *testing.T) {
	t.Parallel()
	s := newProposalTestServer(t)
	ids := make([]any, 101)
	for i := range ids {
		ids[i] = uuid.New().String()
	}
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    ids,
		"action": "reject",
	})
	if !r.IsError {
		t.Error("expected error for >100 ids")
	}
}

func TestHandleConfirmProposals_InvalidUUID(t *testing.T) {
	t.Parallel()
	s := newProposalTestServer(t)
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{"not-a-uuid"},
		"action": "accept",
	})
	if !r.IsError {
		t.Error("expected error for invalid UUID")
	}
}

func TestHandleConfirmProposals_NonStringIDElement(t *testing.T) {
	t.Parallel()
	s := newProposalTestServer(t)
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{42}, // integer, not string
		"action": "accept",
	})
	if !r.IsError {
		t.Error("expected error for non-string id element")
	}
}

// ---- happy path: proposal created then confirmed ----

func TestHandleConfirmProposals_RejectPending(t *testing.T) {
	t.Parallel()
	s := newProposalTestServer(t)
	ctx := context.Background()

	// Create a proposal first via handleProposeGoal.
	propReq := mcpmsg.CallToolRequest{}
	propReq.Params.Arguments = map[string]any{
		"title": "Batch test goal",
		"area":  "engineering",
	}
	propResult, err := s.handleProposeGoal(ctx, propReq)
	if err != nil || propResult.IsError {
		t.Fatalf("handleProposeGoal: err=%v isError=%v text=%s", err, propResult.IsError, resultText(propResult))
	}

	// Extract the proposal ID from the JSON result. The indented JSON produced
	// by jsonText uses `"id": "<uuid>"` (space after colon).
	text := resultText(propResult)
	// Try both compact and indented forms.
	var rawID string
	for _, prefix := range []string{`"id": "`, `"id":"`} {
		start := strings.Index(text, prefix)
		if start == -1 {
			continue
		}
		start += len(prefix)
		end := strings.Index(text[start:], `"`)
		if end == -1 {
			continue
		}
		candidate := text[start : start+end]
		if _, err := uuid.Parse(candidate); err == nil {
			rawID = candidate
			break
		}
	}
	if rawID == "" {
		t.Fatalf("could not find valid UUID id in response: %s", text)
	}

	// Batch confirm (reject) the proposal.
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    []any{rawID},
		"action": "reject",
	})
	if r.IsError {
		t.Errorf("expected success, got error: %s", resultText(r))
	}
	result := resultText(r)
	// jsonText uses indented JSON so check both compact and indented forms.
	if !strings.Contains(result, `"accepted": 1`) && !strings.Contains(result, `"accepted":1`) {
		t.Errorf("expected accepted:1 in result, got: %s", result)
	}
}

func TestHandleConfirmProposals_ExactlyMaxIDs_NoProposals(t *testing.T) {
	t.Parallel()
	// Exactly 100 random UUIDs — none are in the store so all will fail,
	// but validation must pass (not a 400-equivalent tool error on count).
	s := newProposalTestServer(t)
	ids := make([]any, 100)
	for i := range ids {
		ids[i] = uuid.New().String()
	}
	r := callConfirmProposals(t, s, map[string]any{
		"ids":    ids,
		"action": "accept",
	})
	// Should not be a validation error (count is OK); the result will show
	// all failed because the IDs don't exist.
	if r.IsError {
		t.Errorf("100 IDs should not trigger validation error, got: %s", resultText(r))
	}
	result := resultText(r)
	// jsonText uses indented JSON so check both compact and indented forms.
	if !strings.Contains(result, `"failed": 100`) && !strings.Contains(result, `"failed":100`) {
		t.Errorf("expected failed:100 in result, got: %s", result)
	}
}

// ---- decodeGoalParams / decodeProjectParams: empty-title + priority range ----
//
// [F981-05] mirrors internal/proposal/accept_decode_length_test.go's coverage
// of the seam-side decoders — decodeGoalParams/decodeProjectParams (this
// file) previously lacked the empty-title rejection both have, and
// decodeProjectParams also lacked the priority 1-5 range check (LLM tool
// input is hostile; these decoders run on a payload an agent controls via
// propose_goal/propose_project).

func TestDecodeGoalParams_EmptyTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		payload    map[string]any
		wantErr    bool
		wantSubstr string
	}{
		{
			name:    "non-empty title → ok",
			payload: map[string]any{"title": "Become CEO", "area": "career"},
			wantErr: false,
		},
		{
			name:       "empty title → rejected",
			payload:    map[string]any{"title": "", "area": "career"},
			wantErr:    true,
			wantSubstr: "goal payload missing title",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, errMsg := decodeGoalParams(raw)
			if tc.wantErr {
				if errMsg == "" {
					t.Fatalf("decodeGoalParams: want error, got none")
				}
				if !strings.Contains(errMsg, tc.wantSubstr) {
					t.Errorf("errMsg = %q, want substring %q", errMsg, tc.wantSubstr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("decodeGoalParams: unexpected error: %s", errMsg)
			}
		})
	}
}

func TestDecodeProjectParams_EmptyTitleAndPriorityRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		payload    map[string]any
		wantErr    bool
		wantSubstr string
	}{
		{
			name:    "within limits → ok",
			payload: map[string]any{"name": "proj", "title": "Project", "area": "projects"},
			wantErr: false,
		},
		{
			name:       "empty title → rejected",
			payload:    map[string]any{"name": "proj", "title": ""},
			wantErr:    true,
			wantSubstr: "project payload missing title",
		},
		{
			name:       "priority out of range (too high) → rejected",
			payload:    map[string]any{"title": "ok", "priority": 6},
			wantErr:    true,
			wantSubstr: "project priority must be 1-5",
		},
		{
			name:       "priority out of range (negative) → rejected",
			payload:    map[string]any{"title": "ok", "priority": -1},
			wantErr:    true,
			wantSubstr: "project priority must be 1-5",
		},
		{
			name:    "priority 1 → ok (boundary)",
			payload: map[string]any{"title": "ok", "priority": 1},
			wantErr: false,
		},
		{
			name:    "priority 5 → ok (boundary)",
			payload: map[string]any{"title": "ok", "priority": 5},
			wantErr: false,
		},
		{
			name:    "priority 0 (unset) → ok",
			payload: map[string]any{"title": "ok"},
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal fixture: %v", err)
			}
			_, errMsg := decodeProjectParams(raw)
			if tc.wantErr {
				if errMsg == "" {
					t.Fatalf("decodeProjectParams: want error, got none")
				}
				if !strings.Contains(errMsg, tc.wantSubstr) {
					t.Errorf("errMsg = %q, want substring %q", errMsg, tc.wantSubstr)
				}
				return
			}
			if errMsg != "" {
				t.Fatalf("decodeProjectParams: unexpected error: %s", errMsg)
			}
		})
	}
}

// ---- F0911-04: SQLite decision-store tag-noise parity on accept_proposal ----

// TestAcceptProposal_DecisionTagNoiseNamesField pins AC-14: the iface accept
// path (tools_proposal.go:1048, materializeDecisionIface -> s.decision.Log)
// used by acceptProposalSequential when neither a Postgres pool nor
// s.sqliteProposal is wired. Before F0911-01/F0911-04 storeErrorText
// returned only "creating decision failed" and the SQLite row was already
// written; after, the field name and excerpt survive and the row is
// rejected.
func TestAcceptProposal_DecisionTagNoiseNamesField(t *testing.T) {
	t.Parallel()
	s := newProposalTestServer(t)
	s.sqliteProposal = nil // force acceptProposalSequential (the iface path)
	ctx := context.Background()

	payload := mustMarshal(t, proposal.DecisionProposerPayload{
		Title:    "iface path decision",
		Decision: "Use Y</decision>",
	})
	row, err := s.proposal.Create(ctx, proposal.CreateParams{Type: proposal.TypeDecision, Payload: payload})
	if err != nil {
		t.Fatalf("seeding TypeDecision proposal: %v", err)
	}

	r := callConfirmProposal(t, s, row.ID.String(), "accept")
	if !r.IsError {
		t.Fatalf("expected tag-noise rejection, got success: %s", resultText(r))
	}
	text := resultText(r)
	if !strings.Contains(text, "decision") {
		t.Errorf("error should name the field, got: %s", text)
	}
	if !strings.Contains(text, `near "`) {
		t.Errorf("error should include the bounded excerpt, got: %s", text)
	}
}

// TestAcceptProposal_SQLiteTagNoiseRollsBackWholeAcceptance pins AC-15: the
// SQLite tx accept path (tools_proposal.go:854 ->
// storage/sqlite/accept_proposal.go:121, DecisionStore.LogTx) now validates
// tag-noise like pgx, so a mid-tx failure rolls back the WHOLE acceptance —
// the proposal stays pending and no decisions row is created — instead of
// (before F0911-04) silently writing the noisy row and resolving the
// proposal. On pgx this rollback behaviour already exists today
// (proposal/accept_pg.go:175); this pins that SQLite now matches.
func TestAcceptProposal_SQLiteTagNoiseRollsBackWholeAcceptance(t *testing.T) {
	t.Parallel()
	s, sdb := newTestWorkSessionServerWithDB(t)
	ctx := context.Background()

	payload := mustMarshal(t, proposal.DecisionProposerPayload{
		Title:    "sqlite tx decision",
		Decision: "Use Y</decision>",
	})
	row, err := s.proposal.Create(ctx, proposal.CreateParams{Type: proposal.TypeDecision, Payload: payload})
	if err != nil {
		t.Fatalf("seeding TypeDecision proposal: %v", err)
	}

	r := callConfirmProposal(t, s, row.ID.String(), "accept")
	if !r.IsError {
		t.Fatalf("expected tag-noise rejection, got success: %s", resultText(r))
	}

	prop, err := s.proposal.Get(ctx, row.ID)
	if err != nil {
		t.Fatalf("fetching proposal after failed accept: %v", err)
	}
	if prop.Status != string(proposal.StatusPending) {
		t.Errorf("proposal.Status = %q, want %q (whole acceptance must roll back)", prop.Status, proposal.StatusPending)
	}

	var count int
	dbRow := sdb.QueryRowContext(ctx, `SELECT COUNT(*) FROM decisions WHERE title = 'sqlite tx decision'`)
	if err := dbRow.Scan(&count); err != nil {
		t.Fatalf("querying decisions: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 decisions rows after rollback, got %d", count)
	}
}

// countingProposalStore counts how many times ListPendingPage is invoked, so
// a rejected limit/offset can be proven to short-circuit BEFORE the store is
// ever reached — [F1003-02].
type countingProposalStore struct {
	*stubProposalStore
	listPendingPageCalls int
}

func (s *countingProposalStore) ListPendingPage(_ context.Context, _, _ int32) ([]db.PendingProposal, error) {
	s.listPendingPageCalls++
	return nil, nil
}

// TestHandleListPendingProposals_RejectsFractionalOrNonNumericPaging is
// [F1003-02]'s acceptance proof: limit/offset now go through optionalIntArg
// (tools_context.go), not numberArg (server.go), so a fractional or
// non-numeric value is REJECTED with an exact error string instead of being
// silently truncated and reaching the store (the F9/U12 bug class). The error
// text matches optionalIntArg's own existing output — the same text
// list_active_repos' sibling (parseRepoPagingArgs) already produces — NOT
// decodeIntField's "got %v" format, which belongs to the seam-migrated
// siblings (list_projects/list_goals) outside this ticket's scope.
func TestHandleListPendingProposals_RejectsFractionalOrNonNumericPaging(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"fractional limit", map[string]any{"limit": 2.5}, "limit must be a whole number"},
		{"fractional offset", map[string]any{"offset": 1.5}, "offset must be a whole number"},
		{"non-numeric limit", map[string]any{"limit": "abc"}, "limit must be a number"},
		{"non-numeric offset", map[string]any{"offset": "abc"}, "offset must be a number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spy := &countingProposalStore{stubProposalStore: &stubProposalStore{}}
			s := &Server{proposal: spy}
			req := mcpmsg.CallToolRequest{}
			req.Params.Arguments = tc.args
			r, err := s.handleListPendingProposals(context.Background(), req)
			if err != nil {
				t.Fatalf("handleListPendingProposals error: %v", err)
			}
			if !r.IsError {
				t.Fatalf("expected error result, got: %s", resultText(r))
			}
			if resultText(r) != tc.want {
				t.Errorf("message = %q, want %q", resultText(r), tc.want)
			}
			if spy.listPendingPageCalls != 0 {
				t.Errorf("ListPendingPage called %d times, want 0 — a rejected paging arg must "+
					"never reach s.proposal.ListPendingPage", spy.listPendingPageCalls)
			}
		})
	}
}

// TestHandleListPendingProposals_OmittedPagingArgsStillSucceed pins
// optionalIntArg's absent-key branch returning (0, nil) for both limit and
// offset — [F1003-02]'s negative case: an implementation that made these
// required (requireIntArg instead of optionalIntArg) would break every
// existing caller that omits them.
func TestHandleListPendingProposals_OmittedPagingArgsStillSucceed(t *testing.T) {
	t.Parallel()
	spy := &countingProposalStore{stubProposalStore: &stubProposalStore{}}
	s := &Server{proposal: spy}
	r, err := s.handleListPendingProposals(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleListPendingProposals error: %v", err)
	}
	if r.IsError {
		t.Fatalf("omitted limit/offset must still succeed, got: %s", resultText(r))
	}
	if spy.listPendingPageCalls != 1 {
		t.Errorf("ListPendingPage called %d times, want 1", spy.listPendingPageCalls)
	}
}
