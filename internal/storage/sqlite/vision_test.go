package sqlite_test

import (
	"context"
	"errors"
	"testing"

	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/vision"
	"github.com/google/uuid"
)

// openVisionDB opens an in-memory SQLite DB for vision store tests.
func openVisionDB(t *testing.T) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---- VisionStore tests ----

func TestSQLiteVisionStore_Add(t *testing.T) {
	db := openVisionDB(t)
	store := wbtsqlite.NewVisionStore(db)
	ctx := context.Background()

	tests := []struct {
		name    string
		params  vision.AddVisionParams
		wantErr bool
	}{
		{
			name: "happy path with all fields",
			params: vision.AddVisionParams{
				Title:            "SQLite vector search",
				WhyBlocked:       "Needs FTS5 setup",
				DependsOn:        []string{"dep-1", "dep-2"},
				ParentInitiative: "search-2027",
				RepoName:         "wayneblacktea",
				ContextMD:        "Will use SQLite FTS5 extension.",
			},
		},
		{
			name: "minimal required fields",
			params: vision.AddVisionParams{
				Title:      "Minimal",
				WhyBlocked: "Not ready",
			},
		},
		{
			name: "nil depends_on becomes empty slice",
			params: vision.AddVisionParams{
				Title:      "Nil deps",
				WhyBlocked: "reason",
				DependsOn:  nil,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			item, err := store.Add(ctx, tc.params)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Add: %v", err)
			}
			if item.Title != tc.params.Title {
				t.Errorf("Title: got %q, want %q", item.Title, tc.params.Title)
			}
			if item.Status != vision.VisionStatusOpen {
				t.Errorf("Status: got %q, want open", item.Status)
			}
			if item.DependsOn == nil {
				t.Error("DependsOn should not be nil")
			}
		})
	}
}

