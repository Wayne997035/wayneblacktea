package decision

import (
	"context"

	"github.com/google/uuid"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// StoreIface is the backend-agnostic contract for the Decision bounded
// context.
type StoreIface interface {
	Log(ctx context.Context, p LogParams) (*db.Decision, error)
	ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error)
	All(ctx context.Context, limit int32) ([]db.Decision, error)
	ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error)
}

var _ StoreIface = (*Store)(nil)
