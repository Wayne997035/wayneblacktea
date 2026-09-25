//go:build integration

package workspace_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

// TestUpsertRepo_RepoNameRule_Postgres pins [F0925-29] on the Postgres repo
// store: names breaking the workspace repo name rule are rejected with
// ErrInvalidRepoName before any write, and the path-shaped names production
// stores are accepted and stored verbatim.
func TestUpsertRepo_RepoNameRule_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := workspace.NewStore(pool, &wsID)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM repos WHERE workspace_id = $1`, wsID); err != nil {
			t.Logf("cleanup repos: %v", err)
		}
	})

	for _, name := range []string{"../x", "-x/y", "a//b", "a b", ".claude"} {
		if _, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: name}); !errors.Is(err, validator.ErrInvalidRepoName) {
			t.Errorf("UpsertRepo(%q): want ErrInvalidRepoName, got %v", name, err)
		}
	}
	for _, name := range []string{"wayneblacktea", "Flare-Go/auth"} {
		repo, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: name})
		if err != nil {
			t.Errorf("UpsertRepo(%q): %v", name, err)
			continue
		}
		if repo.Name != name {
			t.Errorf("UpsertRepo(%q) stored %q", name, repo.Name)
		}
	}
}