func TestSQLiteVisionStore_GetByID(t *testing.T) {
	db := openVisionDB(t)
	store := wbtsqlite.NewVisionStore(db)
	ctx := context.Background()

	item, err := store.Add(ctx, vision.AddVisionParams{
		Title:      "Get by ID",
		WhyBlocked: "testing",
		ContextMD:  "Context details here.",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	t.Run("happy path", func(t *testing.T) {
		got, err := store.GetByID(ctx, item.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.ID != item.ID {
			t.Errorf("ID: got %s, want %s", got.ID, item.ID)
		}
		if got.ContextMD != "Context details here." {
			t.Errorf("ContextMD: got %q, want non-empty", got.ContextMD)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.GetByID(ctx, uuid.New())
		if !errors.Is(err, vision.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})
}

func TestSQLiteVisionStore_List(t *testing.T) { //nolint:gocyclo // table-driven filter test requires many branches
	db := openVisionDB(t)
	store := wbtsqlite.NewVisionStore(db)
	ctx := context.Background()

	// Seed items.
	_, err := store.Add(ctx, vision.AddVisionParams{
		Title:            "Open idea",
		WhyBlocked:       "No time",
		ParentInitiative: "roadmap-x",
	})
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}

	discussing, err := store.Add(ctx, vision.AddVisionParams{
		Title:            "Discussing idea",
		WhyBlocked:       "Waiting on decision",
		ParentInitiative: "roadmap-x",
	})
	if err != nil {
		t.Fatalf("seed discussing: %v", err)
	}
	statusD := vision.VisionStatusDiscussing
	_, err = store.Update(ctx, discussing.ID, vision.UpdateVisionParams{Status: &statusD})
	if err != nil {
		t.Fatalf("update to discussing: %v", err)
	}

	dismissed, err := store.Add(ctx, vision.AddVisionParams{
		Title:      "Dismissed idea",
		WhyBlocked: "Rejected",
	})
	if err != nil {
		t.Fatalf("seed dismissed: %v", err)
	}
	statusX := vision.VisionStatusDismissed
	_, err = store.Update(ctx, dismissed.ID, vision.UpdateVisionParams{Status: &statusX})
	if err != nil {
		t.Fatalf("update to dismissed: %v", err)
	}

	t.Run("default excludes dismissed", func(t *testing.T) {
		items, err := store.List(ctx, vision.ListVisionFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, it := range items {
			if it.Status == vision.VisionStatusDismissed {
				t.Errorf("List returned dismissed item: %v", it.ID)
			}
		}
		if len(items) < 2 {
			t.Errorf("expected >= 2 non-dismissed, got %d", len(items))
		}
	})

	t.Run("filter by initiative", func(t *testing.T) {
		items, err := store.List(ctx, vision.ListVisionFilter{ParentInitiative: "roadmap-x"})
		if err != nil {
			t.Fatalf("List with initiative: %v", err)
		}
		for _, it := range items {
			if it.ParentInitiative != "roadmap-x" {
				t.Errorf("expected only roadmap-x, got %q", it.ParentInitiative)
			}
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		items, err := store.List(ctx, vision.ListVisionFilter{Status: vision.VisionStatusDiscussing})
		if err != nil {
			t.Fatalf("List with status filter: %v", err)
		}
		for _, it := range items {
			if it.Status != vision.VisionStatusDiscussing {
				t.Errorf("expected only discussing, got %q", it.Status)
			}
		}
	})
}

func TestSQLiteVisionStore_Update(t *testing.T) {
	db := openVisionDB(t)
	store := wbtsqlite.NewVisionStore(db)
	ctx := context.Background()

	item, err := store.Add(ctx, vision.AddVisionParams{
		Title:      "Update me",
		WhyBlocked: "Original reason",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	t.Run("update status", func(t *testing.T) {
		st := vision.VisionStatusMaturing
		updated, err := store.Update(ctx, item.ID, vision.UpdateVisionParams{Status: &st})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if updated.Status != vision.VisionStatusMaturing {
			t.Errorf("Status: got %q, want maturing", updated.Status)
		}
		if updated.LastDiscussedAt == nil {
			t.Error("LastDiscussedAt should be set after update")
		}
	})

	t.Run("update context_md", func(t *testing.T) {
		newCtx := "New context details."
		updated, err := store.Update(ctx, item.ID, vision.UpdateVisionParams{ContextMD: &newCtx})
		if err != nil {
			t.Fatalf("Update context_md: %v", err)
		}
		if updated.ContextMD != newCtx {
			t.Errorf("ContextMD: got %q, want %q", updated.ContextMD, newCtx)
		}
	})

	t.Run("not found", func(t *testing.T) {
		st := vision.VisionStatusOpen
		_, err := store.Update(ctx, uuid.New(), vision.UpdateVisionParams{Status: &st})
		if !errors.Is(err, vision.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})
}

func TestSQLiteVisionStore_Promote(t *testing.T) {
	db := openVisionDB(t)
	store := wbtsqlite.NewVisionStore(db)
	ctx := context.Background()

	item, err := store.Add(ctx, vision.AddVisionParams{
		Title:      "Promote me",
		WhyBlocked: "Was blocked, now ready",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	t.Run("happy path", func(t *testing.T) {
		taskID := uuid.New()
		promoted, err := store.Promote(ctx, item.ID, vision.PromoteParams{PromotedTaskID: taskID})
		if err != nil {
			t.Fatalf("Promote: %v", err)
		}
		if promoted.Status != vision.VisionStatusPromoted {
			t.Errorf("Status: got %q, want promoted", promoted.Status)
		}
		if promoted.PromotedTaskID == nil || *promoted.PromotedTaskID != taskID {
			t.Errorf("PromotedTaskID: got %v, want %s", promoted.PromotedTaskID, taskID)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := store.Promote(ctx, uuid.New(), vision.PromoteParams{PromotedTaskID: uuid.New()})
		if !errors.Is(err, vision.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})
}
