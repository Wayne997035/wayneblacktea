package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openReflectionDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLiteReflectionStore_Create(t *testing.T) {
	db := openReflectionDB(t)
	store := wbtsqlite.NewReflectionStore(db)
	ctx := context.Background()

	wsID := uuid.New()
	relType := "task"
	relID := uuid.New()

	tests := []struct {
		name    string
		params  reflection.CreateParams
		wantErr bool
	}{
		{
			name: "happy path with all fields",
			params: reflection.CreateParams{
				WorkspaceID:       &wsID,
				Type:              "daily",
				RelatedEntityType: &relType,
				RelatedEntityID:   &relID,
				Summary:           "Today was productive",
				Insights:          json.RawMessage(`["insight one","insight two"]`),
				PatternsDetected:  json.RawMessage(`{"pattern":"morning focus"}`),
				SuggestedActions:  json.RawMessage(`["review backlog"]`),
				Confidence:        0.85,
			},
		},
		{
			name: "minimal required fields",
			params: reflection.CreateParams{
				WorkspaceID: &wsID,
				Type:        "weekly",
				Summary:     "Weekly review",
			},
		},
		{
			name: "nil JSONB fields stored correctly",
			params: reflection.CreateParams{
				WorkspaceID: &wsID,
				Type:        "system",
				Summary:     "System check",
				Confidence:  0.5,
			},
		},
		{
			name: "confidence boundary 1.0",
			params: reflection.CreateParams{
				WorkspaceID: &wsID,
				Type:        "decision",
				Summary:     "High confidence",
				Confidence:  1.0,
			},
		},
		{
			name:    "invalid type returns error",
			params:  reflection.CreateParams{WorkspaceID: &wsID, Type: "invalid_type"},
			wantErr: true,
		},
		{
			name:    "confidence above 1.0 returns error",
			params:  reflection.CreateParams{WorkspaceID: &wsID, Type: "daily", Confidence: 1.5},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := store.Create(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if r == nil {
				t.Fatal("Create returned nil")
			}
			if r.Type != tc.params.Type {
				t.Errorf("Type: got %q, want %q", r.Type, tc.params.Type)
			}
			if r.Summary != tc.params.Summary {
				t.Errorf("Summary: got %q, want %q", r.Summary, tc.params.Summary)
			}
			if r.Confidence != tc.params.Confidence {
				t.Errorf("Confidence: got %f, want %f", r.Confidence, tc.params.Confidence)
			}
			if r.ID == uuid.Nil {
				t.Error("ID should not be nil")
			}
		})
	}
}

