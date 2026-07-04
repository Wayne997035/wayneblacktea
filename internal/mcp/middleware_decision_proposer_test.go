package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/Wayne997035/wayneblacktea/internal/llm"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// testTool is the canonical mutating tool name used across test cases.
// Constant so a single literal change covers every fireProposer call.
const testTool = "add_task"

// stubProposerDisciplineStore is a minimal discipline.Store implementation used by
// the proposer middleware tests. It only needs RecentDecisionTimes.
type stubProposerDisciplineStore struct {
	times []time.Time
	err   error
}

func (s *stubProposerDisciplineStore) Insert(_ context.Context, _ discipline.InsertParams) error {
	return nil
}

func (s *stubProposerDisciplineStore) RecentMutating(_ context.Context, _ time.Time, _ int) ([]discipline.Event, error) {
	return nil, nil
}

func (s *stubProposerDisciplineStore) RecentDecisionTimes(_ context.Context, _ string, _ time.Time) ([]time.Time, error) {
	return s.times, s.err
}

// stubProposalStore captures Create calls for assertion.
type stubProposalStore struct {
	mu       sync.Mutex
	created  []proposal.CreateParams
	createOk *db.PendingProposal
	createEr error
}

func (s *stubProposalStore) Create(_ context.Context, p proposal.CreateParams) (*db.PendingProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, p)
	if s.createEr != nil {
		return nil, s.createEr
	}
	if s.createOk != nil {
		return s.createOk, nil
	}
	return &db.PendingProposal{}, nil
}

func (s *stubProposalStore) Get(_ context.Context, _ uuid.UUID) (*db.PendingProposal, error) {
	return nil, nil
}

func (s *stubProposalStore) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return nil, nil
}

func (s *stubProposalStore) ListAll(_ context.Context, _ string, _ int32) ([]db.PendingProposal, error) {
	return nil, nil
}

func (s *stubProposalStore) Resolve(_ context.Context, _ uuid.UUID, _ proposal.Status) (*db.PendingProposal, error) {
	return nil, nil
}

func (s *stubProposalStore) BatchConfirm(_ context.Context, _ []uuid.UUID, _ proposal.Status) (proposal.BatchConfirmResult, error) {
	return proposal.BatchConfirmResult{}, nil
}

func (s *stubProposalStore) AutoProposeConceptFromKnowledge(_ context.Context, _ *db.KnowledgeItem, _ string) (*db.PendingProposal, error) {
	return nil, nil
}

// snapshotCreated returns a copy of the captured create params, safe for read.
func (s *stubProposalStore) snapshotCreated() []proposal.CreateParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]proposal.CreateParams, len(s.created))
	copy(out, s.created)
	return out
}

// stubDrafterClient drives the LLM chain inside DecisionDrafter.
type stubDrafterClient struct {
	mu       sync.Mutex
	out      string
	err      error
	panicNow bool
	requests []llm.JSONRequest
}

func (s *stubDrafterClient) Name() string { return "stub" }
func (s *stubDrafterClient) CompleteJSON(_ context.Context, req llm.JSONRequest) (string, error) {
	if s.panicNow {
		panic("drafter panic")
	}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	return s.out, s.err
}

func (s *stubDrafterClient) snapshotRequests() []llm.JSONRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]llm.JSONRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// newProposerServer builds a minimal *Server pointed at the supplied stubs.
// Only the fields the middleware reads are populated; everything else is nil.
func newProposerServer(disc *stubProposerDisciplineStore, prop *stubProposalStore, drafter *ai.DecisionDrafter) *Server {
	return &Server{
		discipline: disc,
		proposal:   prop,
		drafter:    drafter,
		sessionID:  "test-session-1",
	}
}

// fireProposer invokes the middleware once with `tool`, then waits up to
// 1 s for the background goroutine to complete (polled via the stub).
// Returns the captured proposal Creates.
func fireProposer(t *testing.T, srv *Server, tool string) []proposal.CreateParams {
	return fireProposerWithArgs(t, srv, tool, map[string]any{"title": "Ship feature"})
}

