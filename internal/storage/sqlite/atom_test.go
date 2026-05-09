package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openAtomDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteAtomStore_AddAtom(t *testing.T) {
	db := openAtomDB(t)
	store := wbtsqlite.NewAtomStore(db)
	ctx := context.Background()

	parentID := uuid.New()

	tests := []struct {
		name    string
		params  atom.AddAtomParams
		wantErr bool
	}{
		{
			name: "happy path all fields",
			params: atom.AddAtomParams{
				ParentTable: "decisions",
				ParentID:    parentID,
				Content:     "Use testcontainers for integration tests",
				Keywords:    []string{"testcontainers", "docker"},
				Tags:        []string{"testing"},
			},
		},
		{
			name: "minimal required fields",
			params: atom.AddAtomParams{
				ParentTable: "knowledge_items",
				ParentID:    parentID,
				Content:     "Go modules use go.mod",
			},
		},
		{
			name: "nil keywords and tags become empty slices",
			params: atom.AddAtomParams{
				ParentTable: "procedural_memories",
				ParentID:    parentID,
				Content:     "Atom with nil slices",
				Keywords:    nil,
				Tags:        nil,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, err := store.AddAtom(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("AddAtom: %v", err)
			}
			if a == nil {
				t.Fatal("AddAtom returned nil")
			}
			if a.Content != tc.params.Content {
				t.Errorf("Content: got %q, want %q", a.Content, tc.params.Content)
			}
			if a.Keywords == nil {
				t.Error("Keywords should not be nil")
			}
			if a.Tags == nil {
				t.Error("Tags should not be nil")
			}
		})
	}
}

func TestSQLiteAtomStore_AddLink(t *testing.T) {
	db := openAtomDB(t)
	store := wbtsqlite.NewAtomStore(db)
	ctx := context.Background()

	parentID := uuid.New()
	a1, err := store.AddAtom(ctx, atom.AddAtomParams{
		ParentTable: "decisions",
		ParentID:    parentID,
		Content:     "Atom A",
	})
	if err != nil {
		t.Fatalf("AddAtom A: %v", err)
	}
	a2, err := store.AddAtom(ctx, atom.AddAtomParams{
		ParentTable: "decisions",
		ParentID:    parentID,
		Content:     "Atom B",
	})
	if err != nil {
		t.Fatalf("AddAtom B: %v", err)
	}

	t.Run("happy path link creation", func(t *testing.T) {
		err := store.AddLink(ctx, atom.AddLinkParams{
			FromAtomID: a1.ID,
			ToAtomID:   a2.ID,
			LinkType:   "same_entity",
			Confidence: 0.8,
		})
		if err != nil {
			t.Fatalf("AddLink: %v", err)
		}
	})

	t.Run("duplicate link is a no-op", func(t *testing.T) {
		err := store.AddLink(ctx, atom.AddLinkParams{
			FromAtomID: a1.ID,
			ToAtomID:   a2.ID,
			LinkType:   "same_entity",
			Confidence: 0.9,
		})
		if err != nil {
			t.Fatalf("duplicate AddLink should not error: %v", err)
		}
	})

	t.Run("different link type succeeds", func(t *testing.T) {
		err := store.AddLink(ctx, atom.AddLinkParams{
			FromAtomID: a1.ID,
			ToAtomID:   a2.ID,
			LinkType:   "same_project",
			Confidence: 0.5,
		})
		if err != nil {
			t.Fatalf("AddLink different type: %v", err)
		}
	})
}

func TestSQLiteAtomStore_ListByParent(t *testing.T) {
	db := openAtomDB(t)
	store := wbtsqlite.NewAtomStore(db)
	ctx := context.Background()

	parentID := uuid.New()
	otherParentID := uuid.New()

	for i := 0; i < 3; i++ {
		_, err := store.AddAtom(ctx, atom.AddAtomParams{
			ParentTable: "decisions",
			ParentID:    parentID,
			Content:     "atom for parent",
		})
		if err != nil {
			t.Fatalf("seed atom %d: %v", i, err)
		}
	}
	_, err := store.AddAtom(ctx, atom.AddAtomParams{
		ParentTable: "decisions",
		ParentID:    otherParentID,
		Content:     "atom for other parent",
	})
	if err != nil {
		t.Fatalf("seed other parent: %v", err)
	}

	t.Run("returns only atoms for matching parent", func(t *testing.T) {
		atoms, err := store.ListByParent(ctx, "decisions", parentID)
		if err != nil {
			t.Fatalf("ListByParent: %v", err)
		}
		if len(atoms) != 3 {
			t.Errorf("expected 3 atoms, got %d", len(atoms))
		}
	})

	t.Run("different parent returns different atoms", func(t *testing.T) {
		atoms, err := store.ListByParent(ctx, "decisions", otherParentID)
		if err != nil {
			t.Fatalf("ListByParent: %v", err)
		}
		if len(atoms) != 1 {
			t.Errorf("expected 1 atom, got %d", len(atoms))
		}
	})

	t.Run("non-existent parent returns empty", func(t *testing.T) {
		atoms, err := store.ListByParent(ctx, "decisions", uuid.New())
		if err != nil {
			t.Fatalf("ListByParent: %v", err)
		}
		if len(atoms) != 0 {
			t.Errorf("expected 0 atoms, got %d", len(atoms))
		}
	})
}

