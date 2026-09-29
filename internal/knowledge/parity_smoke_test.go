package knowledge_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/conformance"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/sqlitetemplate"
	"github.com/google/uuid"
)

// parityQueryVec is the fixed 768-dim vector (matching the knowledge_items
// embedding column, migrations/000005_knowledge.up.sql:10) both conformance
// test funcs below attach to their smoke item, then pass to
// conformance.RunKnowledgeSmoke as the SearchByCosine query.
func parityQueryVec() []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = 0.5
	}
	return v
}

// TestKnowledgeConformance_Postgres runs conformance.RunKnowledgeSmoke
// against the real Postgres backend (F0929-73). Skips under -short
// (openKnowledgePgPool, store_postgres_test.go). Uses fixedVecEmbedder
// (store_prepare_test.go, same package) so AddItem attaches the embedding
// synchronously — PG's NewStore takes an embed client at construction.
func TestKnowledgeConformance_Postgres(t *testing.T) {
	pool := openKnowledgePgPool(t)
	ws := uuid.New()
	vec := parityQueryVec()
	ctx := context.Background()
	store := knowledge.NewStore(pool, fixedVecEmbedder{vec: vec}, &ws)

	item, err := store.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "smoke-item", Content: "c",
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	// unembedded item (row 10's negative case): built through a SEPARATE
	// store with a nil embed client, same pool/workspace, so AddItem's
	// embedAndCheckDupAtLevel short-circuits at `s.embed == nil`
	// (internal/knowledge/store.go) and never touches the fixed-vector test
	// embedder above or its similarity-dedup guard.
	noEmbedStore := knowledge.NewStore(pool, nil, &ws)
	unembedded, err := noEmbedStore.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "smoke-item-unembedded", Content: "no embedding",
	})
	if err != nil {
		t.Fatalf("AddItem (unembedded fixture): %v", err)
	}

	conformance.RunKnowledgeSmoke(t, store, item.ID, unembedded.ID, vec, "postgres")
}

// knowledgeParitySmokeTemplateKey is this file's own sqlitetemplate
// registry key (F0925-05) — distinct per-package, see
// gtd/parity_smoke_test.go's equivalent constant for the full rationale.
const knowledgeParitySmokeTemplateKey = "internal/knowledge/parity_smoke_test"

func knowledgeParitySmokeMigrator(ctx context.Context, path string) error {
	d, err := sqlite.Open(ctx, path, "")
	if err != nil {
		return err
	}
	return d.Close()
}

// TestKnowledgeConformance_SQLite runs conformance.RunKnowledgeSmoke against
// the SQLite backend. Always runs (no -short gate, no Docker needed).
// SQLite's NewKnowledgeStore takes no embed client — AddItem's prep.Vec is
// nil, so the embedding is attached afterward via UpdateEmbedding (same
// pattern as internal/storage/sqlite/knowledge_search_cosine_test.go).
func TestKnowledgeConformance_SQLite(t *testing.T) {
	ctx := context.Background()
	ws := uuid.New()
	vec := parityQueryVec()
	path := sqlitetemplate.Path(t, knowledgeParitySmokeTemplateKey, knowledgeParitySmokeMigrator, "knowledge-parity.db")

	d, err := sqlite.Open(ctx, path, ws.String())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	store := sqlite.NewKnowledgeStore(d)
	item, err := store.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "smoke-item", Content: "c",
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if err := store.UpdateEmbedding(ctx, item.ID, ai.SerializeEmbedding(vec)); err != nil {
		t.Fatalf("UpdateEmbedding: %v", err)
	}

	// unembedded item (row 10's negative case): SQLite's AddItem never
	// embeds regardless of caller, so no second store is needed here — just
	// skip the UpdateEmbedding call.
	unembedded, err := store.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "smoke-item-unembedded", Content: "no embedding",
	})
	if err != nil {
		t.Fatalf("AddItem (unembedded fixture): %v", err)
	}

	conformance.RunKnowledgeSmoke(t, store, item.ID, unembedded.ID, vec, "sqlite")
}
