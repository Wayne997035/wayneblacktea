package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// [F184-02][F184-03] SQLite-backend HTTP-layer coverage for the TypeKnowledge
// accept seam — mirrors proposal_handler_knowledge_pg_test.go for the PG
// backend. No TestMain needed here: SQLite tests open a fresh :memory: DB
// per test (openGoalProjectSQLiteHandler's pattern), no shared singleton.

// openKnowledgeAcceptSQLiteHandler mirrors openGoalProjectSQLiteHandler
// (proposal_handler_goalproject_sqlite_test.go, same package) but also wires
// a wbtsqlite.KnowledgeStore into AcceptDeps.Knowledge — real :memory: DB, no
// mocks (SQLite has no container, so :memory: stands in for testcontainers).
func openKnowledgeAcceptSQLiteHandler(t *testing.T) (*handler.ProposalHandler, *wbtsqlite.ProposalStore, *wbtsqlite.DB) {
	t.Helper()
	ctx := context.Background()
	sdb, err := wbtsqlite.Open(ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("wbtsqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = sdb.Close() })

	propStore := wbtsqlite.NewProposalStore(sdb)
	gtdStore := wbtsqlite.NewGTDStore(sdb)
	learningStore := wbtsqlite.NewLearningStore(sdb)
	decisionStore := wbtsqlite.NewDecisionStore(sdb)
	knowledgeStore := wbtsqlite.NewKnowledgeStore(sdb)

	deps := wbtsqlite.AcceptDeps{
		Proposal:  propStore,
		GTD:       gtdStore,
		Learning:  learningStore,
		Decision:  decisionStore,
		Knowledge: knowledgeStore,
	}
	h := handler.NewProposalHandler(propStore, learningStore).
		WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
			return wbtsqlite.NewAcceptAdapter(id, deps)
		})
	return h, propStore, sdb
}

// TestConfirmProposal_TypeKnowledge_SQLite_MaterialisesKnowledgeItemsRow
// proves the HTTP accept path routes TypeKnowledge through
// proposal.AcceptOrchestration on the SQLite backend: a knowledge_items row
// is written inside the same tx that resolves the proposal (read back via
// getByIDTx per WriteItemTx's doc comment — SQLite serialises all access
// through a single pooled connection), with embedding absent (Vec=nil is
// expected on SQLite, not a bug — knowledge.PreparedItem.Vec's doc comment).
func TestConfirmProposal_TypeKnowledge_SQLite_MaterialisesKnowledgeItemsRow(t *testing.T) {
	h, propStore, sdb := openKnowledgeAcceptSQLiteHandler(t)
	ctx := context.Background()

	title := "sqlite-knowledge-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{
		"title": title, "content": "Memory decays without review.", "tags": []string{"learning"},
	})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeKnowledge, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		KnowledgeItem *struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"knowledge_item"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response not valid JSON: %v (body: %s)", err, rec.Body.String())
	}
	if resp.KnowledgeItem == nil {
		t.Fatalf("expected non-null knowledge_item in response, got: %s", rec.Body.String())
	}
	if resp.KnowledgeItem.Title != title {
		t.Errorf("knowledge_item.title = %q, want %q", resp.KnowledgeItem.Title, title)
	}

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_items WHERE title = ? AND content = ?",
		title, "Memory decays without review.").Scan(&count); err != nil {
		t.Fatalf("query knowledge_items: %v", err)
	}
	if count != 1 {
		t.Errorf("knowledge_items row count = %d, want 1", count)
	}

	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusAccepted) {
		t.Errorf("proposal status = %q, want accepted", got.Status)
	}
}

// TestConfirmProposal_TypeKnowledge_SQLite_EmptyTitle_BadRequest mirrors
// TestConfirmProposal_TypeKnowledge_PG_EmptyTitle_BadRequest for the SQLite
// backend — proposal.DecodeKnowledgePayload's missing-title rejection fires
// via validateGoalProjectPayload BEFORE BeginTx.
func TestConfirmProposal_TypeKnowledge_SQLite_EmptyTitle_BadRequest(t *testing.T) {
	h, propStore, sdb := openKnowledgeAcceptSQLiteHandler(t)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]any{"title": "", "content": "some content"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeKnowledge, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing title") {
		t.Errorf("body = %q, want substring %q", rec.Body.String(), "missing title")
	}

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_items").Scan(&count); err != nil {
		t.Fatalf("query knowledge_items: %v", err)
	}
	if count != 0 {
		t.Errorf("knowledge_items row count = %d, want 0 on malformed payload", count)
	}
	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending after 400", got.Status)
	}
}

// TestConfirmBatch_TypeKnowledge_SQLite_MaterialisesKnowledgeItemsRow mirrors
// TestConfirmBatch_TypeKnowledge_PG_MaterialisesKnowledgeItemsRow for the
// SQLite backend.
func TestConfirmBatch_TypeKnowledge_SQLite_MaterialisesKnowledgeItemsRow(t *testing.T) {
	h, propStore, sdb := openKnowledgeAcceptSQLiteHandler(t)
	ctx := context.Background()

	title := "sqlite-batch-knowledge-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"title": title, "content": "batch accept content"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeKnowledge, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + p.ID.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertBatchResultOK(t, rec.Body.Bytes(), p.ID.String())

	var count int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM knowledge_items WHERE title = ?", title).Scan(&count); err != nil {
		t.Fatalf("query knowledge_items: %v", err)
	}
	if count != 1 {
		t.Errorf("knowledge_items row count = %d, want 1", count)
	}
}
