package mcp

import (
	"context"
	"errors"
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
	out string
	err error
}

func (s *stubDrafterClient) Name() string { return "stub" }
func (s *stubDrafterClient) CompleteJSON(_ context.Context, _ llm.JSONRequest) (string, error) {
	return s.out, s.err
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
	req.Params.Arguments = map[string]any{"title": "Ship feature"}

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

// Ensure the stub stores satisfy their contracts.
var (
	_ discipline.Store    = (*stubProposerDisciplineStore)(nil)
	_ proposal.StoreIface = (*stubProposalStore)(nil)
)