func TestSQLiteReflectionStore_List(t *testing.T) {
	db := openReflectionDB(t)
	store := wbtsqlite.NewReflectionStore(db)
	ctx := context.Background()

	wsID := uuid.New()

	// Seed: 2 daily, 1 weekly
	for i := 0; i < 2; i++ {
		_, err := store.Create(ctx, reflection.CreateParams{
			WorkspaceID: &wsID,
			Type:        "daily",
			Summary:     "daily reflection",
		})
		if err != nil {
			t.Fatalf("seed daily %d: %v", i, err)
		}
	}
	_, err := store.Create(ctx, reflection.CreateParams{
		WorkspaceID: &wsID,
		Type:        "weekly",
		Summary:     "weekly reflection",
	})
	if err != nil {
		t.Fatalf("seed weekly: %v", err)
	}

	t.Run("list all returns all seeded reflections", func(t *testing.T) {
		results, err := store.List(ctx, reflection.ListParams{WorkspaceID: &wsID})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(results) < 3 {
			t.Errorf("expected at least 3 results, got %d", len(results))
		}
	})

	t.Run("type filter returns only matching type", func(t *testing.T) {
		weekly := "weekly"
		results, err := store.List(ctx, reflection.ListParams{WorkspaceID: &wsID, Type: &weekly})
		if err != nil {
			t.Fatalf("List with type filter: %v", err)
		}
		for _, r := range results {
			if r.Type != "weekly" {
				t.Errorf("expected type=weekly, got %q", r.Type)
			}
		}
		if len(results) == 0 {
			t.Error("expected at least 1 weekly result")
		}
	})

	t.Run("no match returns empty slice", func(t *testing.T) {
		otherWs := uuid.New()
		results, err := store.List(ctx, reflection.ListParams{WorkspaceID: &otherWs})
		if err != nil {
			t.Fatalf("List no match: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("limit is honoured", func(t *testing.T) {
		results, err := store.List(ctx, reflection.ListParams{WorkspaceID: &wsID, Limit: 1})
		if err != nil {
			t.Fatalf("List with limit: %v", err)
		}
		if len(results) > 1 {
			t.Errorf("expected at most 1 result, got %d", len(results))
		}
	})
}

func TestSQLiteReflectionStore_GetLatest(t *testing.T) {
	db := openReflectionDB(t)
	store := wbtsqlite.NewReflectionStore(db)
	ctx := context.Background()

	wsID := uuid.New()

	t.Run("ErrNotFound when no reflections exist", func(t *testing.T) {
		_, err := store.GetLatest(ctx, &wsID, "daily")
		if !errors.Is(err, reflection.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	created, err := store.Create(ctx, reflection.CreateParams{
		WorkspaceID: &wsID,
		Type:        "daily",
		Summary:     "first daily",
	})
	if err != nil {
		t.Fatalf("seed daily: %v", err)
	}

	t.Run("returns the created reflection", func(t *testing.T) {
		latest, err := store.GetLatest(ctx, &wsID, "daily")
		if err != nil {
			t.Fatalf("GetLatest: %v", err)
		}
		if latest.ID != created.ID {
			t.Errorf("ID mismatch: got %s, want %s", latest.ID, created.ID)
		}
	})

	t.Run("different type returns ErrNotFound", func(t *testing.T) {
		_, err := store.GetLatest(ctx, &wsID, "knowledge")
		if !errors.Is(err, reflection.ErrNotFound) {
			t.Errorf("expected ErrNotFound for type 'knowledge', got %v", err)
		}
	})
}

func TestSQLiteReflectionStore_RecentWithPatterns(t *testing.T) {
	db := openReflectionDB(t)
	store := wbtsqlite.NewReflectionStore(db)
	ctx := context.Background()

	wsID := uuid.New()

	t.Run("empty result when no reflections with patterns", func(t *testing.T) {
		since := time.Now().Add(-24 * time.Hour)
		results, err := store.RecentWithPatterns(ctx, &wsID, since, 10)
		if err != nil {
			t.Fatalf("RecentWithPatterns: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results, got %d", len(results))
		}
	})

	// Seed: one reflection without patterns, one with patterns.
	_, err := store.Create(ctx, reflection.CreateParams{
		WorkspaceID: &wsID,
		Type:        "daily",
		Summary:     "no patterns",
	})
	if err != nil {
		t.Fatalf("seed no-pattern: %v", err)
	}
	_, err = store.Create(ctx, reflection.CreateParams{
		WorkspaceID:      &wsID,
		Type:             "weekly",
		Summary:          "with patterns",
		PatternsDetected: json.RawMessage(`{"pattern":"recurring delay"}`),
	})
	if err != nil {
		t.Fatalf("seed with-pattern: %v", err)
	}

	t.Run("returns only reflections with patterns", func(t *testing.T) {
		since := time.Now().Add(-1 * time.Hour)
		results, err := store.RecentWithPatterns(ctx, &wsID, since, 10)
		if err != nil {
			t.Fatalf("RecentWithPatterns after seed: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("expected 1 result, got %d", len(results))
		}
		if len(results) > 0 && results[0].PatternsDetected == nil {
			t.Error("patterns_detected should not be nil")
		}
	})

	t.Run("since in future returns empty", func(t *testing.T) {
		since := time.Now().Add(1 * time.Hour)
		results, err := store.RecentWithPatterns(ctx, &wsID, since, 10)
		if err != nil {
			t.Fatalf("RecentWithPatterns future since: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results with future since, got %d", len(results))
		}
	})

	t.Run("different workspace returns empty", func(t *testing.T) {
		otherWs := uuid.New()
		since := time.Now().Add(-1 * time.Hour)
		results, err := store.RecentWithPatterns(ctx, &otherWs, since, 10)
		if err != nil {
			t.Fatalf("RecentWithPatterns other ws: %v", err)
		}
		if len(results) != 0 {
			t.Errorf("expected 0 results for different workspace, got %d", len(results))
		}
	})
}

// TestSQLiteReflectionStore_PruneOlderThan verifies that PruneOlderThan removes
// old reflections but leaves newer rows untouched. Mirrors the PG integration
// test to ensure dual-backend parity per backend-security-design.md §6.5.
func TestSQLiteReflectionStore_PruneOlderThan(t *testing.T) {
	db := openReflectionDB(t)
	store := wbtsqlite.NewReflectionStore(db)
	ctx := context.Background()

	wsID := uuid.New()

	// Seed a recent reflection.
	_, err := store.Create(ctx, reflection.CreateParams{
		WorkspaceID: &wsID,
		Type:        "daily",
		Summary:     "recent reflection for prune test",
		Confidence:  0.5,
	})
	if err != nil {
		t.Fatalf("Create (recent): %v", err)
	}

	// Prune with a cutoff in the past (nothing should be deleted).
	oldCutoff := time.Now().Add(-365 * 24 * time.Hour)
	n, err := store.PruneOlderThan(ctx, oldCutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan (old cutoff): %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 pruned rows with old cutoff, got %d", n)
	}

	// Prune with a future cutoff (recent row should be pruned).
	futureCutoff := time.Now().Add(1 * time.Minute)
	n, err = store.PruneOlderThan(ctx, futureCutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan (future cutoff): %v", err)
	}
	if n == 0 {
		t.Error("expected at least 1 pruned row, got 0")
	}

	// Workspace should now have no daily reflections.
	daily := "daily"
	results, err := store.List(ctx, reflection.ListParams{WorkspaceID: &wsID, Type: &daily})
	if err != nil {
		t.Fatalf("List after prune: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after prune, got %d", len(results))
	}

	// Prune on empty store returns 0 without error.
	n, err = store.PruneOlderThan(ctx, futureCutoff)
	if err != nil {
		t.Fatalf("PruneOlderThan on empty store: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 rows deleted on empty store, got %d", n)
	}
}
