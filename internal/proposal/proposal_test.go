//go:build integration

package proposal_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/waynechen/wayneblacktea/internal/proposal"
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

func TestProposal_CreateGetResolve(t *testing.T) {
	pool := setupPool(t)
	store := proposal.NewStore(pool)
	ctx := context.Background()

	payload, err := json.Marshal(map[string]any{
		"title":   "Auto-extract decisions to memory",
		"area":    "engineering",
		"summary": "Discord bot proposes weekly knowledge concept cards",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	created, err := store.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeGoal,
		Payload:    payload,
		ProposedBy: "claude-code",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, cleanErr := pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", created.ID)
		if cleanErr != nil {
			t.Logf("cleanup proposal: %v", cleanErr)
		}
	})

	if created.Status != string(proposal.StatusPending) {
		t.Errorf("expected status=pending, got %s", created.Status)
	}
	if created.Type != string(proposal.TypeGoal) {
		t.Errorf("expected type=goal, got %s", created.Type)
	}

	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get returned wrong row: want %s, got %s", created.ID, got.ID)
	}

	resolved, err := store.Resolve(ctx, created.ID, proposal.StatusAccepted)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Status != string(proposal.StatusAccepted) {
		t.Errorf("expected status=accepted, got %s", resolved.Status)
	}
	if !resolved.ResolvedAt.Valid {
		t.Errorf("expected resolved_at to be set")
	}

	// Re-resolving a non-pending proposal must return ErrNotFound (idempotent).
	if _, err := store.Resolve(ctx, created.ID, proposal.StatusRejected); !errors.Is(err, proposal.ErrNotFound) {
		t.Errorf("expected ErrNotFound on re-resolve, got %v", err)
	}
}

func TestProposal_Get_NotFound(t *testing.T) {
	store := proposal.NewStore(setupPool(t))
	ctx := context.Background()

	_, err := store.Get(ctx, uuid.UUID{})
	if !errors.Is(err, proposal.ErrNotFound) {
		t.Errorf("expected ErrNotFound for zero UUID, got %v", err)
	}
}

func TestProposal_Resolve_InvalidStatus(t *testing.T) {
	store := proposal.NewStore(setupPool(t))
	ctx := context.Background()

	// Status must be accepted/rejected; pending is not a resolution.
	_, err := store.Resolve(ctx, uuid.UUID{}, proposal.StatusPending)
	if err == nil {
		t.Fatal("expected error for invalid resolve status, got nil")
	}
}

func TestProposal_ListPending(t *testing.T) {
	pool := setupPool(t)
	store := proposal.NewStore(pool)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"title": "list-test"})
	created, err := store.Create(ctx, proposal.CreateParams{
		Type:       proposal.TypeTask,
		Payload:    payload,
		ProposedBy: "test",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM pending_proposals WHERE id = $1", created.ID)
	})

	rows, err := store.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListPending did not include the just-created proposal %s", created.ID)
	}
}
