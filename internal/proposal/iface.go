package proposal

import (
	"context"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Proposal bounded
// context. AutoProposeConceptFromKnowledge is included because it is the
// only public helper that callers (HTTP, MCP) currently invoke directly.
type StoreIface interface {
	Create(ctx context.Context, p CreateParams) (*db.PendingProposal, error)
	Get(ctx context.Context, id uuid.UUID) (*db.PendingProposal, error)
	ListPending(ctx context.Context) ([]db.PendingProposal, error)
	// ListAll returns all proposals of the given type regardless of status,
	// newest first, up to limit rows. Used by GET /api/proposals?status=.
	ListAll(ctx context.Context, proposalType string, limit int32) ([]db.PendingProposal, error)
	Resolve(ctx context.Context, id uuid.UUID, status Status) (*db.PendingProposal, error)
	// BatchConfirm resolves multiple proposals to the given status in a single
	// operation. On Postgres the entire batch runs inside one transaction —
	// any individual failure rolls back the whole batch. On SQLite each ID is
	// processed independently (best-effort). Callers MUST validate ids length
	// (1–100) and status before invoking.
	BatchConfirm(ctx context.Context, ids []uuid.UUID, status Status) (BatchConfirmResult, error)
	AutoProposeConceptFromKnowledge(ctx context.Context, item *db.KnowledgeItem, proposedBy string) (*db.PendingProposal, error)
}

var _ StoreIface = (*Store)(nil)
