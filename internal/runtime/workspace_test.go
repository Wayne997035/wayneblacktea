package runtime_test

import (
	"errors"
	"testing"

	"github.com/waynechen/wayneblacktea/internal/runtime"
)

func TestWorkspaceIDFromEnv_Unset(t *testing.T) {
	t.Setenv("WORKSPACE_ID", "")
	got, err := runtime.WorkspaceIDFromEnv()
	if err != nil {
		t.Fatalf("WorkspaceIDFromEnv: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil workspace when env unset, got %v", got)
	}
}

func TestWorkspaceIDFromEnv_Valid(t *testing.T) {
	t.Setenv("WORKSPACE_ID", "550e8400-e29b-41d4-a716-446655440000")
	got, err := runtime.WorkspaceIDFromEnv()
	if err != nil {
		t.Fatalf("WorkspaceIDFromEnv: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil workspace id")
	}
	if got.String() != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("got %s, want 550e8400-...", got.String())
	}
}

func TestWorkspaceIDFromEnv_Invalid(t *testing.T) {
	t.Setenv("WORKSPACE_ID", "not-a-uuid")
	_, err := runtime.WorkspaceIDFromEnv()
	if !errors.Is(err, runtime.ErrInvalidWorkspaceID) {
		t.Errorf("expected ErrInvalidWorkspaceID, got %v", err)
	}
}

func TestWorkspaceIDFromEnv_TrimsWhitespace(t *testing.T) {
	t.Setenv("WORKSPACE_ID", "  550e8400-e29b-41d4-a716-446655440000  ")
	got, err := runtime.WorkspaceIDFromEnv()
	if err != nil {
		t.Fatalf("WorkspaceIDFromEnv: %v", err)
	}
	if got == nil || got.String() != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("expected trimmed UUID, got %v", got)
	}
}

func TestUserIDFromEnv(t *testing.T) {
	t.Setenv("USER_ID", "  wayne  ")
	if got := runtime.UserIDFromEnv(); got != "wayne" {
		t.Errorf("expected trimmed 'wayne', got %q", got)
	}
	t.Setenv("USER_ID", "")
	if got := runtime.UserIDFromEnv(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
