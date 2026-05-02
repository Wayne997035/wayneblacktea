package decay

import (
	"context"
	"log/slog"
	"time"
)

// pruneJobTimeout caps each soft-prune run. A full workspace scan + UPDATE
// should finish well within 60 s; we leave room for a slow DB.
const pruneJobTimeout = 60 * time.Second

// softPruneAgeCutoff is the minimum age a row must have before it is eligible
// for soft deletion. Items younger than 90 days are NEVER pruned regardless of
// strength, per the design boundary in the acceptance criteria.
const softPruneAgeCutoff = 90 * 24 * time.Hour

// strengthThreshold is the Ebbinghaus strength below which an item is pruned.
const strengthThreshold = 0.05

// PrunerStore is the minimal interface the Pruner needs from each table store.
// Only knowledge_items and concepts implement this; decisions is excluded by
// design (it is an audit trail / truth source).
type PrunerStore interface {
	// SoftPruneDecayed sets archived_at=NOW() on rows where:
	//   - archived_at IS NULL (not already pruned)
	//   - created_at < cutoff (older than 90 days)
	//   - computed strength < threshold
	// Returns the number of rows soft-deleted.
	SoftPruneDecayed(ctx context.Context, cutoff time.Time, strengthThreshold float64) (int64, error)
}

// Pruner runs the daily soft-prune job across all eligible stores.
// Decisions table is intentionally excluded.
type Pruner struct {
	knowledge PrunerStore
	concepts  PrunerStore
}

// NewPruner creates a Pruner. Both stores are required; pass nil to skip a
// store (e.g. SQLite builds that only have one backend).
func NewPruner(knowledge, concepts PrunerStore) *Pruner {
	return &Pruner{
		knowledge: knowledge,
		concepts:  concepts,
	}
}

// Run executes the daily soft-prune cycle. It is designed to be called from
// the gocron scheduler; all errors are logged at warn level so the scheduler
// keeps running other jobs.
//
// Context is intentionally NOT the request context — each call creates its
// own timeout-scoped background context.
func (p *Pruner) Run() {
	ctx, cancel := context.WithTimeout(context.Background(), pruneJobTimeout)
	defer cancel()

	cutoff := time.Now().UTC().Add(-softPruneAgeCutoff)

	if p.knowledge != nil {
		n, err := p.knowledge.SoftPruneDecayed(ctx, cutoff, strengthThreshold)
		if err != nil {
			slog.Warn("decay pruner: knowledge_items prune failed", "err", err)
		} else if n > 0 {
			slog.Info("decay pruner: knowledge_items soft-pruned", "count", n)
		}
	}

	if p.concepts != nil {
		n, err := p.concepts.SoftPruneDecayed(ctx, cutoff, strengthThreshold)
		if err != nil {
			slog.Warn("decay pruner: concepts prune failed", "err", err)
		} else if n > 0 {
			slog.Info("decay pruner: concepts soft-pruned", "count", n)
		}
	}
}
