package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// ---------------------------------------------------------------------------
// M-NEW-1: TypeDecision must materialise via the batch path, not just flip
// status. Mirrors the singular roundtrip test in
// internal/proposal/decision_roundtrip_test.go for the batch HTTP endpoint.
// ---------------------------------------------------------------------------

const batchRoundTripDecisionTitle = "Batch-accept decision: enable HTTP/3"

// fakeBatchDecisionStore records each Log call so the test can assert
// materialisation happened (or did NOT, on bad-payload / disabled-store
// paths). Returns a fresh decision row keyed by the per-test counter.
type fakeBatchDecisionStore struct {
	mu     sync.Mutex
	logged []decision.LogParams
	// errOnLog forces Log to return an error so tests can exercise the
	// materialise-failure branch.
	errOnLog error
}

func (f *fakeBatchDecisionStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.errOnLog != nil {
		return nil, f.errOnLog
	}
	f.logged = append(f.logged, p)
	return &db.Decision{
		ID:        uuid.New(),
		Title:     p.Title,
		Context:   p.Context,
		Decision:  p.Decision,
		Rationale: p.Rationale,
	}, nil
}

func newDecisionProposalRow(t *testing.T, id uuid.UUID, title string) db.PendingProposal {
	t.Helper()
	payload, err := json.Marshal(proposal.DecisionProposerPayload{
		Title:        title,
		Decision:     "Adopt the new transport protocol across edge.",
		Rationale:    "Reduces RTT for mobile users in APAC.",
		Alternatives: []string{"keep HTTP/2", "experiment with QUIC subset"},
		SessionID:    "batch-roundtrip-session",
		TriggerTool:  "complete_task",
	})
	if err != nil {
		t.Fatalf("marshal decision payload: %v", err)
	}
	return db.PendingProposal{
		ID:      id,
		Type:    string(proposal.TypeDecision),
		Status:  string(proposal.StatusPending),
		Payload: payload,
	}
}

// TestProposal_AcceptBatch_TypeDecision_HTTP exercises the HTTP batch confirm
// path with a TypeDecision proposal — the path the round-2 reviewer flagged
// as a still-broken C-1 class data loss (status flips to accepted, but no
// decisions row was written).
func TestProposal_AcceptBatch_TypeDecision_HTTP(t *testing.T) {
	decisionID := uuid.New()
	conceptID := uuid.New()
	badPayloadID := uuid.New()

	decisionProp := newDecisionProposalRow(t, decisionID, batchRoundTripDecisionTitle)
	conceptProp := makeConceptProposal(conceptID, "Spaced repetition", "Daily review beats cramming.", []string{"learning"})
	badDecisionProp := db.PendingProposal{
		ID:      badPayloadID,
		Type:    string(proposal.TypeDecision),
		Status:  string(proposal.StatusPending),
		Payload: []byte(`{not valid json`),
	}

	cases := []batchDecisionCase{
		{
			name:                 "happy: 1 TypeDecision in batch → decision logged + status flipped",
			body:                 `{"ids":["` + decisionID.String() + `"],"action":"accept"}`,
			store:                newFakeProposalStore(decisionProp),
			decisionStore:        &fakeBatchDecisionStore{},
			wireDecisionStore:    true,
			wantOKIDs:            []uuid.UUID{decisionID},
			wantDecisionLogCount: 1,
		},
		{
			name:                 "mixed: TypeDecision + TypeConcept → both materialise",
			body:                 `{"ids":["` + decisionID.String() + `","` + conceptID.String() + `"],"action":"accept"}`,
			store:                newFakeProposalStore(decisionProp, conceptProp),
			decisionStore:        &fakeBatchDecisionStore{},
			wireDecisionStore:    true,
			wantOKIDs:            []uuid.UUID{decisionID, conceptID},
			wantDecisionLogCount: 1,
		},
		{
			name:                 "bad payload + good in same batch → bad ID errors, good ID proceeds",
			body:                 `{"ids":["` + badPayloadID.String() + `","` + decisionID.String() + `"],"action":"accept"}`,
			store:                newFakeProposalStore(badDecisionProp, decisionProp),
			decisionStore:        &fakeBatchDecisionStore{},
			wireDecisionStore:    true,
			wantOKIDs:            []uuid.UUID{decisionID},
			wantFailIDs:          []uuid.UUID{badPayloadID},
			wantDecisionLogCount: 1, // only the good payload was logged
		},
		{
			name:                 "decision store not wired → TypeDecision id fails, no resolve",
			body:                 `{"ids":["` + decisionID.String() + `"],"action":"accept"}`,
			store:                newFakeProposalStore(decisionProp),
			decisionStore:        &fakeBatchDecisionStore{},
			wireDecisionStore:    false,
			wantOKIDs:            nil,
			wantFailIDs:          []uuid.UUID{decisionID},
			wantDecisionLogCount: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runBatchDecisionCase(t, tc)
		})
	}
}

// batchDecisionCase is the table-row shape for the HTTP batch decision tests.
// Lifted to a named type so the per-case runner is a top-level func with low
// cyclomatic complexity (otherwise the whole table-driven block exceeds the
// gocyclo threshold).
type batchDecisionCase struct {
	name                 string
	body                 string
	store                *fakeProposalStore
	decisionStore        *fakeBatchDecisionStore
	wireDecisionStore    bool
	wantOKIDs            []uuid.UUID
	wantFailIDs          []uuid.UUID
	wantDecisionLogCount int
}

