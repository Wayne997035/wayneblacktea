package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/handler"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type slugRecordingWorkspaceStore struct {
	fakeWorkspaceStore
	got workspace.UpsertRepoParams
}

func (s *slugRecordingWorkspaceStore) UpsertRepo(_ context.Context, p workspace.UpsertRepoParams) (*db.Repo, error) {
	s.got = p
	if s.err != nil {
		return nil, s.err
	}
	return &db.Repo{ID: uuid.New(), Name: p.Name}, nil
}

// TestUpsertRepo_GitHubSlug pins [F0925-31] on POST /api/workspace/repos:
// github_slug is forwarded with presence semantics, an invalid slug is a 400
// that never reaches the store, and the store's ErrInvalidGitHubSlug is 400.
func TestUpsertRepo_GitHubSlug(t *testing.T) {
	t.Parallel()
	post := func(store *slugRecordingWorkspaceStore, body string) int {
		e := newEcho()
		e.POST("/api/workspace/repos", handler.NewWorkspaceHandler(store).UpsertRepo)
		return performRequest(e, http.MethodPost, "/api/workspace/repos", body).Code
	}

	s := &slugRecordingWorkspaceStore{}
	if code := post(s, `{"name":"wayneblacktea","github_slug":"Wayne997035/wayneblacktea"}`); code != http.StatusOK ||
		s.got.GitHubSlug == nil || *s.got.GitHubSlug != "Wayne997035/wayneblacktea" {
		t.Errorf("valid slug: status %d, forwarded %v", code, s.got.GitHubSlug)
	}
	s = &slugRecordingWorkspaceStore{}
	if post(s, `{"name":"wayneblacktea"}`); s.got.GitHubSlug != nil {
		t.Errorf("absent github_slug must stay nil")
	}
	s = &slugRecordingWorkspaceStore{}
	if code := post(s, `{"name":"wayneblacktea","github_slug":"../x"}`); code != http.StatusBadRequest || s.got.Name != "" {
		t.Errorf("invalid slug: status %d, store reached=%v", code, s.got.Name != "")
	}
	s = &slugRecordingWorkspaceStore{}
	s.err = fmt.Errorf("upserting repo: %w", validator.ErrInvalidGitHubSlug)
	if code := post(s, `{"name":"wayneblacktea"}`); code != http.StatusBadRequest {
		t.Errorf("store ErrInvalidGitHubSlug: status %d, want 400", code)
	}
}

// TestRepoOverview_CarriesGitHubSlug pins that the overview's hand-built
// repo summary carries github_slug when set and omits it when not.
func TestRepoOverview_CarriesGitHubSlug(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		slug pgtype.Text
		want string
	}{{pgtype.Text{String: "Wayne997035/wayneblacktea", Valid: true}, `"github_slug":"Wayne997035/wayneblacktea"`}, {pgtype.Text{}, ""}} {
		repo := &db.Repo{ID: uuid.New(), Name: "wayneblacktea", Status: "active", GithubSlug: tc.slug}
		h := handler.NewWorkspaceOverviewHandler(&fakeOverviewWorkspaceStore{repo: repo},
			&fakeOverviewGTDStore{projectErr: gtd.ErrNotFound},
			&fakeOverviewDecisionStore{}, &fakeOverviewSessionStore{})
		e := newEcho()
		e.GET("/api/workspace/repos/:id/overview", h.GetRepoOverview)
		rec := performRequest(e, http.MethodGet, "/api/workspace/repos/"+repo.ID.String()+"/overview", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Repo json.RawMessage `json:"repo"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		got := string(body.Repo)
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("repo summary %s missing %s", got, tc.want)
		}
		if tc.want == "" && strings.Contains(got, "github_slug") {
			t.Errorf("unset github_slug must be omitted, got %s", got)
		}
	}
}
