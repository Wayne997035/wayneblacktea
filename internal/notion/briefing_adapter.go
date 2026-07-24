package notion

import (
	"context"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
	"github.com/Wayne997035/wayneblacktea/internal/proposal"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
)

// briefingStoresAdapter implements BriefingStores by delegating to the
// backend-agnostic StoreIface types from storage.ServerStores.
type briefingStoresAdapter struct {
	gtd      gtd.StoreIface
	learning learning.StoreIface
	proposal proposal.StoreIface
	decision decision.StoreIface
}

func (a *briefingStoresAdapter) Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error) {
	tasks, err := a.gtd.Tasks(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("gtd tasks: %w", err)
	}
	return tasks, nil
}

func (a *briefingStoresAdapter) DueReviews(ctx context.Context, limit int) ([]learning.DueReview, error) {
	reviews, err := a.learning.DueReviews(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("due reviews: %w", err)
	}
	return reviews, nil
}

func (a *briefingStoresAdapter) ListPending(ctx context.Context) ([]db.PendingProposal, error) {
	pending, err := a.proposal.ListPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending proposals: %w", err)
	}
	return pending, nil
}

func (a *briefingStoresAdapter) All(ctx context.Context, limit int32) ([]db.Decision, error) {
	decisions, err := a.decision.All(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("all decisions: %w", err)
	}
	return decisions, nil
}

func (a *briefingStoresAdapter) WeeklyProgress(ctx context.Context) (completed, total int64, err error) {
	completed, total, err = a.gtd.WeeklyProgress(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("weekly progress: %w", err)
	}
	return completed, total, nil
}

// NewBriefingStores adapts a storage.ServerStores bundle into the
// BriefingStores surface BuildDailyBriefing needs. Used by cmd/server to wire
// the scheduler's daily briefing job.
func NewBriefingStores(stores storage.ServerStores) BriefingStores {
	return &briefingStoresAdapter{
		gtd:      stores.GTD(),
		learning: stores.Learning(),
		proposal: stores.Proposal(),
		decision: stores.Decision(),
	}
}
