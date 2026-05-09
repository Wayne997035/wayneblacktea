//go:build integration

package procedural_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/procedural"
	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// skipMigrations are migration files that cannot be run by pgx because they
// contain psql metacommands.
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

// TestStore_Add verifies happy path and edge cases of Add.
func TestStore_Add(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := procedural.New(pool, &wsID)
	ctx := context.Background()

	tests := []struct {
		name    string
		params  procedural.AddParams
		wantErr bool
	}{
		{
			name: "happy path with all fields",
			params: procedural.AddParams{
				WorkspaceID:  &wsID,
				RepoName:     "wayneblacktea",
				Title:        "How to run migrations",
				WhenToUse:    "Before any schema change or deploy",
				ApproachMD:   "cd build && task migrate-up",
				ToolsUsed:    []string{"Bash", "Read"},
				FilesTouched: []string{"migrations/000001_init.up.sql"},
			},
		},
		{
			name: "minimal required fields only",
			params: procedural.AddParams{
				WorkspaceID: &wsID,
				Title:       "Minimal procedural memory",
				WhenToUse:   "",
				ApproachMD:  "",
			},
		},
		{
			name: "nil tools and files become empty slices",
			params: procedural.AddParams{
				WorkspaceID:  &wsID,
				Title:        "Empty slices",
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
					t.Errorf("expected error, got nil")
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
			if mem.SuccessCount != 0 {
				t.Errorf("SuccessCount: got %d, want 0", mem.SuccessCount)
			}
		})
	}
}

// TestStore_Query verifies keyword filter and repo filter.
func TestStore_Query(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := procedural.New(pool, &wsID)
	ctx := context.Background()

	// Seed two items.
	_, err := store.Add(ctx, procedural.AddParams{
		WorkspaceID: &wsID,
		RepoName:    "wayneblacktea",
		Title:       "How to run migrations",
		WhenToUse:   "Before any deploy",
		ApproachMD:  "cd build && task migrate-up",
	})
	if err != nil {
		t.Fatalf("seed item 1: %v", err)
	}

	_, err = store.Add(ctx, procedural.AddParams{
		WorkspaceID: &wsID,
		RepoName:    "other-repo",
		Title:       "How to lint code",
		WhenToUse:   "Before submitting a PR",
		ApproachMD:  "golangci-lint run ./...",
	})
	if err != nil {
		t.Fatalf("seed item 2: %v", err)
	}

	t.Run("keyword matches title", func(t *testing.T) {
		results, err := store.Query(ctx, procedural.QueryFilter{
			WorkspaceID: &wsID,
			Keywords:    "migrations",
		})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(results) != 1 {
			t.Errorf("expected 1 result, got %d", len(results))
		}
		if len(results) > 0 && results[0].Title != "How to run migrations" {
			t.Errorf("unexpected title: %q", results[0].Title)
		}
	})

	t.Run("keyword matches when_to_use", func(t *testing.T) {
		results, err := store.Query(ctx, procedural.QueryFilter{
			WorkspaceID: &wsID,
			Keywords:    "deploy",
		})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 result for 'deploy'")
		}
	})

	t.Run("repo filter narrows results", func(t *testing.T) {
		results, err := store.Query(ctx, procedural.QueryFilter{
			WorkspaceID: &wsID,
			RepoName:    "other-repo",
		})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		for _, r := range results {
			if r.RepoName != "other-repo" {
				t.Errorf("unexpected repo_name: %q", r.RepoName)
			}
		}
		if len(results) != 1 {
			t.Errorf("expected 1 result for other-repo, got %d", len(results))
		}
	})

	t.Run("no keyword returns all in workspace", func(t *testing.T) {
		results, err := store.Query(ctx, procedural.QueryFilter{
			WorkspaceID: &wsID,
		})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(results) < 2 {
			t.Errorf("expected at least 2 results, got %d", len(results))
		}
	})
}

// TestStore_MarkUsed verifies counter increment and not-found behaviour.
func TestStore_MarkUsed(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := procedural.New(pool, &wsID)
	ctx := context.Background()

	mem, err := store.Add(ctx, procedural.AddParams{
		WorkspaceID: &wsID,
		Title:       "MarkUsed test",
	})
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
