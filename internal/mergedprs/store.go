// Package mergedprs persists observed merged PRs so a daily fuzzy-match
// detector can correlate them against legacy backlog tasks whose
// branch_name / pr_url linkage is null. See sprint
// feature/0519-gtd-reconcile-phase2 (GTD-fix 10/12).
//
// This package owns ONLY persistence (upsert / list-recent / prune). The
// matcher logic lives in `internal/gtd/fuzzy_reconcile.go` and reads tasks
// via gtd.StoreIface — it does not need to depend on this package directly.
package mergedprs

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AuditBodyExcerptMaxLen is the rune cap applied to body_excerpt before
// it's persisted to merged_prs_observed. UTF-8 safe via []rune slicing.
// Used by both HTTP (handler) and MCP reconcile paths so the dashboard
// sees a consistent length budget. Kept here (the persistence package)
// because the cap is a property of the audit row, not the call site.
const AuditBodyExcerptMaxLen = 500

// Observed is the domain record persisted into the merged_prs_observed table.
// BodyExcerpt is the sanitised, length-capped slice of the original PR body
// (sanitisation happens in the handler/MCP layer before invoking Upsert so
// the store is free of input-handling responsibility).
type Observed struct {
	ID          uuid.UUID
	WorkspaceID *uuid.UUID
	Repo        string
	URL         string
	HeadRef     string
	Title       string
	BodyExcerpt string
	MergedAt    time.Time
	ObservedAt  time.Time
}

// UpsertParams holds the inputs for Store.Upsert.
type UpsertParams struct {
	WorkspaceID *uuid.UUID
	Repo        string
	URL         string
	HeadRef     string
	Title       string
	BodyExcerpt string
	MergedAt    time.Time
}

// Store is the backend-agnostic interface for merged_prs_observed. Both
// SQLite and Postgres stores must satisfy this interface.
type Store interface {
	// Upsert inserts a row keyed by URL. On conflict (url) the observed_at is
	// refreshed to NOW() and the other fields updated to the latest values so
	// subsequent reposts (e.g. "wbt reconcile --since" replay) refresh the
	// timestamp instead of duplicating.
	Upsert(ctx context.Context, p UpsertParams) error

	// RecentObservedSince returns rows with observed_at >= since, scoped to
	// the store's workspace (or override via workspaceID). Ordered by
	// observed_at DESC.
	RecentObservedSince(ctx context.Context, since time.Time, workspaceID *uuid.UUID) ([]Observed, error)

	// PruneOlderThan deletes rows whose observed_at is older than olderThan
	// before now(). Returns the number of rows deleted. Implementations MUST
	// use a code constant, not user input, for the duration to keep this
	// safe against SQL injection vectors.
	PruneOlderThan(ctx context.Context, olderThan time.Duration) (int64, error)
}