func fireProposerWithArgs(t *testing.T, srv *Server, tool string, args map[string]any) []proposal.CreateParams {
	t.Helper()
	mw := srv.decisionProposerMiddleware()
	handler := mw(func(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		// Simulate a successful tool call returning text content.
		return &mcpmsg.CallToolResult{
			Content: []mcpmsg.Content{mcpmsg.TextContent{Type: "text", Text: "ok"}},
		}, nil
	})
	req := mcpmsg.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args

	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("middleware handler: %v", err)
	}

	// Poll for the background goroutine to flush its work.
	prop, _ := srv.proposal.(*stubProposalStore)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if got := prop.snapshotCreated(); len(got) > 0 {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return prop.snapshotCreated()
}

func resetDecisionProposerBudgetForTest(t *testing.T, now time.Time) {
	t.Helper()
	mcpDecisionProposerBudget.mu.Lock()
	mcpDecisionProposerBudget.tokens = mcpDecisionProposerMaxPerWindow
	mcpDecisionProposerBudget.resetAt = now.Add(mcpDecisionProposerWindow)
	mcpDecisionProposerBudget.mu.Unlock()
	t.Cleanup(func() {
		mcpDecisionProposerBudget.mu.Lock()
		mcpDecisionProposerBudget.tokens = mcpDecisionProposerMaxPerWindow
		mcpDecisionProposerBudget.resetAt = time.Time{}
		mcpDecisionProposerBudget.mu.Unlock()
	})
}

func TestDecisionProposer_HappyPath_InsertsProposal(t *testing.T) {
	disc := &stubProposerDisciplineStore{} // no recent decisions in window
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"Ship X","decision":"Adopt approach Y","rationale":"trade-off Z","alternatives":["a","b"]}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, testTool)

	if len(got) != 1 {
		t.Fatalf("expected 1 proposal, got %d", len(got))
	}
	if got[0].Type != proposal.TypeDecision {
		t.Errorf("type = %q, want %q", got[0].Type, proposal.TypeDecision)
	}
	if got[0].ProposedBy != "wayneblacktea-auto-decision" {
		t.Errorf("proposed_by = %q, want %q", got[0].ProposedBy, "wayneblacktea-auto-decision")
	}
	if len(got[0].Payload) == 0 {
		t.Errorf("payload is empty")
	}
}

func TestDecisionProposer_RecentDecisionInWindow_NoInsert(t *testing.T) {
	disc := &stubProposerDisciplineStore{
		// User logged a decision 5 minutes ago — middleware MUST skip.
		times: []time.Time{time.Now().Add(-5 * time.Minute)},
	}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"would have proposed"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, testTool)

	if len(got) != 0 {
		t.Errorf("expected 0 proposals (recent decision in window), got %d", len(got))
	}
}

func TestDecisionProposer_DrafterReturnsEmptyTitle_NoInsert(t *testing.T) {
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	// Drafter declines (routine activity).
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"","decision":"","rationale":""}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, testTool)

	if len(got) != 0 {
		t.Errorf("expected 0 proposals (empty title from drafter), got %d", len(got))
	}
}

func TestDecisionProposer_NonMutatingTool_NoInsert(t *testing.T) {
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"x"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, "list_decisions") // not in MutatingTools

	if len(got) != 0 {
		t.Errorf("expected 0 proposals (read-only tool), got %d", len(got))
	}
}

func TestDecisionProposer_LogDecisionTrigger_NoInsert(t *testing.T) {
	// log_decision is in MutatingTools but MUST be skipped: emitting a
	// decision proposal in response to a log_decision is silly recursion.
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"x"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, "log_decision")

	if len(got) != 0 {
		t.Errorf("expected 0 proposals (log_decision self-trigger), got %d", len(got))
	}
}

