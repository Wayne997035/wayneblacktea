package outcome

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// StoreIface is the backend-agnostic contract for the Outcome bounded context.
// Postgres-backed *Store and SQLite-backed *OutcomeStore both satisfy this
// interface.
type StoreIface interface {
	// CreateOutcome records the result of an executed entity (task/decision/
	// sprint/project) and returns the persisted record.
	CreateOutcome(ctx context.Context, params CreateOutcomeParams) (Outcome, error)

	// GetOutcomeByID fetches a single outcome by primary key, scoped to the
	// given workspace when non-nil. Returns ErrNotFound when no row matches.
	GetOutcomeByID(ctx context.Context, id uuid.UUID, workspaceID *uuid.UUID) (Outcome, error)

	// ListRecentOutcomes returns outcomes ordered by created_at DESC, filtered
	// by workspaceID (when non-nil) and entityType (when non-empty). limit is
	// capped by the store; callers SHOULD pass a reasonable value (≤ 100).
	ListRecentOutcomes(ctx context.Context, workspaceID *uuid.UUID, entityType string, limit int) ([]Outcome, error)

	// CreateEvaluation attaches a structured analysis record to an existing
	// outcome. The caller is responsible for verifying the outcome exists first
	// (GetOutcomeByID) to provide a helpful error message to the MCP user.
	CreateEvaluation(ctx context.Context, params CreateEvaluationParams) (Evaluation, error)

	// ListEvaluationsByOutcomeID returns all evaluations for the given outcome,
	// scoped to workspaceID when non-nil, ordered by created_at ASC.
	ListEvaluationsByOutcomeID(ctx context.Context, outcomeID uuid.UUID, workspaceID *uuid.UUID) ([]Evaluation, error)

	// ListFailedOutcomes returns outcomes with result='failure' or
	// result='regressed', ordered by created_at DESC. Used by
	// find_failed_patterns to surface failure context for retrospection.
	ListFailedOutcomes(ctx context.Context, workspaceID *uuid.UUID, limit int) ([]Outcome, error)

	// PruneOlderThan hard-deletes outcomes and their evaluations older than
	// cutoff. Evaluations are deleted first (no FK cascade per red-line §9).
	// Called daily by the scheduler to enforce the 90-day TTL.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}

// Compile-time assertion: Postgres Store satisfies StoreIface.
var _ StoreIface = (*Store)(nil)
