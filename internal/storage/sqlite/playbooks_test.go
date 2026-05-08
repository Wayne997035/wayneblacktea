package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/playbook"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

func openPlaybookDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSQLitePlaybookStore_Create(t *testing.T) {
	db := openPlaybookDB(t)
	store := wbtsqlite.NewPlaybookStore(db)
	ctx := context.Background()

	tests := []struct {
		name    string
		params  playbook.CreateParams
		wantErr bool
	}{
		{
			name: "happy path with all fields",
			params: playbook.CreateParams{
				TriggerPattern:    "Before adding a new domain package",
				ActionTemplate:    "Check existing patterns in internal/vision/; follow same Store+StoreIface layout",
				SourceDecisionIDs: []uuid.UUID{uuid.New(), uuid.New()},
				Confidence:        0.85,
			},
		},
		{
			name: "minimal required fields with zero confidence uses default",
			params: playbook.CreateParams{
				TriggerPattern: "When choosing DB backend",
				ActionTemplate: "Check STORAGE_BACKEND env; default to Postgres",
				Confidence:     0,
			},
		},
		{
			name: "nil source decision ids becomes empty slice",
			params: playbook.CreateParams{
				TriggerPattern:    "Trigger nil IDs",
				ActionTemplate:    "Action nil IDs",
				SourceDecisionIDs: nil,
				Confidence:        0.5,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pb, err := store.Create(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if pb.ID == (uuid.UUID{}) {
				t.Error("ID should not be zero")
			}
			if pb.TriggerPattern != tc.params.TriggerPattern {
				t.Errorf("TriggerPattern: got %q, want %q", pb.TriggerPattern, tc.params.TriggerPattern)
			}
			if pb.ActionTemplate != tc.params.ActionTemplate {
				t.Errorf("ActionTemplate: got %q, want %q", pb.ActionTemplate, tc.params.ActionTemplate)
			}
			if pb.SourceDecisionIDs == nil {
				t.Error("SourceDecisionIDs should not be nil")
			}
			if tc.params.Confidence == 0 {
				// default is 0.50
				if pb.Confidence != 0.50 {
					t.Errorf("default Confidence: got %f, want 0.50", pb.Confidence)
				}
			} else {
				if pb.Confidence != tc.params.Confidence {
					t.Errorf("Confidence: got %f, want %f", pb.Confidence, tc.params.Confidence)
				}
			}
			if pb.Hits != 0 {
				t.Errorf("Hits: got %d, want 0", pb.Hits)
			}
		})
	}
}

//nolint:gocyclo // table-driven filter test with 5 sub-scenarios; splitting would obscure shared setup
func TestSQLitePlaybookStore_List(t *testing.T) {
	db := openPlaybookDB(t)
	store := wbtsqlite.NewPlaybookStore(db)
	ctx := context.Background()

	// Seed two playbooks.
	_, err := store.Create(ctx, playbook.CreateParams{
		TriggerPattern: "Before adding a migration",
		ActionTemplate: "Read backend-security-design.md §6",
		Confidence:     0.90,
	})
	if err != nil {
		t.Fatalf("seed 1: %v", err)
	}
	_, err = store.Create(ctx, playbook.CreateParams{
		TriggerPattern: "When writing a new HTTP handler",
		ActionTemplate: "Add io.LimitReader to all body reads",
		Confidence:     0.75,
	})
	if err != nil {
		t.Fatalf("seed 2: %v", err)
	}

	t.Run("list all returns both ordered by confidence desc", func(t *testing.T) {
		items, err := store.List(ctx, playbook.ListParams{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(items) < 2 {
			t.Errorf("expected >= 2, got %d", len(items))
		}
		// First item should have higher or equal confidence.
		if items[0].Confidence < items[1].Confidence {
			t.Errorf("not ordered by confidence DESC: %f < %f", items[0].Confidence, items[1].Confidence)
		}
	})

	t.Run("keyword filter matches trigger_pattern", func(t *testing.T) {
		items, err := store.List(ctx, playbook.ListParams{
			ContextKeywords: []string{"migration"},
		})
		if err != nil {
			t.Fatalf("List with keyword: %v", err)
		}
		if len(items) == 0 {
			t.Fatal("expected at least 1 result for keyword 'migration'")
		}
		found := false
		for _, pb := range items {
			if pb.TriggerPattern == "Before adding a migration" {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected migration playbook in results")
		}
	})

	t.Run("keyword filter matches action_template", func(t *testing.T) {
		items, err := store.List(ctx, playbook.ListParams{
			ContextKeywords: []string{"LimitReader"},
		})
		if err != nil {
			t.Fatalf("List with action keyword: %v", err)
		}
		if len(items) == 0 {
			t.Fatal("expected at least 1 result for keyword 'LimitReader'")
		}
	})

	t.Run("empty keyword list returns all", func(t *testing.T) {
		items, err := store.List(ctx, playbook.ListParams{
			ContextKeywords: []string{},
		})
		if err != nil {
			t.Fatalf("List with empty keywords: %v", err)
		}
		if len(items) < 2 {
			t.Errorf("expected >= 2, got %d", len(items))
		}
	})

	t.Run("no match keyword returns empty", func(t *testing.T) {
		items, err := store.List(ctx, playbook.ListParams{
			ContextKeywords: []string{"zzz_nonexistent_zzz"},
		})
		if err != nil {
			t.Fatalf("List with no-match keyword: %v", err)
		}
		if len(items) != 0 {
			t.Errorf("expected 0 results, got %d", len(items))
		}
	})
}

func TestSQLitePlaybookStore_IncrementHits(t *testing.T) {
	db := openPlaybookDB(t)
	store := wbtsqlite.NewPlaybookStore(db)
	ctx := context.Background()

	pb, err := store.Create(ctx, playbook.CreateParams{
		TriggerPattern: "Increment test",
		ActionTemplate: "Action",
		Confidence:     0.6,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("happy path increments hits", func(t *testing.T) {
		if err := store.IncrementHits(ctx, pb.ID); err != nil {
			t.Fatalf("IncrementHits: %v", err)
		}
		// List and check hits.
		items, err := store.List(ctx, playbook.ListParams{})
		if err != nil {
			t.Fatalf("List after increment: %v", err)
		}
		var found *playbook.Playbook
		for _, item := range items {
			if item.ID == pb.ID {
				found = item
				break
			}
		}
		if found == nil {
			t.Fatal("playbook not found after increment")
		}
		if found.Hits != 1 {
			t.Errorf("Hits: got %d, want 1", found.Hits)
		}
		if found.LastUsedAt == nil {
			t.Error("LastUsedAt should be set after IncrementHits")
		}
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		err := store.IncrementHits(ctx, uuid.New())
		if !errors.Is(err, playbook.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})
}
