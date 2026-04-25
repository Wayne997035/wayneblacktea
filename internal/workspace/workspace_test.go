//go:build integration

package workspace_test

import (
	"context"
	"crypto/tls"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/waynechen/wayneblacktea/internal/workspace"
)

func setupPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse DATABASE_URL: %v", err)
	}
	if cfg.ConnConfig.TLSConfig != nil {
		cfg.ConnConfig.TLSConfig = &tls.Config{ //nolint:gosec // test-only: skip CA verify for Aiven custom CA
			InsecureSkipVerify: true,
		}
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestUpsertAndGetRepo(t *testing.T) {
	pool := setupPool(t)
	store := workspace.NewStore(pool)
	ctx := context.Background()

	repo, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:     "test-repo-" + t.Name(),
		Language: "go",
	})
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM repos WHERE id = $1", repo.ID)
		if cleanErr != nil {
			t.Logf("cleanup repo: %v", cleanErr)
		}
	})

	got, err := store.RepoByName(ctx, repo.Name)
	if err != nil {
		t.Fatalf("RepoByName: %v", err)
	}
	if got.Name != repo.Name {
		t.Errorf("expected name=%s, got %s", repo.Name, got.Name)
	}
}

func TestRepoByName_NotFound(t *testing.T) {
	store := workspace.NewStore(setupPool(t))
	ctx := context.Background()

	_, err := store.RepoByName(ctx, "nonexistent-repo-xyz")
	if err == nil {
		t.Fatal("expected ErrNotFound, got nil")
	}
}

func TestUpsertRepo_UpdateExisting(t *testing.T) {
	pool := setupPool(t)
	store := workspace.NewStore(pool)
	ctx := context.Background()

	name := "test-upsert-update-" + t.Name()
	first, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:     name,
		Language: "go",
	})
	if err != nil {
		t.Fatalf("UpsertRepo first: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM repos WHERE id = $1", first.ID)
		if cleanErr != nil {
			t.Logf("cleanup repo: %v", cleanErr)
		}
	})

	second, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:          name,
		Language:      "go",
		CurrentBranch: "main",
		Description:   "updated description",
	})
	if err != nil {
		t.Fatalf("UpsertRepo second: %v", err)
	}

	if second.ID != first.ID {
		t.Errorf("upsert should return same ID: first=%s second=%s", first.ID, second.ID)
	}
	if !second.Description.Valid || second.Description.String != "updated description" {
		t.Errorf("expected updated description, got %+v", second.Description)
	}
}

func TestActiveRepos(t *testing.T) {
	store := workspace.NewStore(setupPool(t))
	ctx := context.Background()

	repos, err := store.ActiveRepos(ctx)
	if err != nil {
		t.Fatalf("ActiveRepos: %v", err)
	}
	// Validate all returned repos have status=active (DB constraint guarantees it,
	// but verify the query filter is working correctly).
	for _, r := range repos {
		if r.Status != "active" {
			t.Errorf("expected status=active, got %s for repo %s", r.Status, r.Name)
		}
	}
}
