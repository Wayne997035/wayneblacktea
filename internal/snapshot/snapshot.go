// Package snapshot manages project status snapshots — Haiku-generated
// structured summaries of a project's current state (sprint progress, gap
// analysis, pending work). These are derived/computed data, not user-authored
// knowledge, and therefore bypass the pending_proposals review gate.
package snapshot

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no snapshot exists for the given slug.
var ErrNotFound = errors.New("snapshot: not found")

// Snapshot is a single status snapshot row.
type Snapshot struct {
	ID                uuid.UUID   `json:"id"`
	Slug              string      `json:"slug"`
	WorkspaceID       *uuid.UUID  `json:"workspace_id,omitempty"`
	GeneratedAt       time.Time   `json:"generated_at"`
	SprintSummary     string      `json:"sprint_summary"`
	GapAnalysis       string      `json:"gap_analysis"`
	SotaCatchupPct    int         `json:"sota_catchup_pct"`
	PendingSummary    string      `json:"pending_summary"`
	SourceDecisionIDs []uuid.UUID `json:"source_decision_ids,omitempty"`
	Source            string      `json:"source"`
}

// WriteParams collects the fields needed to insert a new snapshot row.
type WriteParams struct {
	Slug              string
	WorkspaceID       *uuid.UUID
	SprintSummary     string
	GapAnalysis       string
	SotaCatchupPct    int
	PendingSummary    string
	SourceDecisionIDs []uuid.UUID
	Source            string
}

// IsNotFound reports whether err wraps ErrNotFound.
func IsNotFound(err error) bool {
	return err != nil && (errors.Is(err, ErrNotFound) || strings.Contains(err.Error(), ErrNotFound.Error()))
}

// StoreIface is the backend-agnostic contract for the snapshot bounded context.
type StoreIface interface {
	// Write inserts a new snapshot row and returns the persisted record.
	Write(ctx context.Context, p WriteParams) (*Snapshot, error)
	// LatestFresh returns the newest snapshot for slug whose generated_at is
	// within the given maxAge window. Returns ErrNotFound when none qualifies.
	LatestFresh(ctx context.Context, slug string, maxAge time.Duration) (*Snapshot, error)
	// LatestSlugs returns the distinct slugs that have at least one snapshot,
	// scoped to the configured workspace. Used by the Saturday cron to enumerate
	// all known projects.
	LatestSlugs(ctx context.Context) ([]string, error)
}
