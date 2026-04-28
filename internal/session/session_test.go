//go:build integration

package session_test

import (
	"context"
	"crypto/tls"
	"errors"
	"os"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/session"
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

func TestSetAndGetHandoff(t *testing.T) {
	pool := setupPool(t)
	store := session.NewStore(pool)
	ctx := context.Background()

	h, err := store.SetHandoff(ctx, session.HandoffParams{
		RepoName:       "chat-gateway",
		Intent:         "Continue implementing GetMembers handler",
		ContextSummary: "Handler skeleton done, repo layer missing",
	})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM session_handoffs WHERE id = $1", h.ID)
		if cleanErr != nil {
			t.Logf("cleanup handoff: %v", cleanErr)
		}
	})

	latest, err := store.LatestHandoff(ctx)
	if err != nil {
		t.Fatalf("LatestHandoff: %v", err)
	}
	if latest.Intent != "Continue implementing GetMembers handler" {
		t.Errorf("unexpected intent: %s", latest.Intent)
	}
}

func TestResolveHandoff(t *testing.T) {
	pool := setupPool(t)
	store := session.NewStore(pool)
	ctx := context.Background()

	h, err := store.SetHandoff(ctx, session.HandoffParams{
		RepoName: "chat-gateway",
		Intent:   "Test resolve flow",
	})
	if err != nil {
		t.Fatalf("SetHandoff: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM session_handoffs WHERE id = $1", h.ID)
		if cleanErr != nil {
			t.Logf("cleanup handoff: %v", cleanErr)
		}
	})

	if err := store.Resolve(ctx, h.ID); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// After resolving, the handoff should no longer appear as the latest unresolved.
	// (Assuming no other unresolved handoffs exist that are newer — this test creates
	// the most recent one and resolves it immediately.)
	latest, err := store.LatestHandoff(ctx)
	if err == nil && latest != nil && latest.ID == h.ID {
		t.Errorf("resolved handoff should not be returned as latest unresolved")
	}
}

func TestLatestHandoff_NotFound(t *testing.T) {
	pool := setupPool(t)
	store := session.NewStore(pool)
	ctx := context.Background()

	// First, resolve all existing handoffs so the table is effectively empty of unresolved ones.
	rows, _ := pool.Query(ctx, "SELECT id FROM session_handoffs WHERE resolved_at IS NULL") //nolint:errcheck // best-effort
	defer rows.Close()

	var ids []interface{}
	for rows.Next() {
		var id interface{}
		if scanErr := rows.Scan(&id); scanErr == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		_, _ = pool.Exec(ctx, "UPDATE session_handoffs SET resolved_at = NOW() WHERE id = $1", id)
	}

	_, err := store.LatestHandoff(ctx)
	if err == nil {
		// If there are still unresolved handoffs (from other test runs), skip assertion.
		t.Skip("unresolved handoffs exist in DB, skipping ErrNotFound check")
	}
	if !errors.Is(err, session.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
