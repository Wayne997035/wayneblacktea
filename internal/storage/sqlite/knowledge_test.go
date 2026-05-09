package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openKnowledgeStore(t *testing.T, dsn, workspaceID string) *sqlite.KnowledgeStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), dsn, workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewKnowledgeStore(d)
}

func TestKnowledgeStore_AddListGetRoundTrip(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	item, err := s.AddItem(context.Background(), knowledge.AddItemParams{
		Type:          "article",
		Title:         "SQLite notes",
		Content:       "Use LIKE fallback for local search",
		URL:           "https://example.com/sqlite",
		Tags:          []string{"sqlite", "search"},
		Source:        "manual",
		LearningValue: 4,
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if item.Title != "SQLite notes" || !item.Url.Valid || len(item.Tags) != 2 ||
		!item.LearningValue.Valid || item.LearningValue.Int32 != 4 {
		t.Fatalf("unexpected item: %+v", item)
	}

	got, err := s.GetByID(context.Background(), item.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != item.ID || got.Tags[1] != "search" {
		t.Fatalf("unexpected fetched item: %+v", got)
	}
}

func TestKnowledgeStore_NullOptionalFieldsAndDefaults(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	item, err := s.AddItem(context.Background(), knowledge.AddItemParams{
		Type: "til", Title: "Minimal", Content: "",
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	if item.Url.Valid || item.LearningValue.Valid || item.Source != "manual" || len(item.Tags) != 0 {
		t.Fatalf("unexpected defaults/optionals: %+v", item)
	}
}

func TestKnowledgeStore_EmptyTable(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	rows, err := s.List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected empty List, got %+v", rows)
	}
	matches, err := s.Search(context.Background(), "missing", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected empty Search, got %+v", matches)
	}
	_, err = s.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatalf("expected knowledge.ErrNotFound, got %v", err)
	}
}

func TestKnowledgeStore_SearchOrdering(t *testing.T) {
	// Renamed from LIKESearchOrdering: now uses FTS5; both items must be found.
	// Strict first-result ordering is not asserted because FTS5 BM25 + Ebbinghaus
	// re-sort is non-deterministic when items are created with identical decay
	// parameters within milliseconds of each other.
	s := openKnowledgeStore(t, ":memory:", "")
	if _, err := s.AddItem(context.Background(), knowledge.AddItemParams{
		Type: "article", Title: "Content match", Content: "sqlite appears only here",
	}); err != nil {
		t.Fatalf("AddItem content match: %v", err)
	}
	if _, err := s.AddItem(context.Background(), knowledge.AddItemParams{
		Type: "til", Title: "SQLite title match", Content: "local notes",
	}); err != nil {
		t.Fatalf("AddItem title match: %v", err)
	}

	rows, err := s.Search(context.Background(), "sqlite", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 matches, got %+v", rows)
	}
	// Verify both expected titles are present (FTS5 recall).
	titles := map[string]bool{rows[0].Title: true, rows[1].Title: true}
	if !titles["SQLite title match"] || !titles["Content match"] {
		t.Fatalf("expected both items in results, got %+v", rows)
	}
}

func TestKnowledgeStore_SearchFTS5(t *testing.T) {
	ctx := context.Background()
	s := openKnowledgeStore(t, ":memory:", "")

	// Insert 3 items with distinct unique words.
	_, err := s.AddItem(ctx, knowledge.AddItemParams{
		Type: "article", Title: "SQLite notes", Content: "portable embedded database",
	})
	if err != nil {
		t.Fatalf("AddItem SQLite notes: %v", err)
	}
	_, err = s.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "FTS5 configuration guide", Content: "full-text search tokenizer",
	})
	if err != nil {
		t.Fatalf("AddItem FTS5 guide: %v", err)
	}
	_, err = s.AddItem(ctx, knowledge.AddItemParams{
		Type: "bookmark", Title: "Golang concurrency", Content: "goroutines channels select",
	})
	if err != nil {
		t.Fatalf("AddItem Golang concurrency: %v", err)
	}

	t.Run("unique word returns correct item", func(t *testing.T) {
		// "goroutine" appears only in the concurrency item.
		got, err := s.Search(ctx, "goroutine", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(got) < 1 {
			t.Fatalf("expected at least 1 result, got 0")
		}
		if got[0].Title != "Golang concurrency" {
			t.Errorf("expected Golang concurrency first, got %q", got[0].Title)
		}
	})

	t.Run("prefix match", func(t *testing.T) {
		// "sqlit" is a prefix of "SQLite"; porter unicode61 + * should match.
		got, err := s.Search(ctx, "sqlit", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(got) < 1 {
			t.Fatalf("expected at least 1 result for prefix 'sqlit', got 0")
		}
		if got[0].Title != "SQLite notes" {
			t.Errorf("expected SQLite notes first, got %q", got[0].Title)
		}
	})

	t.Run("empty query returns nil no error", func(t *testing.T) {
		got, err := s.Search(ctx, "", 10)
		if err != nil {
			t.Fatalf("Search empty: unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for empty query, got %d items", len(got))
		}
	})

	t.Run("special-chars-only query returns nil no error", func(t *testing.T) {
		got, err := s.Search(ctx, `"^*:-+().`, 10)
		if err != nil {
			t.Fatalf("Search special chars: unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for special-char query, got %d items", len(got))
		}
	})
}

func TestKnowledgeStore_SearchCoarseFTS5(t *testing.T) {
	ctx := context.Background()
	s := openKnowledgeStore(t, ":memory:", "")

	// Insert a root item and a child item with the same keyword.
	root, err := s.AddItem(ctx, knowledge.AddItemParams{
		Type: "article", Title: "Root document about vectors", Content: "embedding similarity search",
	})
	if err != nil {
		t.Fatalf("AddItem root: %v", err)
	}
	rootBytes := [16]byte(root.ID)
	_, err = s.AddItem(ctx, knowledge.AddItemParams{
		Type:         "article",
		Title:        "Child section about vectors",
		Content:      "embedding subsection detail",
		ParentID:     &rootBytes,
		HeadingLevel: 1,
	})
	if err != nil {
		t.Fatalf("AddItem child: %v", err)
	}

	t.Run("coarse returns root only", func(t *testing.T) {
		got, err := s.SearchCoarse(ctx, "vector", 10)
		if err != nil {
			t.Fatalf("SearchCoarse: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 root result from SearchCoarse, got %d: %+v", len(got), got)
		}
		if got[0].Title != "Root document about vectors" {
			t.Errorf("expected root title, got %q", got[0].Title)
		}
	})

	t.Run("coarse empty query returns nil no error", func(t *testing.T) {
		got, err := s.SearchCoarse(ctx, "", 10)
		if err != nil {
			t.Fatalf("SearchCoarse empty: unexpected error: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for empty query, got %d items", len(got))
		}
	})
}

func TestKnowledgeStore_URLDuplicate(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	params := knowledge.AddItemParams{
		Type: "bookmark", Title: "First", Content: "", URL: "https://example.com/dup",
	}
	if _, err := s.AddItem(context.Background(), params); err != nil {
		t.Fatalf("AddItem first: %v", err)
	}
	params.Title = "Second"
	_, err := s.AddItem(context.Background(), params)
	var dup knowledge.ErrDuplicate
	if !errors.As(err, &dup) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestKnowledgeStore_WorkspaceIsolation(t *testing.T) {
	wsA, wsB := uuid.New().String(), uuid.New().String()
	dsn := "file:knowledge-" + uuid.New().String() + "?mode=memory&cache=shared"
	storeA := openKnowledgeStore(t, dsn, wsA)
	storeB := openKnowledgeStore(t, dsn, wsB)

	if _, err := storeA.AddItem(context.Background(), knowledge.AddItemParams{
		Type: "til", Title: "Only A", Content: "workspace scoped",
	}); err != nil {
		t.Fatalf("AddItem A: %v", err)
	}
	ctx := context.Background()
	rowsB, err := storeB.List(ctx, 10, 0)
	if err != nil {
		t.Fatalf("List B: %v", err)
	}
	if len(rowsB) != 0 {
		t.Fatalf("workspace B should not see A knowledge: %+v", rowsB)
	}

	// FTS5 Search and SearchCoarse must also respect workspace scoping.
	rowsB, err = storeB.Search(ctx, "workspace", 10)
	if err != nil {
		t.Fatalf("storeB.Search: %v", err)
	}
	if len(rowsB) != 0 {
		t.Errorf("storeB.Search: expected 0 results for workspace-A item, got %d", len(rowsB))
	}

	coarseB, err := storeB.SearchCoarse(ctx, "workspace", 10)
	if err != nil {
		t.Fatalf("storeB.SearchCoarse: %v", err)
	}
	if len(coarseB) != 0 {
		t.Errorf("storeB.SearchCoarse: expected 0 results for workspace-A item, got %d", len(coarseB))
	}
}

func TestKnowledgeStore_ContextCanceled(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.List(ctx, 10, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestKnowledgeStore_UpdateLearningValue(t *testing.T) {
	s := openKnowledgeStore(t, ":memory:", "")
	ctx := context.Background()

	item, err := s.AddItem(ctx, knowledge.AddItemParams{
		Type: "til", Title: "Spaced repetition", Content: "Ebbinghaus curve",
	})
	if err != nil {
		t.Fatalf("AddItem: %v", err)
	}

	for _, v := range []int{1, 3, 5} {
		if err := s.UpdateLearningValue(ctx, item.ID, v); err != nil {
			t.Errorf("UpdateLearningValue(%d): %v", v, err)
		}
		got, err := s.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID after update to %d: %v", v, err)
		}
		if !got.LearningValue.Valid || int(got.LearningValue.Int32) != v {
			t.Errorf("after UpdateLearningValue(%d): got LearningValue=%+v", v, got.LearningValue)
		}
	}

	if err := s.UpdateLearningValue(ctx, uuid.New(), 3); !errors.Is(err, knowledge.ErrNotFound) {
		t.Errorf("expected ErrNotFound for unknown ID, got %v", err)
	}
}
