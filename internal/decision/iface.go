package decision

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Decision bounded
// context.
type StoreIface interface {
	Log(ctx context.Context, p LogParams) (*db.Decision, error)
	ByRepo(ctx context.Context, repoName string, limit int32) ([]db.Decision, error)
	All(ctx context.Context, limit int32) ([]db.Decision, error)
	ByProject(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Decision, error)
	// ByTask returns the most recent decisions linked to a specific task UUID.
	// SECURITY: scoped to workspace_id.
	ByTask(ctx context.Context, taskID uuid.UUID, limit int32) ([]db.Decision, error)
	// SearchByCosine returns the top-limit decisions most similar to queryEmbedding.
	// SECURITY: scoped to workspace_id.
	SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.Decision, error)
	// List returns decisions filtered by ListParams (project XOR repo, plus
	// IncludeAuto). Workspace is NOT a ListParams field — it stays
	// store-scoped. See ListParams.Validate for the rejected combinations.
	List(ctx context.Context, p ListParams) ([]db.Decision, error)
}

var _ StoreIface = (*Store)(nil)
