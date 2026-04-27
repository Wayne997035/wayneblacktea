package proposal

import (
	"context"

	"github.com/google/uuid"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// StoreIface is the backend-agnostic contract for the Proposal bounded
// context. AutoProposeConceptFromKnowledge is included because it is the
// only public helper that callers (HTTP, MCP) currently invoke directly.
type StoreIface interface {
	Create(ctx context.Context, p CreateParams) (*db.PendingProposal, error)
	Get(ctx context.Context, id uuid.UUID) (*db.PendingProposal, error)
	ListPending(ctx context.Context) ([]db.PendingProposal, error)
	Resolve(ctx context.Context, id uuid.UUID, status Status) (*db.PendingProposal, error)
	AutoProposeConceptFromKnowledge(ctx context.Context, item *db.KnowledgeItem, proposedBy string) (*db.PendingProposal, error)
}

var _ StoreIface = (*Store)(nil)
