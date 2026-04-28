package workspace

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/db"
)

// StoreIface is the backend-agnostic contract for the Workspace bounded
// context (tracked Git repos, not the workspace_id scoping concept).
type StoreIface interface {
	ActiveRepos(ctx context.Context) ([]db.Repo, error)
	RepoByName(ctx context.Context, name string) (*db.Repo, error)
	UpsertRepo(ctx context.Context, p UpsertRepoParams) (*db.Repo, error)
}

var _ StoreIface = (*Store)(nil)
