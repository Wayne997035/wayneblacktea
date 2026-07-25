package proposal_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// roundTripTitle is the canonical decision title each subtest uses to
// assert the materialised row matches the proposer payload.
const roundTripTitle = "Adopt HTTP/2 server-push for project pages"

func newDecisionPayload(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(proposal.DecisionProposerPayload{
		Title:        roundTripTitle,
		Decision:     "Use SSE-style streaming via Echo's c.Response().Flush()",
		Rationale:    "Avoids websocket complexity; covers our 99% read pattern.",
		Alternatives: []string{"raw WebSocket", "long-poll", "GraphQL subscriptions"},
		SessionID:    "round-trip-test-session",
		TriggerTool:  "complete_task",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return body
}

// ---------------------------------------------------------------------------
// HTTP path — TypeDecision accept goes through internal/handler/proposal_handler.go
// ---------------------------------------------------------------------------

// fakeDecisionStore is a minimal proposalDecisionStore implementation that
// records calls + lets tests drive the materialised result. The store holds
// the persisted db.Decision in memory keyed by ID; the round-trip test
// asserts ID matches what the handler returned.
type fakeDecisionStore struct {
	mu       sync.Mutex
	logged   []decision.LogParams
	returnID uuid.UUID
}

func (s *fakeDecisionStore) Log(_ context.Context, p decision.LogParams) (*db.Decision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logged = append(s.logged, p)
	id := s.returnID
	if id == (uuid.UUID{}) {
		id = uuid.New()
	}
	row := &db.Decision{
		ID:        id,
		Title:     p.Title,
		Context:   p.Context,
		Decision:  p.Decision,
		Rationale: p.Rationale,
	}
	return row, nil
}

// fakeProposalStoreForDecision is a proposal.StoreIface stub holding a
// single TypeDecision row that the HTTP handler can read + resolve.
type fakeProposalStoreForDecision struct {
	mu         sync.Mutex
	row        *db.PendingProposal
	resolveErr error
}

func (s *fakeProposalStoreForDecision) Create(_ context.Context, _ proposal.CreateParams) (*db.PendingProposal, error) {
	return nil, nil
}

func (s *fakeProposalStoreForDecision) Get(_ context.Context, id uuid.UUID) (*db.PendingProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.row == nil || s.row.ID != id {
		return nil, proposal.ErrNotFound
	}
	clone := *s.row
	return &clone, nil
}

func (s *fakeProposalStoreForDecision) ListPending(_ context.Context) ([]db.PendingProposal, error) {
	return nil, nil
}

func (s *fakeProposalStoreForDecision) ListAll(_ context.Context, _ string, _ int32) ([]db.PendingProposal, error) {
	return nil, nil
}

func (s *fakeProposalStoreForDecision) Resolve(_ context.Context, id uuid.UUID, status proposal.Status) (*db.PendingProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.resolveErr != nil {
		return nil, s.resolveErr
	}
	if s.row == nil || s.row.ID != id {
		return nil, proposal.ErrNotFound
	}
	if s.row.Status != string(proposal.StatusPending) {
		return nil, proposal.ErrNotFound
	}
	s.row.Status = string(status)
	clone := *s.row
	return &clone, nil
}

func (s *fakeProposalStoreForDecision) BatchConfirm(
	_ context.Context, _ []uuid.UUID, _ proposal.Status,
) (proposal.BatchConfirmResult, error) {
	return proposal.BatchConfirmResult{}, nil
}

func (s *fakeProposalStoreForDecision) AutoProposeConceptFromKnowledge(
	_ context.Context, _ *db.KnowledgeItem, _ string,
) (*db.PendingProposal, error) {
	return nil, nil
}

// fakeLearningForDecision is required to satisfy NewProposalHandler — never
// invoked on the TypeDecision path.
type fakeLearningForDecision struct{}

func (fakeLearningForDecision) CreateConcept(_ context.Context, _, _ string, _ []string) (*db.Concept, error) {
	return &db.Concept{}, nil
}

// TestProposal_Accept_TypeDecision_Roundtrip_HTTP exercises the HTTP accept
// path end-to-end: a TypeDecision row is "in" pending_proposals (fake), the
// handler accepts it, materialises a decisions row via the decision store,
// and marks the proposal accepted. Asserts the persisted decision title /
// decision text / rationale match the proposer payload.
func TestProposal_Accept_TypeDecision_Roundtrip_HTTP(t *testing.T) {
	propID := uuid.New()
	payload := newDecisionPayload(t)
	store := &fakeProposalStoreForDecision{
		row: &db.PendingProposal{
			ID:      propID,
			Type:    string(proposal.TypeDecision),
			Status:  string(proposal.StatusPending),
			Payload: payload,
		},
	}
	decStore := &fakeDecisionStore{}

	h := handler.NewProposalHandler(store, fakeLearningForDecision{}).WithDecision(decStore)

	e := echo.New()
	body := strings.NewReader(`{"action":"accept"}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/proposals/"+propID.String()+"/confirm", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/proposals/:id/confirm")
	c.SetParamNames("id")
	c.SetParamValues(propID.String())

	if err := h.ConfirmProposal(c); err != nil {
		t.Fatalf("ConfirmProposal: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Decision store MUST have been called exactly once with payload data.
	if len(decStore.logged) != 1 {
		t.Fatalf("decision.Log calls = %d, want 1", len(decStore.logged))
	}
	got := decStore.logged[0]
	if got.Title != roundTripTitle {
		t.Errorf("title = %q, want %q", got.Title, roundTripTitle)
	}
	if !strings.Contains(got.Decision, "SSE-style streaming") {
		t.Errorf("decision body unexpected: %q", got.Decision)
	}
	if !strings.Contains(got.Rationale, "websocket complexity") {
		t.Errorf("rationale missing source text: %q", got.Rationale)
	}
	// Alternatives MUST be appended into the rationale tail.
	for _, alt := range []string{"raw WebSocket", "long-poll", "GraphQL subscriptions"} {
		if !strings.Contains(got.Rationale, alt) {
			t.Errorf("rationale missing alternative %q: %q", alt, got.Rationale)
		}
	}

	// Proposal MUST be marked accepted.
	pp, gErr := store.Get(context.Background(), propID)
	if gErr != nil {
		t.Fatalf("post-confirm Get: %v", gErr)
	}
	if pp.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal status = %q, want accepted", pp.Status)
	}

	// Response payload contains the decision row.
	var resp struct {
		Decision *db.Decision `json:"decision"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Decision == nil || resp.Decision.Title != roundTripTitle {
		t.Errorf("response decision missing or wrong: %+v", resp.Decision)
	}
}

// TestProposal_Accept_TypeDecision_Roundtrip_HTTP_BadPayload verifies
// malformed JSON in the proposal payload yields a 400 (not a server error)
// and the proposal is NOT resolved.
func TestProposal_Accept_TypeDecision_Roundtrip_HTTP_BadPayload(t *testing.T) {
	propID := uuid.New()
	store := &fakeProposalStoreForDecision{
		row: &db.PendingProposal{
			ID:      propID,
			Type:    string(proposal.TypeDecision),
			Status:  string(proposal.StatusPending),
			Payload: []byte(`{this is not json`),
		},
	}
	decStore := &fakeDecisionStore{}
	h := handler.NewProposalHandler(store, fakeLearningForDecision{}).WithDecision(decStore)

	e := echo.New()
	body := strings.NewReader(`{"action":"accept"}`)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/proposals/"+propID.String()+"/confirm", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/proposals/:id/confirm")
	c.SetParamNames("id")
	c.SetParamValues(propID.String())

	if err := h.ConfirmProposal(c); err != nil {
		t.Fatalf("ConfirmProposal: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d (want 400 for bad payload), body = %s", rec.Code, rec.Body.String())
	}
	if len(decStore.logged) != 0 {
		t.Errorf("decision.Log MUST NOT be called on bad payload, got %d", len(decStore.logged))
	}
	// Proposal MUST still be pending (handler bailed before Resolve).
	pp, _ := store.Get(context.Background(), propID)
	if pp.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending", pp.Status)
	}
}

// ---------------------------------------------------------------------------
// SQLite path — exercises wbtsqlite.DecisionStore.LogTx end-to-end inside a
// transaction, mirroring the MCP confirm_proposal accept flow.
// ---------------------------------------------------------------------------

// TestProposal_Accept_TypeDecision_Roundtrip_SQLite verifies the SQLite Tx
// path: a pending_proposals row of type='decision' is materialised inside a
// single *sql.Tx, the proposal is marked accepted in the same tx, both rows
// commit atomically. Reads back the decision via DecisionStore.All to
// confirm round-trip integrity.
func TestProposal_Accept_TypeDecision_Roundtrip_SQLite(t *testing.T) {
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	propStore := wbtsqlite.NewProposalStore(sdb)
	decStore := wbtsqlite.NewDecisionStore(sdb)

	// 1. Insert a pending TypeDecision row.
	prop, err := propStore.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeDecision,
		Payload:    newDecisionPayload(t),
		ProposedBy: "test",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	// 2. Begin tx, insert decision, resolve proposal, commit — mirrors
	//    Server.acceptProposalSQLite + materializeFromPayloadSQLiteTx.
	tx, err := sdb.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })

	var dp proposal.DecisionProposerPayload
	if err := json.Unmarshal(prop.Payload, &dp); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	logParams := decision.LogParams{
		Title:     dp.Title,
		Context:   "auto-proposed by trigger_tool=" + dp.TriggerTool + " session=" + dp.SessionID,
		Decision:  dp.Decision,
		Rationale: dp.Rationale,
		Source:    decision.SourceAuto,
	}
	if _, err := decStore.LogTx(ctx, tx, logParams); err != nil {
		t.Fatalf("LogTx: %v", err)
	}
	if err := propStore.ResolveTx(ctx, tx, prop.ID, proposal.StatusAccepted); err != nil {
		t.Fatalf("ResolveTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 3. Verify decision was written and matches proposer payload.
	rows, err := decStore.All(ctx, 10)
	if err != nil {
		t.Fatalf("decisions.All: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("decisions count = %d, want 1", len(rows))
	}
	if rows[0].Title != roundTripTitle {
		t.Errorf("title = %q, want %q", rows[0].Title, roundTripTitle)
	}
	if !strings.Contains(rows[0].Decision, "SSE-style streaming") {
		t.Errorf("decision body unexpected: %q", rows[0].Decision)
	}

	// 4. Verify proposal is accepted (single source of truth).
	pp, err := propStore.Get(ctx, prop.ID)
	if err != nil {
		t.Fatalf("proposal Get post-commit: %v", err)
	}
	if pp.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal status = %q, want accepted", pp.Status)
	}
}

// TestProposal_Accept_TypeDecision_Roundtrip_PG runs the same materialise +
// resolve sequence against a real Postgres container so the new
// pending_proposals_type_check (000038) is verified against the same engine
// production uses. Reuses openBatchTestPgPool from
// batch_confirm_postgres_test.go (same package; same skipMigrations map).
//
// Per backend-security-design.md §6.5 dual-backend rule: when SQLite + PG
// integration tests both exist for the same flow, BOTH must run — testcontainers
// PG covers dialect-specific behaviour (CHECK constraint, transaction
// isolation, ROW count) that the SQLite path can't.
func TestProposal_Accept_TypeDecision_Roundtrip_PG(t *testing.T) {
	pool := openBatchTestPgPool(t)
	ctx := context.Background()
	store := proposal.NewStore(pool, nil)

	// 1. Insert a TypeDecision pending row through the public API. This
	//    exercises the new CHECK constraint allowlist.
	created, err := store.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeDecision,
		Payload: newDecisionPayload(t),
	})
	if err != nil {
		t.Fatalf("Create TypeDecision: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", created.ID)
	})

	// 2. Read the row back as the materialiser would and decode the payload.
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Type != string(proposal.TypeDecision) {
		t.Errorf("type = %q, want decision", got.Type)
	}
	var dp proposal.DecisionProposerPayload
	if err := json.Unmarshal(got.Payload, &dp); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if dp.Title != roundTripTitle {
		t.Errorf("payload title = %q, want %q", dp.Title, roundTripTitle)
	}

	// 3. Run the full pg-tx materialise: BEGIN → INSERT decisions → UPDATE
	//    pending_proposals → COMMIT. This mirrors Server.acceptProposalPg.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit

	// Insert decision row directly (the test purposely bypasses
	// internal/decision/Store to keep this proposal-package-only and avoid
	// the embedding/cosine paths). Workspace_id NULL = unscoped, matches
	// the proposal row's workspace.
	decID := uuid.New()
	_, err = tx.Exec(
		ctx, `INSERT INTO decisions (id, title, context, decision, rationale)
		VALUES ($1, $2, $3, $4, $5)`,
		decID, dp.Title,
		"auto-proposed by trigger_tool="+dp.TriggerTool+" session="+dp.SessionID,
		dp.Decision, dp.Rationale,
	)
	if err != nil {
		t.Fatalf("INSERT decisions: %v", err)
	}
	// Resolve via the tx-scoped store so the WHERE status='pending' guard
	// runs inside the same tx as the decision insert.
	if _, err := store.WithTx(tx).Resolve(ctx, created.ID, proposal.StatusAccepted); err != nil {
		t.Fatalf("ResolveTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// 4. Verify post-commit state: proposal accepted, decision visible.
	post, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get post-commit: %v", err)
	}
	if post.Status != string(proposal.StatusAccepted) {
		t.Errorf("status = %q, want accepted", post.Status)
	}

	var (
		gotTitle    string
		gotDecision string
	)
	row := pool.QueryRow(ctx, "SELECT title, decision FROM decisions WHERE id = $1", decID)
	if err := row.Scan(&gotTitle, &gotDecision); err != nil {
		t.Fatalf("SELECT decisions: %v", err)
	}
	if gotTitle != roundTripTitle {
		t.Errorf("decisions.title = %q, want %q", gotTitle, roundTripTitle)
	}
	if !strings.Contains(gotDecision, "SSE-style streaming") {
		t.Errorf("decisions.decision = %q, want contains 'SSE-style streaming'", gotDecision)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM decisions WHERE id = $1", decID)
	})
}

