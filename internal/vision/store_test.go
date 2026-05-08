package vision_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/vision"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are migration files that cannot be run by pgx because they
// contain psql metacommands. These are the same as the GTD store test.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true, // psql `\set` metacommand
}

// openTestPgPool starts a throwaway Postgres container, applies all migrations,
// and returns a pgxpool. Skipped with -short flag (requires Docker).
func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}

	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if tErr := container.Terminate(ctx); tErr != nil {
			t.Logf("terminate container: %v", tErr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	applyAllUpMigrations(t, ctx, pool)
	return pool
}

func applyAllUpMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read embedded migrations dir: %v", err)
	}
	var ups []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		ups = append(ups, name)
	}
	sort.Strings(ups)

	for _, name := range ups {
		if skipMigrations[name] {
			t.Logf("applyAllUpMigrations: skipping %s (psql-metacommand-only file)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

// TestStore_Add tests the happy path and edge cases of Add.
func TestStore_Add(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := vision.NewStore(pool, &wsID)
	ctx := context.Background()

	tests := []struct {
		name    string
		params  vision.AddVisionParams
		wantErr bool
	}{
		{
			name: "happy path with all fields",
			params: vision.AddVisionParams{
				WorkspaceID:      &wsID,
				RepoName:         "wayneblacktea",
				Title:            "Implement vector search",
				WhyBlocked:       "Needs pgvector setup first",
				DependsOn:        []string{"task-abc", "task-def"},
				ParentInitiative: "search-revamp",
				ContextMD:        "Full-text search is not enough for semantic queries.",
			},
		},
		{
			name: "minimal required fields only",
			params: vision.AddVisionParams{
				WorkspaceID: &wsID,
				Title:       "Minimal vision",
				WhyBlocked:  "No capacity",
			},
		},
		{
			name: "empty depends_on becomes empty slice",
			params: vision.AddVisionParams{
				WorkspaceID: &wsID,
				Title:       "Empty depends",
				WhyBlocked:  "Not ready",
				DependsOn:   nil,
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
			if item == nil {
				t.Fatal("Add returned nil item")
			}
			if item.Title != tc.params.Title {
				t.Errorf("Title: got %q, want %q", item.Title, tc.params.Title)
			}
			if item.WhyBlocked != tc.params.WhyBlocked {
				t.Errorf("WhyBlocked: got %q, want %q", item.WhyBlocked, tc.params.WhyBlocked)
			}
			if item.Status != vision.VisionStatusOpen {
				t.Errorf("Status: got %q, want %q", item.Status, vision.VisionStatusOpen)
			}
			if item.DependsOn == nil {
				t.Errorf("DependsOn should not be nil, got nil slice")
			}
		})
	}
}

// TestStore_List tests List with status and initiative filters.
func TestStore_List(t *testing.T) { //nolint:gocyclo // table-driven filter test requires many branches
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := vision.NewStore(pool, &wsID)
	ctx := context.Background()

	// Seed items.
	_, err := store.Add(ctx, vision.AddVisionParams{
		WorkspaceID:      &wsID,
		Title:            "Open item",
		WhyBlocked:       "Reason A",
		ParentInitiative: "roadmap-2027",
	})
	if err != nil {
		t.Fatalf("seed open item: %v", err)
	}

	discussing, err := store.Add(ctx, vision.AddVisionParams{
		WorkspaceID:      &wsID,
		Title:            "Discussing item",
		WhyBlocked:       "Reason B",
		ParentInitiative: "roadmap-2027",
	})
	if err != nil {
		t.Fatalf("seed discussing item: %v", err)
	}

	// Update one to discussing.
	status := vision.VisionStatusDiscussing
	_, err = store.Update(ctx, discussing.ID, vision.UpdateVisionParams{Status: &status})
	if err != nil {
		t.Fatalf("update to discussing: %v", err)
	}

	// Add a dismissed item (should be excluded from default listing).
	dismissed, err := store.Add(ctx, vision.AddVisionParams{
		WorkspaceID: &wsID,
		Title:       "Dismissed item",
		WhyBlocked:  "Reason C",
	})
	if err != nil {
		t.Fatalf("seed dismissed item: %v", err)
	}
	dismissedStatus := vision.VisionStatusDismissed
	_, err = store.Update(ctx, dismissed.ID, vision.UpdateVisionParams{Status: &dismissedStatus})
	if err != nil {
		t.Fatalf("update to dismissed: %v", err)
	}

	t.Run("default list excludes dismissed", func(t *testing.T) {
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
			t.Errorf("expected at least 2 non-dismissed items, got %d", len(items))
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

	t.Run("filter by initiative", func(t *testing.T) {
		items, err := store.List(ctx, vision.ListVisionFilter{ParentInitiative: "roadmap-2027"})
		if err != nil {
			t.Fatalf("List with initiative filter: %v", err)
		}
		for _, it := range items {
			if it.ParentInitiative != "roadmap-2027" {
				t.Errorf("expected only roadmap-2027, got %q", it.ParentInitiative)
			}
		}
		if len(items) < 2 {
			t.Errorf("expected at least 2 items for roadmap-2027, got %d", len(items))
		}
	})
}

// TestStore_GetByID tests GetByID happy path and not-found case.
func TestStore_GetByID(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := vision.NewStore(pool, &wsID)
	ctx := context.Background()

	item, err := store.Add(ctx, vision.AddVisionParams{
		WorkspaceID: &wsID,
		Title:       "Get by ID test",
		WhyBlocked:  "Testing",
		ContextMD:   "Some detailed context here.",
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
		// GetByID returns context_md (unlike List summary).
		if got.ContextMD != "Some detailed context here." {
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

// TestStore_Update tests Update with various combinations of fields.
func TestStore_Update(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := vision.NewStore(pool, &wsID)
	ctx := context.Background()

	item, err := store.Add(ctx, vision.AddVisionParams{
		WorkspaceID: &wsID,
		Title:       "Update test",
		WhyBlocked:  "Original reason",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	t.Run("update status only", func(t *testing.T) {
		status := vision.VisionStatusDiscussing
		updated, err := store.Update(ctx, item.ID, vision.UpdateVisionParams{Status: &status})
		if err != nil {
			t.Fatalf("Update status: %v", err)
		}
		if updated.Status != vision.VisionStatusDiscussing {
			t.Errorf("Status: got %q, want %q", updated.Status, vision.VisionStatusDiscussing)
		}
		if updated.LastDiscussedAt == nil {
			t.Error("LastDiscussedAt should be set after update")
		}
	})

	t.Run("update context_md", func(t *testing.T) {
		newCtx := "Updated context with more detail."
		updated, err := store.Update(ctx, item.ID, vision.UpdateVisionParams{ContextMD: &newCtx})
		if err != nil {
			t.Fatalf("Update context_md: %v", err)
		}
		if updated.ContextMD != newCtx {
			t.Errorf("ContextMD: got %q, want %q", updated.ContextMD, newCtx)
		}
	})

	t.Run("explicit last_discussed_at", func(t *testing.T) {
		ts := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
		updated, err := store.Update(ctx, item.ID, vision.UpdateVisionParams{LastDiscussedAt: &ts})
		if err != nil {
			t.Fatalf("Update with explicit timestamp: %v", err)
		}
		if updated.LastDiscussedAt == nil {
			t.Fatal("LastDiscussedAt should be set")
		}
		if !updated.LastDiscussedAt.Equal(ts) {
			t.Errorf("LastDiscussedAt: got %v, want %v", updated.LastDiscussedAt, ts)
		}
	})

	t.Run("not found", func(t *testing.T) {
		status := vision.VisionStatusOpen
		_, err := store.Update(ctx, uuid.New(), vision.UpdateVisionParams{Status: &status})
		if !errors.Is(err, vision.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got: %v", err)
		}
	})
}

// TestStore_Promote tests the Promote happy path and not-found error.
func TestStore_Promote(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := vision.NewStore(pool, &wsID)
	ctx := context.Background()

	item, err := store.Add(ctx, vision.AddVisionParams{
		WorkspaceID: &wsID,
		Title:       "Promote test",
		WhyBlocked:  "Was blocked",
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
			t.Errorf("Status: got %q, want %q", promoted.Status, vision.VisionStatusPromoted)
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
