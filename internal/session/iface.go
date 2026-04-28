package session

import (
	"context"

	"github.com/google/uuid"
	"github.com/waynechen/wayneblacktea/internal/db"
)

// StoreIface is the backend-agnostic contract for the Session bounded
// context.
type StoreIface interface {
	SetHandoff(ctx context.Context, p HandoffParams) (*db.SessionHandoff, error)
	LatestHandoff(ctx context.Context) (*db.SessionHandoff, error)
	Resolve(ctx context.Context, id uuid.UUID) error
}

var _ StoreIface = (*Store)(nil)
