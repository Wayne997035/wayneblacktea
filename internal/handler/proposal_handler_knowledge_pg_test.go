package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [F184-01][F184-03] PG-backend HTTP-layer coverage for the TypeKnowledge
// accept seam. Reuses the shared TestMain / goalProjectPgPool singleton from
// proposal_handler_goalproject_pg_test.go (same package; internal/handler
// has exactly one TestMain — do NOT declare a second one here, it would be
// "multiple definitions of TestMain", a build error, not a test failure).

// fixedVecEmbedderForProposal is a minimal ai.ContextEmbeddingProvider stub
// that always returns the same vector, letting
// TestConfirmProposal_TypeKnowledge_PG_DuplicateContent_NoRowWritten
// deterministically drive knowledge.Store.Prepare's cosine-similarity dedup
// branch (real Gemini calls are not available in tests) — mirrors
// internal/knowledge/store_prepare_test.go's fixedVecEmbedder (package
// knowledge_test, unexported there, so re-declared here rather than shared).
type fixedVecEmbedderForProposal struct{ vec []float32 }

func (f fixedVecEmbedderForProposal) Embed(context.Context, string) ([]float32, error) {
	return f.vec, nil
}

var _ ai.ContextEmbeddingProvider = fixedVecEmbedderForProposal{}

// fixedTestEmbeddingVector returns a deterministic 768-dim vector (matching
// migrations/000005_knowledge.up.sql's `embedding vector(768)` column) for
// fixedVecEmbedderForProposal — any non-zero constant vector works since the
// dedup test only needs two Prepare calls to embed to the SAME vector
// (cosine similarity 1.0 >= the 0.88 threshold), not any specific value.
func fixedTestEmbeddingVector() []float32 {
	vec := make([]float32, 768)
	for i := range vec {
		vec[i] = 0.1
	}
	return vec
}

// newKnowledgeAcceptPGHandler mirrors newGoalProjectPGHandler
// (proposal_handler_goalproject_pg_test.go, same package) but also wires a
// knowledge.Store into PgAcceptDeps.Knowledge — real testcontainers pool, no
// mocks (backend-security-design.md §6.5). embed may be nil (matches
// production when no embed client is configured: dedup is skipped, prep.Vec
// stays nil, per knowledge.PreparedItem.Vec's doc comment).
func newKnowledgeAcceptPGHandler(pool *pgxpool.Pool, embed ai.ContextEmbeddingProvider) (*handler.ProposalHandler, *proposal.Store) {
	propStore := proposal.NewStore(pool, nil)
	gtdStore := gtd.NewStore(pool, nil)
	learningStore := learning.NewStore(pool, nil)
	decisionStore := decision.NewStore(pool, nil)
	knowledgeStore := knowledge.NewStore(pool, embed, nil)

	deps := proposal.PgAcceptDeps{
		Pool:      pool,
		Proposal:  propStore,
		GTD:       gtdStore,
		Learning:  learningStore,
		Decision:  decisionStore,
		Knowledge: knowledgeStore,
	}
	h := handler.NewProposalHandler(propStore, learningStore).
		WithGoalProjectAccept(func(id uuid.UUID) proposal.AcceptAdapter {
			return proposal.NewPgAcceptAdapter(id, deps)
		})
	return h, propStore
}

// TestConfirmProposal_TypeKnowledge_PG_MaterialisesKnowledgeItemsRow proves
// the HTTP accept path routes TypeKnowledge through
// proposal.AcceptOrchestration on the PG backend: a knowledge_items row is
// written inside the same transaction that resolves the proposal, and the
// response surfaces it as confirmResponse.KnowledgeItem (NOT discarded, see
// acceptGoalOrProject's Risk-flags doc comment on this — D4 response-shape).
func TestConfirmProposal_TypeKnowledge_PG_MaterialisesKnowledgeItemsRow(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newKnowledgeAcceptPGHandler(pool, nil)

	title := "pg-knowledge-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{
		"title": title, "content": "Memory decays without review.", "tags": []string{"learning"},
	})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeKnowledge, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM knowledge_items WHERE title = $1", title) })

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
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM knowledge_items WHERE title = $1 AND content = $2",
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

