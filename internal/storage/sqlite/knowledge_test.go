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

func TestKnowledgeStore_LIKESearchOrdering(t *testing.T) {
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
	if rows[0].Title != "SQLite title match" {
		t.Fatalf("title match should sort before content-only match: %+v", rows)
	}
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
	rowsB, err := storeB.List(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("List B: %v", err)
	}
	if len(rowsB) != 0 {
		t.Fatalf("workspace B should not see A knowledge: %+v", rowsB)
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
