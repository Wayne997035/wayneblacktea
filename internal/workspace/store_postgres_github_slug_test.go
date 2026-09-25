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

// TestUpsertRepo_GitHubSlug_Postgres is the Postgres twin of the SQLite
// TestWorkspaceStore_GitHubSlugPresence ([F0925-31]). RepoByID is a
// hand-written query on this backend, so it is checked separately from the
// sqlc-generated reads.
func TestUpsertRepo_GitHubSlug_Postgres(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	wsID := uuid.New()
	store := workspace.NewStore(pool, &wsID)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM repos WHERE workspace_id = $1`, wsID); err != nil {
			t.Logf("cleanup repos: %v", err)
		}
	})
	const slug = "Wayne997035/wayneblacktea"

	repo, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wayneblacktea", GitHubSlug: strPtr(slug)})
	if err != nil {
		t.Fatalf("UpsertRepo with slug: %v", err)
	}
	if repo.GithubSlug.String != slug {
		t.Fatalf("stored github_slug = %+v, want %q", repo.GithubSlug, slug)
	}
	repo, err = store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wayneblacktea"})
	if err != nil || repo.GithubSlug.String != slug {
		t.Errorf("omitted github_slug must keep %q, got %+v (err %v)", slug, repo.GithubSlug, err)
	}
	byID, err := store.RepoByID(ctx, repo.ID)
	if err != nil || byID.GithubSlug.String != slug {
		t.Errorf("RepoByID github_slug = %+v (err %v), want %q", byID.GithubSlug, err, slug)
	}
	byName, err := store.RepoByName(ctx, "wayneblacktea")
	if err != nil || byName.GithubSlug.String != slug {
		t.Errorf("RepoByName github_slug = %+v (err %v), want %q", byName.GithubSlug, err, slug)
	}
	repo, err = store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wayneblacktea", GitHubSlug: strPtr("")})
	if err != nil || repo.GithubSlug.String != "" {
		t.Errorf("explicit empty github_slug must clear, got %+v (err %v)", repo.GithubSlug, err)
	}
	for _, bad := range []string{"evil;x/y", "../x", "noslash"} {
		if _, err := store.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wayneblacktea", GitHubSlug: strPtr(bad)}); !errors.Is(err, validator.ErrInvalidGitHubSlug) {
			t.Errorf("UpsertRepo github_slug %q: want ErrInvalidGitHubSlug, got %v", bad, err)
		}
	}
}
