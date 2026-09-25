package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
)

func strp(s string) *string { return &s }

// TestWorkspaceStore_GitHubSlugPresence pins [F0925-31] on the SQLite repo
// store: github_slug is presence-aware (nil keeps the stored value, "" clears
// it, a value sets it), every read path (RepoByName, RepoByID, ActiveRepos)
// carries it, and a value that is not an owner/repo slug is rejected.
func TestWorkspaceStore_GitHubSlugPresence(t *testing.T) {
	t.Parallel()
	s := openWorkspaceStore(t, ":memory:", "")
	ctx := context.Background()
	const slug = "Wayne997035/wayneblacktea"

	repo, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wayneblacktea", GitHubSlug: strp(slug)})
	if err != nil {
		t.Fatalf("UpsertRepo with slug: %v", err)
	}
	if !repo.GithubSlug.Valid || repo.GithubSlug.String != slug {
		t.Fatalf("stored github_slug = %+v, want %q", repo.GithubSlug, slug)
	}

	repo, err = s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wayneblacktea"})
	if err != nil {
		t.Fatalf("UpsertRepo without slug: %v", err)
	}
	if repo.GithubSlug.String != slug {
		t.Errorf("omitted github_slug must keep %q, got %+v", slug, repo.GithubSlug)
	}
	byID, err := s.RepoByID(ctx, repo.ID)
	if err != nil || byID.GithubSlug.String != slug {
		t.Errorf("RepoByID github_slug = %+v (err %v), want %q", byID.GithubSlug, err, slug)
	}
	active, err := s.ActiveRepos(ctx)
	if err != nil || len(active) != 1 || active[0].GithubSlug.String != slug {
		t.Errorf("ActiveRepos github_slug = %+v (err %v), want %q", active, err, slug)
	}

	repo, err = s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wayneblacktea", GitHubSlug: strp("")})
	if err != nil {
		t.Fatalf("UpsertRepo clearing slug: %v", err)
	}
	if repo.GithubSlug.String != "" {
		t.Errorf("explicit empty github_slug must clear, got %+v", repo.GithubSlug)
	}

	for _, bad := range []string{"evil;x/y", "../x", "noslash", "a/b/c"} {
		_, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "wayneblacktea", GitHubSlug: strp(bad)})
		if !errors.Is(err, validator.ErrInvalidGitHubSlug) {
			t.Errorf("UpsertRepo github_slug %q: want ErrInvalidGitHubSlug, got %v", bad, err)
		}
	}
}
