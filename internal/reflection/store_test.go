//go:build integration

package reflection_test

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are migration files that cannot be run by pgx because they
// contain psql metacommands.
var skipMigrations = map[string]bool{
	"000011_backfill_workspace_id.up.sql": true,
}

var testPgPool *pgxpool.Pool

func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx,
		"pgvector/pgvector:pg16",
		tcpostgres.WithDatabase("wbt_test"),
		tcpostgres.WithUsername("wbt"),
		tcpostgres.WithPassword("wbt"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		log.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = c.Terminate(ctx) }()

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		log.Printf("get connection string: %v", err)
		return 1
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Printf("pgxpool.New: %v", err)
		return 1
	}
	defer pool.Close()

	applyAllUpMigrationsOnce(ctx, pool)

	testPgPool = pool
	return m.Run()
}

func applyAllUpMigrationsOnce(ctx context.Context, pool *pgxpool.Pool) {
	entries, err := migrationfs.FS.ReadDir(".")
	if err != nil {
		log.Fatalf("read embedded migrations dir: %v", err)
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
			log.Printf("applyAllUpMigrations: skipping %s (psql-metacommand-only file)", name)
			continue
		}
		body, err := migrationfs.FS.ReadFile(name)
		if err != nil {
			log.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			log.Fatalf("apply %s: %v", name, err)
		}
	}
}

func openTestPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Postgres integration test in -short mode (requires Docker)")
	}
	return testPgPool
}

// TestStore_Create verifies happy path and edge cases of Create.
func TestStore_Create(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := reflection.New(pool, &wsID)
	ctx := context.Background()

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
			name: "minimal required fields only",
			params: reflection.CreateParams{
				WorkspaceID: &wsID,
				Type:        "weekly",
				Summary:     "Weekly review",
			},
		},
		{
			name: "nil JSONB fields stored as NULL",
			params: reflection.CreateParams{
				WorkspaceID: &wsID,
				Type:        "system",
				Summary:     "System check",
				Confidence:  0.5,
			},
		},
		{
			name:    "invalid type returns error",
			params:  reflection.CreateParams{Type: "invalid_type"},
			wantErr: true,
		},
		{
			name: "confidence at boundary 1.0",
			params: reflection.CreateParams{
				WorkspaceID: &wsID,
				Type:        "decision",
				Summary:     "High confidence decision",
				Confidence:  1.0,
			},
		},
		{
			name:    "confidence above 1.0 returns error",
			params:  reflection.CreateParams{WorkspaceID: &wsID, Type: "daily", Confidence: 1.1},
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
			if r.CreatedAt.IsZero() {
				t.Error("CreatedAt should not be zero")
			}
		})
	}
}

// TestStore_List verifies List with type filter and empty results.
func TestStore_List(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := reflection.New(pool, &wsID)
	ctx := context.Background()

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
	})

	t.Run("no match returns empty slice", func(t *testing.T) {
		noMatchWsID := uuid.New()
		results, err := store.List(ctx, reflection.ListParams{WorkspaceID: &noMatchWsID})
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

// TestStore_GetLatest verifies GetLatest and ErrNotFound.
func TestStore_GetLatest(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := reflection.New(pool, &wsID)
	ctx := context.Background()

	t.Run("ErrNotFound when no reflections exist", func(t *testing.T) {
		_, err := store.GetLatest(ctx, &wsID, "daily")
		if err == nil {
			t.Error("expected ErrNotFound, got nil")
		}
	})

	// Seed one daily reflection.
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
		if err == nil {
			t.Error("expected ErrNotFound for type 'knowledge', got nil")
		}
	})
}

// TestStore_PruneOlderThan verifies that PruneOlderThan removes old reflections
// but leaves newer rows untouched.
func TestStore_PruneOlderThan(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := reflection.New(pool, &wsID)
	ctx := context.Background()

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
}

// TestStore_RecentWithPatterns verifies empty result when no patterns exist.
func TestStore_RecentWithPatterns(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := reflection.New(pool, &wsID)
	ctx := context.Background()

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

	// Seed one reflection with patterns_detected.
	_, err := store.Create(ctx, reflection.CreateParams{
		WorkspaceID:      &wsID,
		Type:             "weekly",
		Summary:          "weekly with pattern",
		PatternsDetected: json.RawMessage(`{"pattern":"recurring delay"}`),
		Confidence:       0.7,
	})
	if err != nil {
		t.Fatalf("seed weekly with patterns: %v", err)
	}

	t.Run("returns reflection with patterns", func(t *testing.T) {
		since := time.Now().Add(-1 * time.Hour)
		results, err := store.RecentWithPatterns(ctx, &wsID, since, 10)
		if err != nil {
			t.Fatalf("RecentWithPatterns after seed: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("expected 1 result, got %d", len(results))
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
}
