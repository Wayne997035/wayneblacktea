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
	List(ctx context.Context, limit, offset int) ([]db.KnowledgeItem, error)
	GetByID(ctx context.Context, id uuid.UUID) (*db.KnowledgeItem, error)
	// UpdateLearningValue sets the star-rating (1–5) for the given knowledge
	// item. Returns ErrNotFound when the item does not exist in the workspace.
	// SECURITY: value is validated 1–5 exhaustively by the handler layer.
	UpdateLearningValue(ctx context.Context, id uuid.UUID, value int) error
	// SearchByCosine returns the top-limit knowledge items most similar to queryEmbedding.
	// SECURITY: scoped to workspace_id.
	SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.KnowledgeItem, error)
}

var _ StoreIface = (*Store)(nil)
