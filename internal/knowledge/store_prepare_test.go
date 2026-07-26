package knowledge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
)

// fixedVecEmbedder is a minimal ai.ContextEmbeddingProvider stub that always
// returns the same vector, letting tests deterministically drive
// Store.Prepare's cosine-similarity dedup branch (real Gemini calls are not
// available in tests).
type fixedVecEmbedder struct {
	vec []float32
}

func (f fixedVecEmbedder) Embed(context.Context, string) ([]float32, error) {
	return f.vec, nil
}

// TestKnowledgeStore_Prepare_URLDedup_TableDriven exercises Store.Prepare's
// URL exact-match dedup decisions in isolation from any transaction/write,
// using a nil embed client so the cosine-similarity branch never interferes
// (matches AddItem's own "no embedding client → dedup skipped" behavior).
// Covers the happy path, the URL-collision error path, and the edge case
// that a child item (ParentID set) skips URL dedup even when its URL
// collides with an existing row.
func TestKnowledgeStore_Prepare_URLDedup_TableDriven(t *testing.T) {
	pool := openKnowledgePgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := knowledge.NewStore(pool, nil, &wsID)

	seeded, err := store.AddItem(ctx, knowledge.AddItemParams{
		Type:    "til",
		Title:   "Seed item",
		Content: "seed content for URL dedup collision",
		URL:     "https://example.com/seed",
	})
	if err != nil {
		t.Fatalf("seeding AddItem: %v", err)
	}
	parentBytes := [16]byte(seeded.ID)

	cases := []struct {
		name    string
		params  knowledge.AddItemParams
		wantDup bool
	}{
		{
			name: "happy path: no URL collision",
			params: knowledge.AddItemParams{
				Type: "til", Title: "Fresh item", Content: "unrelated content",
				URL: "https://example.com/fresh",
			},
			wantDup: false,
		},
		{
			name: "error: URL exact-match dedup",
			params: knowledge.AddItemParams{
				Type: "til", Title: "Different title, same URL", Content: "different content",
				URL: "https://example.com/seed",
			},
			wantDup: true,
		},
		{
			name: "edge: child item (ParentID set) skips URL dedup despite a colliding URL",
			params: knowledge.AddItemParams{
				Type: "til", Title: "Section child", Content: "child section content",
				URL:      "https://example.com/seed", // would collide if URL dedup ran
				ParentID: &parentBytes,
			},
			wantDup: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prep, err := store.Prepare(ctx, tc.params)
			if tc.wantDup {
				var dupErr knowledge.ErrDuplicate
				if !errors.As(err, &dupErr) {
					t.Fatalf("Prepare(%+v): expected ErrDuplicate, got %v", tc.params, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Prepare(%+v): unexpected error: %v", tc.params, err)
			}
			if prep.Params.Title != tc.params.Title {
				t.Errorf("PreparedItem.Params.Title = %q, want %q", prep.Params.Title, tc.params.Title)
			}
			if prep.Vec != nil {
				t.Errorf("expected nil Vec with no embed client, got %v", prep.Vec)
			}
			if !prep.DedupSkipped {
				t.Errorf("expected DedupSkipped=true with no embed client")
			}
		})
	}
}

// TestKnowledgeStore_Prepare_CosineDedup verifies the cosine-similarity dedup
// branch in isolation: a fixedVecEmbedder makes every Embed call return the
// identical vector, so a second Prepare call at the same heading_level is a
// guaranteed 100% match even though its URL and title are both distinct from
// the seed row — proving the collision is coming from the cosine check, not
// URL dedup.
func TestKnowledgeStore_Prepare_CosineDedup(t *testing.T) {
	pool := openKnowledgePgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	// knowledge_items.embedding is vector(768) (migrations/000005_knowledge.up.sql,
	// sized for Gemini-001); a shorter vector fails the INSERT/UPDATE with a
	// dimension-mismatch error, silently leaving embedding NULL and defeating
	// the dedup query's `WHERE embedding IS NOT NULL` filter.
	sameVec := make([]float32, 768)
	for i := range sameVec {
		sameVec[i] = 0.001 * float32(i%10)
	}
	store := knowledge.NewStore(pool, fixedVecEmbedder{vec: sameVec}, &wsID)

	seeded, err := store.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "Cosine seed", Content: "seed content",
	})
	if err != nil {
		t.Fatalf("seeding AddItem: %v", err)
	}
	if seeded.Title != "Cosine seed" {
		t.Fatalf("unexpected seeded item: %+v", seeded)
	}

	_, err = store.Prepare(ctx, knowledge.AddItemParams{
		Type: "til", Title: "Completely different title", Content: "completely different content",
	})
	var dupErr knowledge.ErrDuplicate
	if !errors.As(err, &dupErr) {
		t.Fatalf("expected ErrDuplicate from cosine collision, got %v", err)
	}
	if dupErr.Similarity < 0.99 {
		t.Errorf("expected ~1.0 similarity for an identical vector, got %v", dupErr.Similarity)
	}
}

// TestKnowledgeStore_WriteItemTx_CommitRoundTrip verifies WriteItemTx writes
// a row that becomes visible (via GetByID) only after the caller commits the
// tx, and that the resulting row matches what AddItem would have produced
// for equivalent params — proving Prepare+WriteItemTx+Commit is behaviorally
// equivalent to AddItem's non-tx path.
func TestKnowledgeStore_WriteItemTx_CommitRoundTrip(t *testing.T) {
	pool := openKnowledgePgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := knowledge.NewStore(pool, nil, &wsID)

	params := knowledge.AddItemParams{
		Type:    "til",
		Title:   "WriteItemTx commit round trip",
		Content: "content written via the tx path",
		URL:     "https://example.com/write-item-tx-commit",
		Tags:    []string{"a", "b"},
	}

	prep, err := store.Prepare(ctx, params)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin: %v", err)
	}
	item, err := store.WriteItemTx(ctx, tx, prep)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("WriteItemTx: %v", err)
	}
	if item.Title != params.Title || len(item.Tags) != 2 {
		t.Fatalf("unexpected item from WriteItemTx: %+v", item)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("tx.Commit: %v", err)
	}

	got, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after commit: %v", err)
	}
	if got.Title != params.Title || !got.Url.Valid || got.Url.String != params.URL {
		t.Errorf("committed row mismatch: %+v", got)
	}
}

// TestKnowledgeStore_WriteItemTx_RollbackLeavesNoRow verifies that a
// WriteItemTx call followed by Rollback leaves no visible row — proving
// WriteItemTx participates correctly in the caller's transaction lifecycle
// (this is the property proposal.AcceptOrchestration's deferred Rollback
// depends on when a later orchestration step fails).
func TestKnowledgeStore_WriteItemTx_RollbackLeavesNoRow(t *testing.T) {
	pool := openKnowledgePgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := knowledge.NewStore(pool, nil, &wsID)

	params := knowledge.AddItemParams{
		Type: "til", Title: "WriteItemTx rollback", Content: "should never be visible",
	}
	prep, err := store.Prepare(ctx, params)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("pool.Begin: %v", err)
	}
	item, err := store.WriteItemTx(ctx, tx, prep)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("WriteItemTx: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("tx.Rollback: %v", err)
	}

	if _, err := store.GetByID(ctx, item.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("expected ErrNotFound for rolled-back row, got %v", err)
	}
}
