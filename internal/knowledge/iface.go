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
}

var _ StoreIface = (*Store)(nil)