func TestDecisionProposer_ConfirmProposalsTrigger_NoInsert(t *testing.T) {
	// confirm_proposals (plural, bulk accept/reject) is in MutatingTools but
	// MUST be skipped for the same reason as its singular counterpart
	// confirm_proposal: emitting a decision proposal in response to a
	// proposal confirmation is redundant recursion.
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"x"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, "confirm_proposals")

	if len(got) != 0 {
		t.Errorf("expected 0 proposals (confirm_proposals self-trigger), got %d", len(got))
	}
}

func TestDecisionProposer_ConfirmProposalTrigger_NoInsert(t *testing.T) {
	// confirm_proposal (singular) MUST also be skipped — pre-existing
	// behavior, tested here alongside its plural counterpart above so the
	// full skip-list condition has direct coverage for every listed tool.
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"x"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, "confirm_proposal")

	if len(got) != 0 {
		t.Errorf("expected 0 proposals (confirm_proposal self-trigger), got %d", len(got))
	}
}

func TestDecisionProposer_DisciplineError_NoInsert_NoCrash(t *testing.T) {
	disc := &stubProposerDisciplineStore{err: errors.New("db down")}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"x"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, testTool)

	if len(got) != 0 {
		t.Errorf("expected 0 proposals (discipline lookup failed), got %d", len(got))
	}
}

func TestDecisionProposer_OptOutEnvDisables(t *testing.T) {
	t.Setenv(disableAutoDecisionsEnvVar, "1")

	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"x","decision":"y","rationale":"z"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, testTool)

	if len(got) != 0 {
		t.Errorf("expected 0 proposals when WBT_DISABLE_AUTO_DECISIONS=1, got %d", len(got))
	}
}

func TestDecisionProposer_EnabledByDefault(t *testing.T) {
	// No env set → enabled.
	t.Setenv(disableAutoDecisionsEnvVar, "")
	if !decisionProposerEnabled() {
		t.Errorf("default state should be ENABLED (opt-out semantics)")
	}
}

func TestDecisionProposer_OptOutValueMatrix(t *testing.T) {
	// M-3 fix: opt-OUT is now strict — only the explicit truthy set
	// {1,true,yes,on} (case-insensitive) disables the proposer. Empty value
	// and any unrecognised string leave it enabled. This eliminates the
	// surprising "any non-falsy value disables" behaviour that previously
	// turned the feature off for unrelated env values like "auto" or
	// "default".
	cases := []struct {
		val  string
		want bool // true = enabled
	}{
		{"", true},
		{"0", true},
		{"false", true},
		{"FALSE", true},
		{"no", true},
		{"off", true},
		{"random", true}, // unknown non-empty = enabled (strict opt-out)
		{"auto", true},
		{"default", true},
		{"1", false},
		{"true", false},
		{"TRUE", false},
		{"yes", false},
		{"YES", false},
		{"on", false},
		{"ON", false},
	}
	for _, tc := range cases {
		t.Run("val="+tc.val, func(t *testing.T) {
			t.Setenv(disableAutoDecisionsEnvVar, tc.val)
			if got := decisionProposerEnabled(); got != tc.want {
				t.Errorf("enabled = %v, want %v for env value %q", got, tc.want, tc.val)
			}
		})
	}
}

func TestDecisionProposer_NilDeps_NoCrash(t *testing.T) {
	// drafter / discipline / proposal all nil → middleware MUST be a no-op.
	srv := &Server{sessionID: "test"}
	mw := srv.decisionProposerMiddleware()
	handler := mw(func(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		return &mcpmsg.CallToolResult{}, nil
	})
	req := mcpmsg.CallToolRequest{}
	req.Params.Name = testTool
	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("nil-deps handler: %v", err)
	}
}

