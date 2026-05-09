package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openProceduralDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteProceduralStore_Add(t *testing.T) {
	db := openProceduralDB(t)
	store := wbtsqlite.NewProceduralStore(db)
	ctx := context.Background()

	tests := []struct {
		name    string
		params  procedural.AddParams
		wantErr bool
	}{
		{
			name: "happy path all fields",
			params: procedural.AddParams{
				RepoName:     "wayneblacktea",
				Title:        "Run migrations",
				WhenToUse:    "Before any deploy",
				ApproachMD:   "cd build && task migrate-up",
				ToolsUsed:    []string{"Bash"},
				FilesTouched: []string{"migrations/"},
			},
		},
		{
			name: "minimal fields",
			params: procedural.AddParams{
				Title: "Minimal",
			},
		},
		{
			name: "nil slices become empty",
			params: procedural.AddParams{
				Title:        "Nil slices",
				ToolsUsed:    nil,
				FilesTouched: nil,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mem, err := store.Add(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if mem == nil {
				t.Fatal("Add returned nil")
			}
			if mem.Title != tc.params.Title {
				t.Errorf("Title: got %q, want %q", mem.Title, tc.params.Title)
			}
			if mem.ToolsUsed == nil {
				t.Error("ToolsUsed should not be nil")
			}
			if mem.FilesTouched == nil {
				t.Error("FilesTouched should not be nil")
			}
		})
	}
}

func TestSQLiteProceduralStore_Query(t *testing.T) {
	db := openProceduralDB(t)
	store := wbtsqlite.NewProceduralStore(db)
	ctx := context.Background()

	_, err := store.Add(ctx, procedural.AddParams{
		RepoName:   "wayneblacktea",
		Title:      "Run migrations",
		WhenToUse:  "Before deploy",
		ApproachMD: "task migrate-up",
	})
	if err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	_, err = store.Add(ctx, procedural.AddParams{
		RepoName:   "other-repo",
		Title:      "Lint code",
		WhenToUse:  "Before PR",
		ApproachMD: "golangci-lint run ./...",
	})
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	t.Run("keyword matches title", func(t *testing.T) {
		results, err := store.Query(ctx, procedural.QueryFilter{Keywords: "migrations"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("expected 1, got %d", len(results))
		}
	})

	t.Run("repo filter", func(t *testing.T) {
		results, err := store.Query(ctx, procedural.QueryFilter{RepoName: "other-repo"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("expected 1 for other-repo, got %d", len(results))
		}
	})

	t.Run("no filter returns all", func(t *testing.T) {
		results, err := store.Query(ctx, procedural.QueryFilter{})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(results) < 2 {
			t.Errorf("expected at least 2, got %d", len(results))
		}
	})

	t.Run("non-matching keyword returns empty", func(t *testing.T) {
		results, err := store.Query(ctx, procedural.QueryFilter{Keywords: "zzznomatch"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0, got %d", len(results))
		}
	})
}

func TestSQLiteProceduralStore_MarkUsed(t *testing.T) {
	db := openProceduralDB(t)
	store := wbtsqlite.NewProceduralStore(db)
	ctx := context.Background()

	mem, err := store.Add(ctx, procedural.AddParams{Title: "MarkUsed test"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	t.Run("increments count and sets last_used_at", func(t *testing.T) {
		updated, err := store.MarkUsed(ctx, mem.ID)
		if err != nil {
			t.Fatalf("MarkUsed: %v", err)
		}
		if updated.SuccessCount != 1 {
			t.Errorf("SuccessCount: got %d, want 1", updated.SuccessCount)
		}
		if updated.LastUsedAt == nil {
			t.Error("LastUsedAt should be set after MarkUsed")
		}
	})

	t.Run("second call increments again", func(t *testing.T) {
		updated, err := store.MarkUsed(ctx, mem.ID)
		if err != nil {
			t.Fatalf("MarkUsed 2nd: %v", err)
		}
		if updated.SuccessCount != 2 {
			t.Errorf("SuccessCount: got %d, want 2", updated.SuccessCount)
		}
	})

	t.Run("unknown id returns ErrNotFound", func(t *testing.T) {
		_, err := store.MarkUsed(ctx, uuid.New())
		if !errors.Is(err, procedural.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})
}
