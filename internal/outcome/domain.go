// Package outcome implements the Outcome + Evaluation bounded context.
// It closes the Action→Result loop: after a task/decision/sprint completes,
// record_outcome captures what actually happened, and evaluate_outcome attaches
// structured analysis and improvement suggestions.
package outcome

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested outcome does not exist.
var ErrNotFound = errors.New("outcome: not found")

// ErrDraftAlreadyFinalized is returned by FinalizeDraft when the targeted
// row's result is no longer "unknown" — a concurrent caller already
// finalized it. Callers (see RecordExecutionResult in lifecycle.go) treat
// this as a signal to re-resolve the entity's latest outcome rather than an
// unrecoverable error.
var ErrDraftAlreadyFinalized = errors.New("outcome: draft already finalized")

// AllowedEntityTypes is the closed set of entity_type values accepted by the store.
// Enforcement at the MCP tool layer prevents arbitrary strings from polluting the
// entity_type column and makes queries predictable.
var AllowedEntityTypes = map[string]bool{
	"task":     true,
	"decision": true,
	"sprint":   true,
	"project":  true,
}

// AllowedResults is the closed set of result values.
// Mirrored in the DB CHECK constraint for dual-layer defence.
var AllowedResults = map[string]bool{
	"success":   true,
	"failure":   true,
	"partial":   true,
	"unknown":   true,
	"regressed": true,
}

// MaxRelatedRuleIDsTotal caps the CUMULATIVE size of a single outcome row's
// related_rule_ids array across its entire append-only lifetime — a
// different guarantee from internal/mcp/tools_outcome.go's maxRelatedRuleIDs
// (20), which only bounds how many IDs a SINGLE record_outcome call may
// supply. Migration 000075's append-only UNION redesign made
// related_rule_ids grow monotonically across repeated
// record_outcome(result="unknown", related_rule_ids=[...]) enrich calls
// against the same draft (FinalizeDraft's element-level union, never a
// replace) — with no cumulative cap, N enrich calls each carrying
// maxRelatedRuleIDs distinct UUIDs grow the stored array to N*20 with no
// upper bound. That is exactly the O(entities * related_rule_ids)
// amplification maxRelatedRuleIDs's own doc comment already warns about:
// scheduler/behavior_governance.go's Wednesday job does one ApplyOutcome DB
// round trip per related_rule_id per outcome. See PR #152 round 5 Major
// (M-R5-1) guarantee A — reproduced empirically as 20 -> 40 -> 60 -> 80 ->
// 100 across 5 enrich calls of 20 fresh UUIDs each, with no backend-side
// limit stopping a 6th, 7th, ... Nth call.
//
// Both backends' FinalizeDraft (store.go's Store.FinalizeDraft,
// storage/sqlite/outcome.go's OutcomeStore.FinalizeDraft) truncate the
// merged/unioned array to this cap AFTER computing the union — never
// before — so already-stored entries (which sort first in the union, per
// FinalizeDraft's own "existing IDs stay first in original order" rule)
// are preserved; only the newly-appended tail is ever dropped, and a
// slog.Warn is emitted on the write path when truncation actually removes
// something. Set to 5x the per-call cap (20*5=100): generous enough for
// realistic cross-linking, but stops the exact escalation this finding
// reproduced (5 retries * 20 IDs/retry = 100) from continuing unbounded.
const MaxRelatedRuleIDsTotal = 100

// Outcome is the domain model for a recorded execution result.
type Outcome struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID *uuid.UUID `json:"workspace_id,omitempty"`
	EntityType  string     `json:"entity_type"`
	EntityID    uuid.UUID  `json:"entity_id"`
	Result      string     `json:"result"`
	Metrics     []byte     `json:"metrics,omitempty"` // raw JSONB / JSON TEXT
	Notes       string     `json:"notes,omitempty"`
	// RelatedRuleIDs optionally links this outcome to one or more behavior rules.
	// The behavior governance scheduler job uses this to call ApplyOutcome per
	// referenced rule, closing the outcome→rule confidence feedback loop.
	// NO FK per CLAUDE.md #9; stale rule IDs are tolerated application-side.
	RelatedRuleIDs []uuid.UUID `json:"related_rule_ids,omitempty"`
	// WorkSessionID optionally links this outcome back to the work_sessions
	// row it was recorded from (wbt-2.0 P2.4, migration 000067). NO FK per
	// CLAUDE.md #9; a stale session_id is tolerated application-side.
	WorkSessionID *uuid.UUID `json:"work_session_id,omitempty"`
	// SupersedesID optionally links this outcome to the prior outcome row it
	// explicitly replaces (migration 000074, decision 80c1e8ae). Set only by
	// RecordExecutionResult's supersede branch (lifecycle.go) when the
	// entity already has a DIFFERENT terminal result — the prior row is left
	// untouched, never silently overwritten. NO FK per CLAUDE.md #9.
	SupersedesID *uuid.UUID `json:"supersedes_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	// UpdatedAt records the last time this row was actually written to in
	// place by FinalizeDraft (migration 000075, append-semantics redesign).
	// Backfilled to CreatedAt for rows that predate the migration — see the
	// migration's comment for why that's the correct value, not a
	// placeholder. CreateOutcome-inserted rows also start with
	// UpdatedAt == CreatedAt (never yet modified). No-op FinalizeDraft
	// paths (ActionDraftPreserved / ActionReplayedIdempotent — see
	// lifecycle.go) never reach the store layer at all, so this column is
	// only ever bumped by a genuine write.
	UpdatedAt time.Time `json:"updated_at"`
}

// Evaluation is a structured analysis record attached to an Outcome.
type Evaluation struct {
	ID                     uuid.UUID  `json:"id"`
	WorkspaceID            *uuid.UUID `json:"workspace_id,omitempty"`
	OutcomeID              uuid.UUID  `json:"outcome_id"`
	Analysis               string     `json:"analysis"`
	Lessons                []byte     `json:"lessons"`                 // JSON array
	ImprovementSuggestions []byte     `json:"improvement_suggestions"` // JSON array
	CreatedAt              time.Time  `json:"created_at"`
}

// CreateOutcomeParams holds parameters for recording a new outcome.
type CreateOutcomeParams struct {
	WorkspaceID *uuid.UUID
	EntityType  string
	EntityID    uuid.UUID
	Result      string
	Metrics     []byte // JSON object or nil
	Notes       string
	// RelatedRuleIDs optionally links this outcome to one or more behavior rules.
	// When provided, the behavior governance scheduler job will call ApplyOutcome
	// for each rule. Pass empty slice (not nil) for clarity; nil is also accepted.
	RelatedRuleIDs []uuid.UUID
	// WorkSessionID optionally links this outcome to the work session it was
	// recorded from. See Outcome.WorkSessionID doc comment for the no-FK rationale.
	WorkSessionID *uuid.UUID
	// SupersedesID optionally marks this outcome as an explicit replacement
	// for a prior outcome row. Set by RecordExecutionResult (lifecycle.go);
	// direct StoreIface.CreateOutcome callers outside that seam normally
	// leave this nil. See Outcome.SupersedesID doc comment.
	SupersedesID *uuid.UUID
}

// CreateEvaluationParams holds parameters for evaluating an outcome.
type CreateEvaluationParams struct {
	WorkspaceID            *uuid.UUID
	OutcomeID              uuid.UUID
	Analysis               string
	Lessons                []byte // JSON array
	ImprovementSuggestions []byte // JSON array
}
