package knowledge_test

import (
	"context"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
)

// TestKnowledgeStore_Pg_SearchReadOnlyDoesNotBumpRecall verifies the P1
// review finding A fix: SearchReadOnly returns the same rows Search would,
// but never writes recall_count/last_recalled_at — assemble_context
// (internal/contextpack) must stay genuinely read-only. Search, the
// mutating counterpart, is exercised in the same test as a contrast: it DOES
// bump recall, proving the assertions below aren't vacuously true.
func TestKnowledgeStore_Pg_SearchReadOnlyDoesNotBumpRecall(t *testing.T) {
	pool := openKnowledgePgPool(t)
	wsID := uuid.New()
	store := knowledge.NewStore(pool, nil, &wsID)
	ctx := context.Background()

	item, err := store.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "readonly search fixture", Content: "search read only recall content",
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	before, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID before search: %v", err)
	}

	results, err := store.SearchReadOnly(ctx, "readonly search fixture", 10)
	if err != nil {
		t.Fatalf("SearchReadOnly: %v", err)
	}
	found := false
	for _, r := range results {
		if r.ID == item.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected item %s in SearchReadOnly results, got %+v", item.ID, results)
	}

	afterReadOnly, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after SearchReadOnly: %v", err)
	}
	if afterReadOnly.RecallCount != before.RecallCount {
		t.Errorf("RecallCount changed after SearchReadOnly: before=%d after=%d, want unchanged (no write)",
			before.RecallCount, afterReadOnly.RecallCount)
	}
	if afterReadOnly.LastRecalledAt.Valid != before.LastRecalledAt.Valid {
		t.Errorf("LastRecalledAt.Valid changed after SearchReadOnly: before=%v after=%v, want unchanged",
			before.LastRecalledAt.Valid, afterReadOnly.LastRecalledAt.Valid)
	}

	// Contrast: Search (the mutating counterpart used everywhere except
	// assemble_context) DOES bump recall — proves the assertions above are
	// distinguishing real behavior, not vacuously true.
	if _, err := store.Search(ctx, "readonly search fixture", 10); err != nil {
		t.Fatalf("Search: %v", err)
	}
	afterSearch, err := store.GetByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetByID after Search: %v", err)
	}
	if afterSearch.RecallCount <= before.RecallCount {
		t.Errorf("expected Search (mutating) to bump RecallCount, got before=%d after=%d",
			before.RecallCount, afterSearch.RecallCount)
	}
	if !afterSearch.LastRecalledAt.Valid {
		t.Error("expected Search (mutating) to set LastRecalledAt")
	}
}
