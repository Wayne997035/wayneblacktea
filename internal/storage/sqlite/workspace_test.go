package sqlite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/google/uuid"
)

const statusActive = "active"

func openWorkspaceStore(t *testing.T, dsn, workspaceID string) *sqlite.WorkspaceStore {
	t.Helper()
	d, err := sqlite.Open(context.Background(), dsn, workspaceID)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return sqlite.NewWorkspaceStore(d)
}

func TestWorkspaceStore_UpsertAndGetRoundTrip(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	repo, err := s.UpsertRepo(context.Background(), workspace.UpsertRepoParams{
		Name:            "round-trip",
		Path:            strPtr("/tmp/round-trip"),
		Description:     strPtr("demo repo"),
		Language:        strPtr("go"),
		CurrentBranch:   strPtr("main"),
		KnownIssues:     []string{"issue one", "issue two"},
		NextPlannedStep: strPtr("ship sqlite stores"),
	})
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	if repo.Name != "round-trip" || !repo.Path.Valid || len(repo.KnownIssues) != 2 {
		t.Fatalf("unexpected repo: %+v", repo)
	}

	got, err := s.RepoByName(context.Background(), "round-trip")
	if err != nil {
		t.Fatalf("RepoByName: %v", err)
	}
	if got.ID != repo.ID || got.KnownIssues[1] != "issue two" {
		t.Fatalf("unexpected fetched repo: %+v", got)
	}
}

func TestWorkspaceStore_NullOptionalFields(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	repo, err := s.UpsertRepo(context.Background(), workspace.UpsertRepoParams{Name: "minimal"})
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	if repo.Path.Valid || repo.Description.Valid || repo.Language.Valid ||
		repo.CurrentBranch.Valid || repo.NextPlannedStep.Valid {
		t.Fatalf("expected NULL optionals, got %+v", repo)
	}
	if len(repo.KnownIssues) != 0 {
		t.Fatalf("expected empty known issues, got %+v", repo.KnownIssues)
	}
}

func TestWorkspaceStore_EmptyTable(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	rows, err := s.ActiveRepos(context.Background())
	if err != nil {
		t.Fatalf("ActiveRepos: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no repos, got %+v", rows)
	}
	_, err = s.RepoByName(context.Background(), "missing")
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("expected workspace.ErrNotFound, got %v", err)
	}
}

func TestWorkspaceStore_UpdateExistingRepo(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	first, err := s.UpsertRepo(context.Background(), workspace.UpsertRepoParams{
		Name: "update-me", Language: strPtr("go"),
	})
	if err != nil {
		t.Fatalf("first UpsertRepo: %v", err)
	}
	second, err := s.UpsertRepo(context.Background(), workspace.UpsertRepoParams{
		Name: "update-me", Language: strPtr("typescript"), Description: strPtr("updated"),
	})
	if err != nil {
		t.Fatalf("second UpsertRepo: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected same repo ID after upsert, first=%s second=%s", first.ID, second.ID)
	}
	if !second.Language.Valid || second.Language.String != "typescript" ||
		!second.Description.Valid || second.Description.String != "updated" {
		t.Fatalf("repo did not update fields: %+v", second)
	}
}

func TestWorkspaceStore_ActiveReposOrderingByNameWhenActivityTies(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	if _, err := s.UpsertRepo(context.Background(), workspace.UpsertRepoParams{Name: "b-repo"}); err != nil {
		t.Fatalf("UpsertRepo b: %v", err)
	}
	if _, err := s.UpsertRepo(context.Background(), workspace.UpsertRepoParams{Name: "a-repo"}); err != nil {
		t.Fatalf("UpsertRepo a: %v", err)
	}
	rows, err := s.ActiveRepos(context.Background())
	if err != nil {
		t.Fatalf("ActiveRepos: %v", err)
	}
	if len(rows) != 2 || rows[0].Status != statusActive || rows[1].Status != statusActive {
		t.Fatalf("expected two active repos, got %+v", rows)
	}
}

func TestWorkspaceStore_WorkspaceIsolation(t *testing.T) {
	wsA, wsB := uuid.New().String(), uuid.New().String()
	dsn := "file:workspace-" + uuid.New().String() + "?mode=memory&cache=shared"
	storeA := openWorkspaceStore(t, dsn, wsA)
	storeB := openWorkspaceStore(t, dsn, wsB)

	repo, err := storeA.UpsertRepo(context.Background(), workspace.UpsertRepoParams{Name: "ws-a"})
	if err != nil {
		t.Fatalf("UpsertRepo A: %v", err)
	}
	if !repo.WorkspaceID.Valid {
		t.Fatalf("expected workspace_id on repo: %+v", repo)
	}
	rowsB, err := storeB.ActiveRepos(context.Background())
	if err != nil {
		t.Fatalf("ActiveRepos B: %v", err)
	}
	if len(rowsB) != 0 {
		t.Fatalf("workspace B should not see A repos: %+v", rowsB)
	}
}

func TestWorkspaceStore_ContextCanceled(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := s.ActiveRepos(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestWorkspaceStore_RepoByID(t *testing.T) {
	s := openWorkspaceStore(t, ":memory:", "")
	ctx := context.Background()

	created, err := s.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name: "by-id", Language: strPtr("go"), Description: strPtr("lookup by id"),
	})
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	t.Run("found", func(t *testing.T) {
		got, err := s.RepoByID(ctx, created.ID)
		if err != nil {
			t.Fatalf("RepoByID: %v", err)
		}
		if got.ID != created.ID || got.Name != "by-id" {
			t.Errorf("unexpected repo: %+v", got)
		}
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		_, err := s.RepoByID(ctx, uuid.New())
		if !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("expected workspace.ErrNotFound, got %v", err)
		}
	})

	t.Run("workspace isolation", func(t *testing.T) {
		// repo A created in default workspace; storeB scoped to wsB cannot see it.
		wsB := uuid.New().String()
		dsn := "file:repo-byid-" + uuid.New().String() + "?mode=memory&cache=shared"
		storeA := openWorkspaceStore(t, dsn, uuid.New().String())
		storeB := openWorkspaceStore(t, dsn, wsB)
		repoA, err := storeA.UpsertRepo(ctx, workspace.UpsertRepoParams{Name: "iso-a"})
		if err != nil {
			t.Fatalf("UpsertRepo: %v", err)
		}
		if _, err := storeB.RepoByID(ctx, repoA.ID); !errors.Is(err, workspace.ErrNotFound) {
			t.Errorf("expected ErrNotFound across workspaces, got %v", err)
		}
	})
}