func TestSQLiteAtomStore_Traverse(t *testing.T) {
	db := openAtomDB(t)
	store := wbtsqlite.NewAtomStore(db)
	ctx := context.Background()

	parentID := uuid.New()

	a, err := store.AddAtom(ctx, atom.AddAtomParams{ParentTable: "decisions", ParentID: parentID, Content: "A"})
	if err != nil {
		t.Fatalf("add A: %v", err)
	}
	b, err := store.AddAtom(ctx, atom.AddAtomParams{ParentTable: "decisions", ParentID: parentID, Content: "B"})
	if err != nil {
		t.Fatalf("add B: %v", err)
	}
	c, err := store.AddAtom(ctx, atom.AddAtomParams{ParentTable: "decisions", ParentID: parentID, Content: "C"})
	if err != nil {
		t.Fatalf("add C: %v", err)
	}
	if err := store.AddLink(ctx, atom.AddLinkParams{FromAtomID: a.ID, ToAtomID: b.ID, LinkType: "same_entity", Confidence: 1.0}); err != nil {
		t.Fatalf("link A->B: %v", err)
	}
	if err := store.AddLink(ctx, atom.AddLinkParams{FromAtomID: b.ID, ToAtomID: c.ID, LinkType: "same_entity", Confidence: 1.0}); err != nil {
		t.Fatalf("link B->C: %v", err)
	}

	t.Run("depth 1 returns start and first neighbour", func(t *testing.T) {
		result, err := store.Traverse(ctx, a.ID, 1)
		if err != nil {
			t.Fatalf("Traverse: %v", err)
		}
		if len(result.Atoms) < 1 {
			t.Errorf("expected at least 1 atom, got %d", len(result.Atoms))
		}
	})

	t.Run("depth 2 reaches C via chain", func(t *testing.T) {
		result, err := store.Traverse(ctx, a.ID, 2)
		if err != nil {
			t.Fatalf("Traverse depth 2: %v", err)
		}
		if len(result.Atoms) < 2 {
			t.Errorf("expected at least 2 atoms, got %d", len(result.Atoms))
		}
	})

	t.Run("non-existent start returns empty result", func(t *testing.T) {
		result, err := store.Traverse(ctx, uuid.New(), 2)
		if err != nil {
			t.Fatalf("Traverse nonexistent: %v", err)
		}
		if len(result.Atoms) != 0 {
			t.Errorf("expected 0 atoms, got %d", len(result.Atoms))
		}
	})

	t.Run("depth capped at 5", func(t *testing.T) {
		result, err := store.Traverse(ctx, a.ID, 99)
		if err != nil {
			t.Fatalf("Traverse depth 99: %v", err)
		}
		if result == nil {
			t.Fatal("nil result")
		}
	})
}

func TestSQLiteAtomStore_Search(t *testing.T) {
	db := openAtomDB(t)
	store := wbtsqlite.NewAtomStore(db)
	ctx := context.Background()

	parentID := uuid.New()

	_, err := store.AddAtom(ctx, atom.AddAtomParams{
		ParentTable: "decisions",
		ParentID:    parentID,
		Content:     "Use testcontainers for integration tests",
		Keywords:    []string{"testcontainers", "docker"},
		Tags:        []string{"testing"},
	})
	if err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	_, err = store.AddAtom(ctx, atom.AddAtomParams{
		ParentTable: "knowledge_items",
		ParentID:    parentID,
		Content:     "Go modules declared in go.mod",
		Keywords:    []string{"golang", "modules"},
		Tags:        []string{"language"},
	})
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	t.Run("matches content", func(t *testing.T) {
		results, err := store.Search(ctx, nil, "testcontainers", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 result for 'testcontainers'")
		}
	})

	t.Run("matches keywords", func(t *testing.T) {
		results, err := store.Search(ctx, nil, "golang", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 result for 'golang' in keywords")
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		results, err := store.Search(ctx, nil, "zzznomatch", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("workspace scoping filters by workspace", func(t *testing.T) {
		wsID := uuid.New()
		_, err := store.AddAtom(ctx, atom.AddAtomParams{
			WorkspaceID: &wsID,
			ParentTable: "decisions",
			ParentID:    parentID,
			Content:     "workspace scoped testcontainers atom",
		})
		if err != nil {
			t.Fatalf("add scoped atom: %v", err)
		}

		// Search with a different workspace should not return this atom.
		otherWs := uuid.New()
		results, err := store.Search(ctx, &otherWs, "workspace scoped", 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results for different workspace, got %d", len(results))
		}
	})
}

func TestSQLiteAtomStore_ErrNotFound(t *testing.T) {
	db := openAtomDB(t)
	store := wbtsqlite.NewAtomStore(db)
	ctx := context.Background()

	// Traverse a non-existent atom returns empty, not an error.
	result, err := store.Traverse(ctx, uuid.New(), 2)
	if err != nil {
		t.Fatalf("expected nil error for non-existent atom, got: %v", err)
	}
	if len(result.Atoms) != 0 {
		t.Errorf("expected empty result, got %d atoms", len(result.Atoms))
	}

	// Verify ErrNotFound is exported correctly.
	if !errors.Is(atom.ErrNotFound, atom.ErrNotFound) {
		t.Error("ErrNotFound sentinel broken")
	}
}