// TestDecisionProposer_ToolErrorNotMutating verifies that when the tool
// returns IsError=true, the middleware does not record a proposal — only
// successful mutations should propose decisions.
func TestDecisionProposer_ToolErrorResult_NoInsert(t *testing.T) {
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"x"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	mw := srv.decisionProposerMiddleware()
	handler := mw(func(_ context.Context, _ mcpmsg.CallToolRequest) (*mcpmsg.CallToolResult, error) {
		return &mcpmsg.CallToolResult{IsError: true}, nil
	})
	req := mcpmsg.CallToolRequest{}
	req.Params.Name = testTool
	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Give the (would-be) goroutine a moment to execute, then verify nothing.
	time.Sleep(50 * time.Millisecond)
	if got := prop.snapshotCreated(); len(got) != 0 {
		t.Errorf("expected 0 proposals on tool-error result, got %d", len(got))
	}
}

func TestDecisionProposer_RateLimitDropsExcess(t *testing.T) {
	resetDecisionProposerBudgetForTest(t, time.Now())
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"rate limited","decision":"d","rationale":"r"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	for i := 0; i < mcpDecisionProposerMaxPerWindow+10; i++ {
		_ = fireProposer(t, srv, testTool)
		wantSoFar := i + 1
		if wantSoFar > mcpDecisionProposerMaxPerWindow {
			wantSoFar = mcpDecisionProposerMaxPerWindow
		}
		waitForCreatedCount(t, prop, wantSoFar)
	}
	got := prop.snapshotCreated()
	if len(got) > mcpDecisionProposerMaxPerWindow {
		t.Fatalf("created %d proposals, want <= %d", len(got), mcpDecisionProposerMaxPerWindow)
	}
	if len(got) != mcpDecisionProposerMaxPerWindow {
		t.Fatalf("created %d proposals, want %d before drops", len(got), mcpDecisionProposerMaxPerWindow)
	}
}

func waitForCreatedCount(t *testing.T, prop *stubProposalStore, want int) {
	t.Helper()
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if got := len(prop.snapshotCreated()); got >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("created %d proposals, want at least %d", len(prop.snapshotCreated()), want)
}

func TestDecisionProposer_RateLimitWindowRollover(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 0, 0, 0, time.UTC)
	resetDecisionProposerBudgetForTest(t, now)
	for i := 0; i < mcpDecisionProposerMaxPerWindow; i++ {
		if !tryAcquireDecisionProposerToken(now) {
			t.Fatalf("token %d rejected before quota exhausted", i)
		}
	}
	if tryAcquireDecisionProposerToken(now) {
		t.Fatal("token accepted after quota exhausted")
	}
	if !tryAcquireDecisionProposerToken(now.Add(mcpDecisionProposerWindow + time.Nanosecond)) {
		t.Fatal("token rejected after window rollover")
	}
}

func TestDecisionProposer_SemaphoreFullDropsSilently(t *testing.T) {
	for i := 0; i < cap(mcpDecisionProposerSem); i++ {
		mcpDecisionProposerSem <- struct{}{}
	}
	t.Cleanup(func() {
		for i := 0; i < cap(mcpDecisionProposerSem); i++ {
			<-mcpDecisionProposerSem
		}
	})

	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{out: `{"title":"x"}`})
	srv := newProposerServer(disc, prop, drafter)
	got := fireProposer(t, srv, testTool)
	if len(got) != 0 {
		t.Fatalf("created %d proposals while semaphore was full, want 0", len(got))
	}
}

func TestDecisionProposer_DrafterPanicRecoveredViaSlogWarn(t *testing.T) {
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{panicNow: true})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposer(t, srv, testTool)
	if len(got) != 0 {
		t.Fatalf("created %d proposals after drafter panic, want 0", len(got))
	}
}

