package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

func TestWorkspaceStore_ModelPreference_DefaultThenUpsert(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", uuid.New().String())
	ctx := context.Background()

	got, err := s.GetModelPreference(ctx)
	if err != nil {
		t.Fatalf("GetModelPreference (no row): %v", err)
	}
	if got != workspace.DefaultModelPreference {
		t.Errorf("default = %q, want %q", got, workspace.DefaultModelPreference)
	}

	if err := s.UpsertModelPreference(ctx, "claude-haiku-4-5"); err != nil {
		t.Fatalf("UpsertModelPreference: %v", err)
	}
	got, err = s.GetModelPreference(ctx)
	if err != nil {
		t.Fatalf("GetModelPreference after upsert: %v", err)
	}
	if got != "claude-haiku-4-5" {
		t.Errorf("after upsert = %q, want claude-haiku-4-5", got)
	}

	// ON CONFLICT update path.
	if err := s.UpsertModelPreference(ctx, "claude-opus-4-8"); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if got, _ = s.GetModelPreference(ctx); got != "claude-opus-4-8" {
		t.Errorf("after 2nd upsert = %q, want claude-opus-4-8", got)
	}
}

func TestWorkspaceStore_ModelPreference_InvalidRejected(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", uuid.New().String())
	if err := s.UpsertModelPreference(context.Background(), "gpt-4"); !errors.Is(err, workspace.ErrInvalidModel) {
		t.Fatalf("expected ErrInvalidModel, got %v", err)
	}
}

func TestWorkspaceStore_ModelPreference_LegacyUnscopedReturnsDefault(t *testing.T) {
	// Empty workspaceID = legacy unscoped mode: Get returns the default and
	// Upsert refuses (no workspace_id to key on).
	s := openWorkspaceStore(t, ":memory:", "")
	got, err := s.GetModelPreference(context.Background())
	if err != nil {
		t.Fatalf("GetModelPreference (legacy): %v", err)
	}
	if got != workspace.DefaultModelPreference {
		t.Errorf("legacy default = %q, want %q", got, workspace.DefaultModelPreference)
	}
	if err := s.UpsertModelPreference(context.Background(), "claude-haiku-4-5"); err == nil {
		t.Errorf("expected error upserting in legacy unscoped mode")
	}
}
