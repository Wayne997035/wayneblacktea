package workspace

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Workspace bounded
// context (tracked Git repos, not the workspace_id scoping concept).
type StoreIface interface {
	ActiveRepos(ctx context.Context) ([]db.Repo, error)
	RepoByName(ctx context.Context, name string) (*db.Repo, error)
	// RepoByID returns a single repo by primary key UUID, scoped to the
	// configured workspace. Returns ErrNotFound when no row matches.
	RepoByID(ctx context.Context, id uuid.UUID) (*db.Repo, error)
	UpsertRepo(ctx context.Context, p UpsertRepoParams) (*db.Repo, error)

	// GetModelPreference returns the workspace's stored AI model, or
	// DefaultModelPreference when no row exists.
	GetModelPreference(ctx context.Context) (string, error)
	// UpsertModelPreference stores the workspace's AI model. Returns
	// ErrInvalidModel when model is not in AllowedModels.
	UpsertModelPreference(ctx context.Context, model string) error
}

var _ StoreIface = (*Store)(nil)
