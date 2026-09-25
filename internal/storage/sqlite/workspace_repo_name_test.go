package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
)

// TestWorkspaceStore_UpsertRepo_RejectsInvalidName is the store-layer
// backstop for repos.name. cmd/seed writes through the store directly,
// bypassing the HTTP and MCP checks, so the store must enforce the rule too.
func TestWorkspaceStore_UpsertRepo_RejectsInvalidName(t *testing.T) {
	t.Parallel() // [F0925-29]
	s := openWorkspaceStore(t, ":memory:", "")
	ctx := context.Background()

	for _, name := range []string{"../x", "-x/y", "a//b", "a b", ".claude"} {
		_, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: name})
		if !errors.Is(err, validator.ErrInvalidRepoName) {
			t.Errorf("UpsertRepo(%q): want ErrInvalidRepoName, got %v", name, err)
		}
	}
}

// TestWorkspaceStore_UpsertRepo_AcceptsRepoPaths pins that the backstop does
// not reject the names production stores.
func TestWorkspaceStore_UpsertRepo_AcceptsRepoPaths(t *testing.T) {
	t.Parallel() // [F0925-29]
	s := openWorkspaceStore(t, ":memory:", "")
	ctx := context.Background()

	for _, name := range []string{"wayneblacktea", "Flare-Go/auth", "kdc-p2p-report/kdc-p2p-server-status-check"} {
		repo, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: name})
		if err != nil {
			t.Errorf("UpsertRepo(%q): %v", name, err)
			continue
		}
		if repo.Name != name {
			t.Errorf("UpsertRepo(%q) stored name %q", name, repo.Name)
		}
	}
}
