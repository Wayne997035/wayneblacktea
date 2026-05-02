package sqlite_test

import (
	"context"
	"testing"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
)

// TestKnowledgeStore_SearchByCosine_EmptyInput verifies input validation:
// nil/empty queryEmbedding or non-positive limit returns nil immediately
// without touching the DB.
func TestKnowledgeStore_SearchByCosine_EmptyInput(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	cases := []struct {
		name  string
		query []float32
		limit int
	}{
		{"nil query", nil, 5},
		{"empty query", []float32{}, 5},
		{"zero limit", []float32{0.1, 0.2}, 0},
		{"negative limit", []float32{0.1, 0.2}, -1},
	}
	for _, tc := range cases {
		got, err := s.SearchByCosine(context.Background(), tc.query, tc.limit)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", tc.name, err)
		}
		if got != nil {
			t.Errorf("%s: expected nil result, got %d items", tc.name, len(got))
		}
	}
}

// TestKnowledgeStore_SearchByCosine_EmptyTable verifies that searching an
// empty table returns nil without error (no rows to scan, no false drop).
func TestKnowledgeStore_SearchByCosine_EmptyTable(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	got, err := s.SearchByCosine(context.Background(), []float32{0.1, 0.2}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result on empty table, got %d items", len(got))
	}
}

// TestKnowledgeStore_SearchByCosine_ItemWithoutEmbeddingExcluded verifies
// that AddItem (which does not populate embedding) yields rows that
// SearchByCosine correctly excludes via the WHERE embedding IS NOT NULL clause.
func TestKnowledgeStore_SearchByCosine_ItemWithoutEmbeddingExcluded(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	if _, err := s.AddItem(context.Background(), knowledge.AddItemParams{
		Type: "article", Title: "no embed", Content: "x",
	}); err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	got, err := s.SearchByCosine(context.Background(), []float32{0.1, 0.2}, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty result (item has no embedding), got %d items", len(got))
	}
}

// TestKnowledgeStore_SearchByCosine_ScanColumnsMatch is the critical
// regression guard for security audit C-1: SearchByCosine SCAN must match
// knowledgeSelectCols (16 cols) + embedding (1 col) = 17 destinations.
// A 12-arg scan would silently fail on every row and the function would
// return nil — breaking SQLite vector recall completely.
//
// We populate one item + its embedding, then SearchByCosine MUST return it
// (not silently drop it via Scan failure).
func TestKnowledgeStore_SearchByCosine_ScanColumnsMatch(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	item, err := s.AddItem(context.Background(), knowledge.AddItemParams{
		Type: "article", Title: "with embed", Content: "x",
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	prov := localai.NewEmbeddingProvider()
	vec, err := prov.Embed("with embed")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if err := s.UpdateEmbedding(context.Background(), item.ID, localai.SerializeEmbedding(vec)); err != nil {
		t.Fatalf("UpdateEmbedding: %v", err)
	}

	got, err := s.SearchByCosine(context.Background(), vec, 5)
	if err != nil {
		t.Fatalf("SearchByCosine: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d (regression: scan column count likely mismatched)", len(got))
	}
	if got[0].ID != item.ID {
		t.Errorf("expected item %s, got %s", item.ID, got[0].ID)
	}
}

// TestKnowledgeStore_SearchByCosine_WorkspaceIsolation verifies that the
// workspace_id filter is enforced — a search in workspace B must not
// return items written in workspace A.
func TestKnowledgeStore_SearchByCosine_WorkspaceIsolation(t *testing.T) {
	const dsn = "file::memory:?cache=shared"
	wsA := "11111111-1111-1111-1111-111111111111"
	wsB := "22222222-2222-2222-2222-222222222222"

	storeA := openKnowledgeStore(t, dsn, wsA)
	storeB := openKnowledgeStore(t, dsn, wsB)

	itemA, err := storeA.AddItem(context.Background(), knowledge.AddItemParams{
		Type: "article", Title: "A", Content: "x",
	})
	if err != nil {
		t.Fatalf("AddItem A: %v", err)
	}
	prov := localai.NewEmbeddingProvider()
	vec, _ := prov.Embed("A")
	if err := storeA.UpdateEmbedding(context.Background(), itemA.ID, localai.SerializeEmbedding(vec)); err != nil {
		t.Fatalf("UpdateEmbedding A: %v", err)
	}

	gotB, err := storeB.SearchByCosine(context.Background(), vec, 5)
	if err != nil {
		t.Fatalf("SearchByCosine B: %v", err)
	}
	if len(gotB) != 0 {
		t.Errorf("workspace B should NOT see workspace A items, got %d", len(gotB))
	}

	gotA, err := storeA.SearchByCosine(context.Background(), vec, 5)
	if err != nil {
		t.Fatalf("SearchByCosine A: %v", err)
	}
	if len(gotA) != 1 {
		t.Errorf("workspace A should see its own item, got %d", len(gotA))
	}
}
