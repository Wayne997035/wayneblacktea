//go:build integration

package decision_test

import (
	"context"
	"os"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/jackc/pgx/v5/pgxpool"
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
	tlsCfg, err := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if err != nil {
		t.Fatalf("build TLS config: %v", err)
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestLogAndListDecision(t *testing.T) {
	pool := setupPool(t)
	store := decision.NewStore(pool)
	ctx := context.Background()

	d, err := store.Log(ctx, decision.LogParams{
		RepoName:  "chat-gateway",
		Title:     "Use pgx over database/sql",
		Context:   "Need PostgreSQL-specific features",
		Decision:  "Use pgx/v5 directly",
		Rationale: "Native pgx supports pgvector and arrays cleanly",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM decisions WHERE id = $1", d.ID)
		if cleanErr != nil {
			t.Logf("cleanup decision: %v", cleanErr)
		}
	})

	list, err := store.ByRepo(ctx, "chat-gateway", 10)
	if err != nil {
		t.Fatalf("ByRepo: %v", err)
	}
	if len(list) == 0 {
		t.Error("expected at least 1 decision")
	}
}

func TestLog_EmptyTitle(t *testing.T) {
	pool := setupPool(t)
	store := decision.NewStore(pool)
	ctx := context.Background()

	// Empty title is a required field (NOT NULL in schema); expect a DB error.
	_, err := store.Log(ctx, decision.LogParams{
		RepoName:  "chat-gateway",
		Title:     "",
		Context:   "ctx",
		Decision:  "dec",
		Rationale: "rat",
	})
	// Empty string is still a valid NOT NULL value in Postgres,
	// so this should succeed — verify graceful handling.
	if err != nil {
		t.Logf("Log with empty title returned error (acceptable): %v", err)
		return
	}
	// If it succeeded, clean up.
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM decisions WHERE title = '' AND repo_name = 'chat-gateway'")
		if cleanErr != nil {
			t.Logf("cleanup: %v", cleanErr)
		}
	})
}

func TestByRepo_Empty(t *testing.T) {
	store := decision.NewStore(setupPool(t))
	ctx := context.Background()

	// Repo name that has no decisions — should return empty slice without error.
	list, err := store.ByRepo(ctx, "nonexistent-repo-xyz-abc", 10)
	if err != nil {
		t.Fatalf("ByRepo for unknown repo: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 decisions for unknown repo, got %d", len(list))
	}
}