// batchConfirmTestResult mirrors the JSON shape returned by ConfirmBatch.
// Lifted out of the test body so the test body stays linear.
type batchConfirmTestResult struct {
	Results []struct {
		ID      string `json:"id"`
		OK      bool   `json:"ok"`
		Skipped bool   `json:"skipped"`
		Error   string `json:"error"`
	} `json:"results"`
}

// runBatchDecisionCase is the per-case runner for
// TestProposal_AcceptBatch_TypeDecision_HTTP. Extracted so the table loop
// stays trivial and gocyclo on the test func stays inside the threshold.
func runBatchDecisionCase(t *testing.T, tc batchDecisionCase) {
	t.Helper()
	h := handler.NewProposalHandler(tc.store, &fakeProposalLearningStore{concept: &db.Concept{ID: uuid.New()}})
	if tc.wireDecisionStore {
		h = h.WithDecision(tc.decisionStore)
	}
	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", tc.body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp batchConfirmTestResult
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	assertBatchOutcome(t, tc, resp)
}

// assertBatchOutcome verifies the per-id OK/Fail outcomes and the decision
// store call count. Pure helper — kept outside runBatchDecisionCase for
// further cyclomatic-budget headroom.
func assertBatchOutcome(t *testing.T, tc batchDecisionCase, resp batchConfirmTestResult) {
	t.Helper()
	okIDs := map[string]bool{}
	failIDs := map[string]bool{}
	for _, r := range resp.Results {
		if r.OK {
			okIDs[r.ID] = true
		} else {
			failIDs[r.ID] = true
		}
	}
	for _, id := range tc.wantOKIDs {
		if !okIDs[id.String()] {
			t.Errorf("expected OK for %s, got results=%+v", id, resp.Results)
		}
	}
	for _, id := range tc.wantFailIDs {
		if !failIDs[id.String()] {
			t.Errorf("expected fail for %s, got results=%+v", id, resp.Results)
		}
	}
	if got := len(tc.decisionStore.logged); got != tc.wantDecisionLogCount {
		t.Errorf("decision.Log calls = %d, want %d (logged=%+v)", got, tc.wantDecisionLogCount, tc.decisionStore.logged)
	}
	if tc.wantDecisionLogCount > 0 && len(tc.decisionStore.logged) > 0 {
		logged := tc.decisionStore.logged[0]
		if !strings.Contains(logged.Title, "HTTP/3") {
			t.Errorf("logged decision title = %q, want contains 'HTTP/3'", logged.Title)
		}
		if !strings.Contains(logged.Rationale, "RTT") {
			t.Errorf("logged decision rationale = %q, want contains 'RTT'", logged.Rationale)
		}
	}
}

// TestProposal_AcceptBatch_TypeDecision_HTTP_OrphanOnResolveRace verifies the
// materialise-then-resolve ordering: when Resolve fails because a concurrent
// accept already flipped the row, the decision row is retained (orphan) and
// the per-row entry surfaces the conflict. This mirrors the singular-path
// trade-off documented in acceptDecision.
func TestProposal_AcceptBatch_TypeDecision_HTTP_OrphanOnResolveRace(t *testing.T) {
	decisionID := uuid.New()
	decisionProp := newDecisionProposalRow(t, decisionID, "race-orphan check")

	store := newFakeProposalStore(decisionProp)
	// Simulate concurrent accept: the row is missing from the resolve path.
	store.resolveErr = proposal.ErrNotFound
	decStore := &fakeBatchDecisionStore{}

	h := handler.NewProposalHandler(store, &fakeProposalLearningStore{}).WithDecision(decStore)
	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + decisionID.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			ID      string `json:"id"`
			OK      bool   `json:"ok"`
			Skipped bool   `json:"skipped"`
			Error   string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(resp.Results))
	}
	r := resp.Results[0]
	if r.OK {
		t.Errorf("expected OK=false on resolve race, got %+v", r)
	}
	if !r.Skipped {
		t.Errorf("expected Skipped=true on resolve race, got %+v", r)
	}
	// Critical: decision MUST have been logged before the resolve race —
	// orphan-decision-over-phantom-accepted.
	if len(decStore.logged) != 1 {
		t.Errorf("decision.Log calls = %d, want 1 (orphan retained)", len(decStore.logged))
	}
}

// TestProposal_AcceptBatch_TypeDecision_HTTP_RejectStillSkipsMaterialise
// guards against accidentally invoking the materialise path on the reject
// branch — reject MUST stay a pure status flip.
func TestProposal_AcceptBatch_TypeDecision_HTTP_RejectStillSkipsMaterialise(t *testing.T) {
	decisionID := uuid.New()
	decisionProp := newDecisionProposalRow(t, decisionID, "reject-no-materialise check")

	store := newFakeProposalStore(decisionProp)
	decStore := &fakeBatchDecisionStore{}
	h := handler.NewProposalHandler(store, &fakeProposalLearningStore{}).WithDecision(decStore)
	e := echo.New()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + decisionID.String() + `"],"action":"reject"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(decStore.logged) != 0 {
		t.Errorf("reject must not invoke decision.Log; got %d calls", len(decStore.logged))
	}
}
