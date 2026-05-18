package knowledge

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Knowledge bounded
// context. Search semantics differ between backends (Postgres FTS + pgvector
// vs SQLite FTS5 + sqlite-vec); the interface itself stays minimal.
type StoreIface interface {
	AddItem(ctx context.Context, p AddItemParams) (*db.KnowledgeItem, error)
	Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error)
	// SearchCoarse searches only root rows (heading_level=0 or parent_id IS NULL).
	// Used by search_knowledge mode="coarse".
	SearchCoarse(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error)
	List(ctx context.Context, limit, offset int) ([]db.KnowledgeItem, error)
	GetByID(ctx context.Context, id uuid.UUID) (*db.KnowledgeItem, error)
	// UpdateLearningValue sets the star-rating (1–5) for the given knowledge
	// item. Returns ErrNotFound when the item does not exist in the workspace.
	// SECURITY: value is validated 1–5 exhaustively by the handler layer.
	UpdateLearningValue(ctx context.Context, id uuid.UUID, value int) error
	// SearchByCosine returns the top-limit knowledge items most similar to queryEmbedding.
	// SECURITY: scoped to workspace_id.
	SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.KnowledgeItem, error)
	// ListChildren returns direct children of parentID ordered by heading_level, created_at.
	// Used by navigate_knowledge and outline_knowledge tools.
	ListChildren(ctx context.Context, parentID uuid.UUID) ([]*db.KnowledgeItem, error)
	// ListRoots returns top-level items (parent_id IS NULL) scoped to the workspace.
	// Used by navigate_knowledge when no parent_id argument is supplied.
	ListRoots(ctx context.Context) ([]*db.KnowledgeItem, error)
	// ListByProjectID returns knowledge items associated with a project UUID.
	// Results are ordered by created_at DESC and capped at limit rows.
	// SECURITY: scoped to workspace_id.
	ListByProjectID(ctx context.Context, projectID uuid.UUID, limit int) ([]db.KnowledgeItem, error)
	// ListByTaskID returns knowledge items associated with a task UUID.
	// Results are ordered by created_at DESC and capped at limit rows.
	// SECURITY: scoped to workspace_id.
	ListByTaskID(ctx context.Context, taskID uuid.UUID, limit int) ([]db.KnowledgeItem, error)
}

var _ StoreIface = (*Store)(nil)
