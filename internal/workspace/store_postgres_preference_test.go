//go:build integration

package workspace_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

func TestModelPreference_DefaultThenUpsert_PG(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := workspace.NewStore(pool, &wsID)

	got, err := store.GetModelPreference(ctx)
	if err != nil {
		t.Fatalf("GetModelPreference (no row): %v", err)
	}
	if got != workspace.DefaultModelPreference {
		t.Errorf("default = %q, want %q", got, workspace.DefaultModelPreference)
	}

	if err := store.UpsertModelPreference(ctx, "claude-haiku-4-5"); err != nil {
		t.Fatalf("UpsertModelPreference: %v", err)
	}
	if got, _ = store.GetModelPreference(ctx); got != "claude-haiku-4-5" {
		t.Errorf("after upsert = %q, want claude-haiku-4-5", got)
	}

	// ON CONFLICT update path.
	if err := store.UpsertModelPreference(ctx, "claude-opus-4-8"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if got, _ = store.GetModelPreference(ctx); got != "claude-opus-4-8" {
		t.Errorf("after 2nd upsert = %q, want claude-opus-4-8", got)
	}
}

func TestModelPreference_InvalidRejected_PG(t *testing.T) {
	pool := openTestPgPool(t)
	store := workspace.NewStore(pool, ptrUUID(uuid.New()))
	if err := store.UpsertModelPreference(context.Background(), "gpt-4"); !errors.Is(err, workspace.ErrInvalidModel) {
		t.Fatalf("expected ErrInvalidModel, got %v", err)
	}
}

func TestModelPreference_WorkspaceIsolation_PG(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsA, wsB := uuid.New(), uuid.New()
	storeA := workspace.NewStore(pool, &wsA)
	storeB := workspace.NewStore(pool, &wsB)

	if err := storeA.UpsertModelPreference(ctx, "claude-opus-4-8"); err != nil {
		t.Fatalf("A upsert: %v", err)
	}
	// Workspace B has no row → must see the default, never A's value.
	if got, _ := storeB.GetModelPreference(ctx); got != workspace.DefaultModelPreference {
		t.Errorf("workspace B leaked workspace A's preference: %q", got)
	}
}

func ptrUUID(u uuid.UUID) *uuid.UUID { return &u }
