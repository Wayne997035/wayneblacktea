package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/snapshot"
)

// statusSnapshotJobTimeout caps the full Saturday status snapshot run.
// Each slug's Haiku call + DB write should be fast; 5 minutes is generous even
// for workspaces with many slugs.
const statusSnapshotJobTimeout = 5 * time.Minute

// runStatusSnapshot is the core logic of the Saturday status snapshot job.
//
// It:
//  1. Lists all slugs that have existing snapshots (or falls back to the
//     primary project slug when no slugs are found — ensures first-run works).
//  2. For each slug, calls snapshot.EnsureSnapshot with force_refresh=true so
//     Saturday always produces a fresh snapshot regardless of the 24 h cache.
//
// All errors are logged at warn level; the function never panics.
// workspace_id is taken from statusSnapshotDeps.workspaceID, honouring the
// WORKSPACE_ID env var set at startup — we never cross-tenant scan.
func runStatusSnapshot(deps statusSnapshotDeps) {
	// Independent timeout — MUST NOT inherit a request context.
	ctx, cancel := context.WithTimeout(context.Background(), statusSnapshotJobTimeout)
	defer cancel()

	slugs, err := listSlugsForSnapshot(ctx, deps)
	if err != nil {
		slog.Warn("status snapshot: listing slugs failed", "err", err)
		return
	}

	if len(slugs) == 0 {
		slog.Info("status snapshot: no slugs found, skipping")
		return
	}

	generated := 0
	for _, slug := range slugs {
		_, _, err := snapshot.EnsureSnapshot(
			ctx, slug, true, // force_refresh = true on Saturday
			deps.store, deps.generator,
			deps.decision, deps.gtd,
			deps.workspaceID,
		)
		if err != nil {
			slog.Warn("status snapshot: failed for slug", "slug", slug, "err", err)
			continue
		}
		slog.Info("status snapshot: generated", "slug", slug)
		generated++
	}

	slog.Info("status snapshot: cron completed",
		"slugs_attempted", len(slugs),
		"snapshots_generated", generated,
	)
}

// listSlugsForSnapshot returns the distinct slugs to snapshot this run.
// It queries the snapshot store for existing slugs; if none exist yet (first
// run) it falls back to the primary project slug so Saturday cron is useful
// from day one.
func listSlugsForSnapshot(ctx context.Context, deps statusSnapshotDeps) ([]string, error) {
	slugs, err := deps.store.LatestSlugs(ctx)
	if err != nil {
		return nil, fmt.Errorf("status snapshot: listing slugs: %w", err)
	}
	if len(slugs) > 0 {
		return slugs, nil
	}
	// First-run: no existing snapshots — seed with the primary project slug.
	return []string{"wayneblacktea"}, nil
}
