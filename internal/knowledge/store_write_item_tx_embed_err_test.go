package knowledge_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
)

// badDimVec is a deliberately wrong-dimension vector: knowledge_items.embedding
// is vector(768) (migrations/000005_knowledge.up.sql), so pgvector rejects a
// 3-float vector at UPDATE time with a real dimension-mismatch error — the
// same documented failure mode TestKnowledgeStore_Prepare_CosineDedup's
// sameVec comment describes. This gives both tests below a genuine Postgres
// error to propagate/swallow, rather than a mocked one.
var badDimVec = []float32{0.1, 0.2, 0.3}

// TestKnowledgeStore_WriteItemTx_EmbeddingErrorPropagates verifies that when
// writeItemRow runs inside an open pgx.Tx (WriteItemTx's path) and the
// embedding UPDATE fails, the real error is returned to the caller instead of
// only being logged. Without this, the caller only ever observes a
// subsequent "current transaction is aborted" (25P02) from whatever
// statement runs next in the same tx — the actual cause never leaves the log.
func TestKnowledgeStore_WriteItemTx_EmbeddingErrorPropagates(t *testing.T) {
	pool := openKnowledgePgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := knowledge.NewStore(pool, fixedVecEmbedder{vec: badDimVec}, &wsID)

	params := knowledge.AddItemParams{
		Type:    "til",
		Title:   "WriteItemTx embedding failure",
		Content: "content whose embedding write will fail",
	}
	prep, err := store.Prepare(ctx, params)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if prep.Vec == nil {
		t.Fatalf("Prepare: expected prep.Vec to carry the bad-dimension vector through, got nil")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	item, err := store.WriteItemTx(ctx, tx, prep)
	if err == nil {
		t.Fatalf("WriteItemTx: expected error from failed embedding write in tx, got nil (item=%+v)", item)
	}
	if item != nil {
		t.Errorf("WriteItemTx: expected nil item on error, got %+v", item)
	}
	if !strings.Contains(err.Error(), "storing embedding in tx") {
		t.Errorf("WriteItemTx error = %q, want it to identify the embedding-in-tx failure", err.Error())
	}
	// The real cause (pgvector dimension mismatch) must still be reachable
	// through the error chain, not just summarized away.
	if errors.Unwrap(err) == nil {
		t.Errorf("WriteItemTx error = %q, want a wrapped underlying cause (errors.Unwrap == nil)", err.Error())
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("tx.Rollback: %v", err)
	}
}

// TestKnowledgeStore_AddItem_EmbeddingErrorWarnAndContinue verifies that
// AddItem's non-tx path (writeItemRow called with dbtx = s.pool) keeps its
// original warn-and-continue behavior when the embedding write fails: the
// knowledge item is still created and returned without error, and the
// embedding column is left NULL (proving the UPDATE genuinely failed rather
// than silently succeeding). This is the counterpart to
// TestKnowledgeStore_WriteItemTx_EmbeddingErrorPropagates and exists so a fix
// to the tx path cannot silently also start erroring the pool path.
func TestKnowledgeStore_AddItem_EmbeddingErrorWarnAndContinue(t *testing.T) {
	pool := openKnowledgePgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := knowledge.NewStore(pool, fixedVecEmbedder{vec: badDimVec}, &wsID)

	item, err := store.AddItem(ctx, knowledge.AddItemParams{
		Type:    "til",
		Title:   "AddItem embedding failure",
		Content: "content whose embedding write will fail",
	})
	if err != nil {
		t.Fatalf("AddItem: expected warn-and-continue (no error) when embedding write fails outside a tx, got: %v", err)
	}
	if item == nil {
		t.Fatalf("AddItem: expected a created item despite the embedding failure, got nil")
	}

	// The row itself must be visible (unlike the tx path, there is no
	// rollback here — the INSERT already committed standalone).
	got, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after AddItem: %v", err)
	}
	if got.Title != "AddItem embedding failure" {
		t.Errorf("unexpected item after AddItem: %+v", got)
	}

	// Confirm the embedding UPDATE actually failed (left NULL) rather than
	// having silently succeeded with a truncated/garbage vector.
	var embeddingIsNull bool
	if err := pool.QueryRow(
		ctx,
		"SELECT embedding IS NULL FROM knowledge_items WHERE id = $1", item.ID,
	).Scan(&embeddingIsNull); err != nil {
		t.Fatalf("querying embedding column: %v", err)
	}
	if !embeddingIsNull {
		t.Errorf("expected embedding to remain NULL after a failed dimension-mismatch UPDATE, got a non-NULL value")
	}
}
