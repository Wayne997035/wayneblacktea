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

	// ExistsForEntity reports whether an outcome row already exists for the
	// given entity, optionally scoped to workspaceID. outcomes has no unique
	// constraint on (entity_type, entity_id) — see migrations/000054 — so
	// callers needing idempotent outcome creation (e.g. complete_task's
	// draft-outcome seeding) MUST check this first.
	ExistsForEntity(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (bool, error)

	// GetLatestForEntity returns the most recently created outcome for the
	// given entity, scoped to workspaceID when non-nil. Returns ErrNotFound
	// when no outcome exists yet. Used by RecordExecutionResult
	// (lifecycle.go, decision 80c1e8ae) to decide whether a new call must
	// finalize a draft, supersede a terminal result, replay idempotently, or
	// create a fresh row.
	GetLatestForEntity(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (Outcome, error)

	// FinalizeDraft transitions an existing result='unknown' draft outcome
	// to a terminal result IN PLACE — same row, same ID, never a new row.
	// The update is guarded by `WHERE result = 'unknown'`, making this
	// race-safe: if a concurrent caller already finalized the same draft,
	// ErrDraftAlreadyFinalized is returned so the orchestration layer can
	// re-resolve and fall through to the terminal-over-terminal branch.
	// params.EntityType/EntityID/WorkspaceID are not re-applied. Result and
	// UpdatedAt are always written; Notes/Metrics/RelatedRuleIDs/
	// WorkSessionID are append-only (migration 000075) — see the PG/SQLite
	// implementations' own doc comments for the exact per-field merge rule.
	// No existing content in any of those four fields can ever be removed
	// or overwritten by this call.
	FinalizeDraft(ctx context.Context, id uuid.UUID, params CreateOutcomeParams) (Outcome, error)

	// SeedDraft atomically ensures a result='unknown' draft exists for the
	// given entity: if ANY outcome (draft or terminal) already exists for
	// the entity, that row is returned unchanged (created=false — mirrors
	// the pre-existing ExistsForEntity semantics: don't add a redundant
	// draft on top of a real result). Otherwise it inserts a fresh draft via
	// INSERT ... ON CONFLICT DO NOTHING against the partial unique index
	// idx_outcomes_one_open_draft (migration 000074): concurrent callers
	// racing to seed the very first draft for an entity are serialized by
	// the database, closing the check-then-insert TOCTOU that the old
	// ExistsForEntity-then-CreateOutcome pattern had. Used by
	// complete_task's seedDraftOutcome (internal/mcp/tools_gtd.go).
	SeedDraft(ctx context.Context, workspaceID *uuid.UUID, entityType string, entityID uuid.UUID) (o Outcome, created bool, err error)
}

// Compile-time assertion: Postgres Store satisfies StoreIface.
var _ StoreIface = (*Store)(nil)