// TestProposal_Accept_TypeDecision_Roundtrip_SQLite_RollbackOnResolveFail
// simulates the failure branch: Resolve fails (e.g. concurrent accept
// already flipped the row), the entire transaction rolls back, and the
// decision row MUST NOT exist after rollback. This is the core safety
// property of the Tx path — better an orphan rollback than a half-state.
func TestProposal_Accept_TypeDecision_Roundtrip_SQLite_RollbackOnResolveFail(t *testing.T) {
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	propStore := wbtsqlite.NewProposalStore(sdb)
	decStore := wbtsqlite.NewDecisionStore(sdb)

	prop, err := propStore.Create(ctx, proposal.CreateParams{
		Type:    proposal.TypeDecision,
		Payload: newDecisionPayload(t),
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	// Pre-resolve the proposal so the in-tx ResolveTx sees a stale state
	// and returns ErrNotFound — simulating a concurrent accept.
	if _, err := propStore.Resolve(ctx, prop.ID, proposal.StatusAccepted); err != nil {
		t.Fatalf("pre-resolve: %v", err)
	}

	tx, err := sdb.BeginTx(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}

	if _, err := decStore.LogTx(ctx, tx, decision.LogParams{
		Title: roundTripTitle, Decision: "x", Rationale: "y", Source: decision.SourceAuto,
	}); err != nil {
		t.Fatalf("LogTx: %v", err)
	}
	resolveErr := propStore.ResolveTx(ctx, tx, prop.ID, proposal.StatusAccepted)
	if resolveErr == nil {
		t.Fatal("ResolveTx should fail on already-resolved row")
	}
	// Roll back; the decision MUST disappear.
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	rows, err := decStore.All(ctx, 10)
	if err != nil {
		t.Fatalf("decisions.All post-rollback: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 decisions after rollback, got %d", len(rows))
	}
}
