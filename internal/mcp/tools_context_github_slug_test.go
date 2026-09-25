package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	mcpmsg "github.com/mark3labs/mcp-go/mcp"
)

// slugCapturingWorkspaceStore records the last UpsertRepoParams and serves
// a fixed ActiveRepos list.
type slugCapturingWorkspaceStore struct {
	countingWorkspaceStore
	got    workspace.UpsertRepoParams
	active []db.Repo
}

func (s *slugCapturingWorkspaceStore) UpsertRepo(_ context.Context, p workspace.UpsertRepoParams) (*db.Repo, error) {
	s.got = p
	return &db.Repo{Name: p.Name}, nil
}

func (s *slugCapturingWorkspaceStore) ActiveRepos(context.Context) ([]db.Repo, error) {
	return s.active, nil
}

// TestSyncRepo_GitHubSlugArg pins [F0925-31]: sync_repo forwards github_slug
// with presence semantics (absent → nil, "" → explicit clear) and rejects a
// value that is not an owner/repo slug before the store.
func TestSyncRepo_GitHubSlugArg(t *testing.T) {
	t.Parallel()
	call := func(args map[string]any) (*slugCapturingWorkspaceStore, *mcpmsg.CallToolResult) {
		store := &slugCapturingWorkspaceStore{}
		req := mcpmsg.CallToolRequest{}
		req.Params.Arguments = args
		res, err := (&Server{workspace: store}).handleSyncRepo(context.Background(), req)
		if err != nil {
			t.Fatalf("handleSyncRepo: %v", err)
		}
		return store, res
	}

	store, res := call(map[string]any{"name": "wayneblacktea", "github_slug": "Wayne997035/wayneblacktea"})
	if res.IsError || store.got.GitHubSlug == nil || *store.got.GitHubSlug != "Wayne997035/wayneblacktea" {
		t.Errorf("slug not forwarded: %+v (%s)", store.got.GitHubSlug, resultText(res))
	}
	store, _ = call(map[string]any{"name": "wayneblacktea"})
	if store.got.GitHubSlug != nil {
		t.Errorf("absent github_slug must stay nil, got %q", *store.got.GitHubSlug)
	}
	store, _ = call(map[string]any{"name": "wayneblacktea", "github_slug": ""})
	if store.got.GitHubSlug == nil || *store.got.GitHubSlug != "" {
		t.Errorf("empty github_slug must be an explicit clear, got %v", store.got.GitHubSlug)
	}
	for _, bad := range []string{"../x", "noslash", "a/b;c"} {
		store, res = call(map[string]any{"name": "wayneblacktea", "github_slug": bad})
		if !res.IsError || !strings.Contains(resultText(res), "github_slug") || store.got.Name != "" {
			t.Errorf("github_slug %q: want rejection before the store, got %s", bad, resultText(res))
		}
	}
}

// TestListActiveRepos_CarriesGitHubSlug pins that list_active_repos'
// hand-built projection carries github_slug, clipped like the other short
// fields because RepoSlugRe has no length cap.
func TestListActiveRepos_CarriesGitHubSlug(t *testing.T) {
	t.Parallel()
	long := "o/" + strings.Repeat("r", 200)
	store := &slugCapturingWorkspaceStore{active: []db.Repo{
		{ID: uuid.New(), Name: "a", GithubSlug: pgtype.Text{String: "Wayne997035/a", Valid: true}},
		{ID: uuid.New(), Name: "b", GithubSlug: pgtype.Text{String: long, Valid: true}},
	}}
	res, err := (&Server{workspace: store}).handleListActiveRepos(context.Background(), mcpmsg.CallToolRequest{})
	if err != nil || res.IsError {
		t.Fatalf("handleListActiveRepos: %v %s", err, resultText(res))
	}
	var body struct {
		Repos []struct {
			GithubSlug          pgtype.Text `json:"github_slug"`
			GithubSlugTruncated bool        `json:"github_slug_truncated"`
		} `json:"repos"`
	}
	if err := json.Unmarshal([]byte(resultText(res)), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Repos) != 2 || body.Repos[0].GithubSlug.String != "Wayne997035/a" || body.Repos[0].GithubSlugTruncated {
		t.Errorf("repo a github_slug = %+v", body.Repos)
	}
	if !body.Repos[1].GithubSlugTruncated || len(body.Repos[1].GithubSlug.String) >= len(long) {
		t.Errorf("long github_slug must be clipped and flagged, got %+v", body.Repos[1])
	}
}