// TestConfirmProposal_TypeKnowledge_PG_EmptyTitle_BadRequest proves a
// TypeKnowledge payload that fails proposal.DecodeKnowledgePayload's
// validation (missing title) surfaces as 400 BEFORE any transaction opens —
// mirrors TestConfirmProposal_TypeGoal_PG_EmptyTitle_BadRequest. pending_proposals.payload
// is jsonb (Postgres validates JSON syntax at INSERT), so a semantically
// invalid-but-syntactically-valid payload is used, same workaround as that
// test and TestConfirmBatch_TypeGoal_PG_MalformedPayload.
func TestConfirmProposal_TypeKnowledge_PG_EmptyTitle_BadRequest(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newKnowledgeAcceptPGHandler(pool, nil)

	payload, _ := json.Marshal(map[string]any{"title": "", "content": "some content"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeKnowledge, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "missing title") {
		t.Errorf("body = %q, want substring %q", rec.Body.String(), "missing title")
	}

	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending after 400", got.Status)
	}
}

// TestConfirmProposal_TypeKnowledge_PG_DuplicateContent_NoRowWritten proves
// PrepareOutOfBand's cosine-similarity dedup (knowledge.Store.Prepare) runs
// and returns knowledge.ErrDuplicate BEFORE BeginTx (ADR 0003 G1
// "先算後寫") when an embed client is configured and finds a near-duplicate:
// no new knowledge_items row is written and the proposal stays pending.
// errors.As(err, &knowledge.ErrDuplicate{}) -> 409 mapping is explicitly out
// of scope this round (spec g1-seam-2026-09-15.md ambiguity #2 / GTD D-08
// decisions.md) — acceptGoalOrProject's generic error branch surfaces this
// as 500, which is asserted here, not treated as a bug.
func TestConfirmProposal_TypeKnowledge_PG_DuplicateContent_NoRowWritten(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	embed := fixedVecEmbedderForProposal{vec: fixedTestEmbeddingVector()}
	h, propStore := newKnowledgeAcceptPGHandler(pool, embed)

	// Seed an existing knowledge_items row via the SAME fixed-vector embed
	// client so the upcoming Prepare call's cosine-similarity dedup finds a
	// same-level match at similarity 1.0 (>= the 0.88 threshold).
	seedStore := knowledge.NewStore(pool, embed, nil)
	seedTitle := "pg-knowledge-seed-" + uuid.NewString()
	seeded, err := seedStore.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: seedTitle, Content: "seed content for dedup",
	})
	if err != nil {
		t.Fatalf("seed AddItem: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM knowledge_items WHERE id = $1", seeded.ID) })

	dupTitle := "pg-knowledge-dup-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"title": dupTitle, "content": "near-duplicate content"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeKnowledge, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/:id/confirm", h.ConfirmProposal)
	rec := performRequest(e, http.MethodPost, "/api/proposals/"+p.ID.String()+"/confirm", `{"action":"accept"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 (ErrDuplicate not yet mapped to 409 — out of scope), got %d: %s", rec.Code, rec.Body.String())
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM knowledge_items WHERE title = $1", dupTitle).Scan(&count); err != nil {
		t.Fatalf("query knowledge_items: %v", err)
	}
	if count != 0 {
		t.Errorf("knowledge_items row count = %d, want 0 (duplicate must not be written)", count)
	}

	got, err := propStore.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != string(proposal.StatusPending) {
		t.Errorf("proposal status = %q, want still pending after duplicate-content rejection", got.Status)
	}
}

// TestConfirmBatch_TypeKnowledge_PG_MaterialisesKnowledgeItemsRow proves the
// batch accept path (POST /api/proposals/confirm-batch) routes TypeKnowledge
// through the same h.goalProjectAdapter -> proposal.AcceptOrchestration seam
// the singular path uses — mirrors TestConfirmBatch_TypeGoal_PG_MaterialisesGoalsRow.
func TestConfirmBatch_TypeKnowledge_PG_MaterialisesKnowledgeItemsRow(t *testing.T) {
	pool := openGoalProjectTestPgPool(t)
	ctx := context.Background()
	h, propStore := newKnowledgeAcceptPGHandler(pool, nil)

	title := "pg-batch-knowledge-" + uuid.NewString()
	payload, _ := json.Marshal(map[string]any{"title": title, "content": "batch accept content"})
	p, err := propStore.Create(ctx, proposal.CreateParams{Type: proposal.TypeKnowledge, Payload: payload})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", p.ID) })

	e := newEcho()
	e.POST("/api/proposals/confirm-batch", h.ConfirmBatch)
	body := `{"ids":["` + p.ID.String() + `"],"action":"accept"}`
	rec := performRequest(e, http.MethodPost, "/api/proposals/confirm-batch", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	assertBatchResultOK(t, rec.Body.Bytes(), p.ID.String())
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM knowledge_items WHERE title = $1", title) })

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM knowledge_items WHERE title = $1", title).Scan(&count); err != nil {
		t.Fatalf("query knowledge_items: %v", err)
	}
	if count != 1 {
		t.Errorf("knowledge_items row count = %d, want 1", count)
	}
}