func TestDecisionProposer_RedactsCredentialsBeforeLLM(t *testing.T) {
	client := &stubDrafterClient{out: `{"title":"redacted","decision":"d","rationale":"r"}`}
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	srv := newProposerServer(disc, prop, ai.NewDecisionDrafter(client))

	got := fireProposerWithArgs(t, srv, testTool, map[string]any{
		"api_key": "sk_live_test123",
		"title":   "Ship feature",
	})
	if len(got) != 1 {
		t.Fatalf("created %d proposals, want 1", len(got))
	}
	requests := client.snapshotRequests()
	if len(requests) != 1 {
		t.Fatalf("captured %d LLM requests, want 1", len(requests))
	}
	if strings.Contains(requests[0].User, "sk_live_test123") {
		t.Fatalf("LLM prompt leaked credential: %s", requests[0].User)
	}
	if !strings.Contains(requests[0].User, "[REDACTED:stripe-key]") {
		t.Fatalf("LLM prompt missing redaction marker: %s", requests[0].User)
	}
}

func TestDecisionProposer_AdversarialToolInputNoCrash(t *testing.T) {
	disc := &stubProposerDisciplineStore{}
	prop := &stubProposalStore{}
	drafter := ai.NewDecisionDrafter(&stubDrafterClient{
		out: `{"title":"adversarial","decision":"d","rationale":"r"}`,
	})
	srv := newProposerServer(disc, prop, drafter)

	got := fireProposerWithArgs(t, srv, testTool, map[string]any{
		"title": strings.Repeat("攻", 1024*1024),
	})
	if len(got) != 1 {
		t.Fatalf("created %d proposals, want 1", len(got))
	}
}

// Ensure the stub stores satisfy their contracts.
var (
	_ discipline.Store    = (*stubProposerDisciplineStore)(nil)
	_ proposal.StoreIface = (*stubProposalStore)(nil)
)

// TestMarshalArgsDeterministic_StableKeyOrder verifies that the helper
// returns the SAME JSON string for the SAME map across many invocations.
// fmt.Sprintf("%v", args) on a Go map returns keys in randomised order; the
// deterministic helper sorts keys via encoding/json so any downstream prompt
// or test snapshot is reproducible.
func TestMarshalArgsDeterministic_StableKeyOrder(t *testing.T) {
	args := map[string]any{
		"zulu":  "z",
		"alpha": 1,
		"mike":  true,
		"echo":  []string{"e1", "e2"},
		"delta": map[string]any{"nested": "value"},
	}
	first := marshalArgsDeterministic(args)
	for i := 0; i < 50; i++ {
		got := marshalArgsDeterministic(args)
		if got != first {
			t.Fatalf("non-deterministic output across calls:\n  first: %s\n  got %d: %s", first, i, got)
		}
	}
	// Sanity: keys should be sorted alphabetically (json.Marshal default for
	// map[string]any). Verify alpha comes before zulu.
	if first == "" {
		t.Fatal("empty result")
	}
	alphaIdx := indexOf(first, `"alpha"`)
	zuluIdx := indexOf(first, `"zulu"`)
	if alphaIdx < 0 || zuluIdx < 0 || alphaIdx >= zuluIdx {
		t.Errorf("keys not in sorted order: %s", first)
	}
}

// TestMarshalArgsDeterministic_EmptyAndError verifies the edge cases:
// empty map returns "{}" and unmarshalable input falls back to %v form.
func TestMarshalArgsDeterministic_EmptyAndError(t *testing.T) {
	if got := marshalArgsDeterministic(map[string]any{}); got != "{}" {
		t.Errorf("empty map: got %q, want {}", got)
	}
	if got := marshalArgsDeterministic(nil); got != "{}" {
		t.Errorf("nil map: got %q, want {}", got)
	}
	// chan can't be marshaled — verifies the fallback returns SOMETHING
	// non-empty rather than panicking.
	bad := map[string]any{"ch": make(chan int)}
	if got := marshalArgsDeterministic(bad); got == "" {
		t.Errorf("unmarshalable input: empty result, expected fallback string")
	}
}

// indexOf is a tiny strings.Index alias used to keep the test readable
// without importing strings just for one call (the package already has many
// helpers; keeping it local avoids a single import).
func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
