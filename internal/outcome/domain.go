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
// upper bound. Reproduced empirically as 20 -> 40 -> 60 -> 80 -> 100 across
// 5 enrich calls of 20 fresh UUIDs each, with no backend-side limit
// stopping a 6th, 7th, ... Nth call (PR #152 round 5 Major, M-R5-1).
//
// What this actually protects against (corrected, PR #152 round 6 Minor —
// the PREVIOUS version of this comment mis-attributed the risk to
// scheduler/behavior_governance.go's Wednesday O(N*M) ApplyOutcome job:
// that job's `switch o.Result` treats "unknown" as `default: continue`
// (behavior_governance.go), so a draft accumulating related_rule_ids via
// repeated enrich calls is NEVER scanned by that job while it stays a
// draft — the enrich-accumulation phase itself contributes ZERO cost to
// that job, so it cannot be the thing this cap defends). The real risk this
// cap bounds is unbounded per-ROW storage growth and unbounded MCP response
// payload size: list_recent_outcomes / find_failed_patterns
// (tools_outcome.go's maxOutcomeLimit=100) return the full related_rule_ids
// array for every returned row, so a single uncapped array inflates every
// subsequent list/find call's response size (and the context an agent
// consuming that response pays for) indefinitely. As a secondary,
// incidental effect: IF a draft that accumulated related_rule_ids is
// eventually finalized to a terminal result, this same cap also bounds
// that row's contribution to the Wednesday job's O(N*M) cost once it
// becomes eligible for scanning — but that is not why this cap exists; it
// exists to bound row/array size and response payload.
//
// Both backends' FinalizeDraft (store.go's Store.FinalizeDraft,
// storage/sqlite/outcome.go's OutcomeStore.FinalizeDraft) apply
// CapRelatedRuleIDs (store.go) to the merged/unioned array — see that
// function's doc comment for the exact "an already-linked rule ID can
// never be silently removed, even if existing already exceeds the cap"
// rule (PR #152 round 6 Major, m-R6-3) — and a slog.Warn is emitted on the
// write path only when truncation actually removed something.
//
// Derivation-quality note: 100 is NOT derived from a storage-cost or
// response-payload budget. It is a round, generous ceiling set at 5x the
// per-call cap (20*5=100) — chosen to comfortably exceed any realistic
// cross-linking workflow while still being finite, and it happens to match
// the exact shape of the M-R5-1 attack repro (5 retries * 20 IDs/retry).
// Treat it as a deliberately conservative "stop unbounded growth" number,
// not a tightly reasoned capacity limit.
const MaxRelatedRuleIDsTotal = 100

// MaxNotesTotalRunes caps the CUMULATIVE size (in runes) of a single outcome
// row's Notes field across its entire append-only enrich lifetime
// (FinalizeDraft's "\n\n"-joined append, migration 000075) — a different
// guarantee from sanitize.Notes's per-call 500-rune cap
// (backend-security-design.md §5.4), which only bounds what ONE
// record_outcome/finish_work call may contribute. Without this cap, N
// enrich calls against the same draft (each carrying up to 500 runes) grow
// the stored Notes column by up to 500*N runes with no upper bound — the
// same unbounded-accumulation shape as MaxRelatedRuleIDsTotal above, and
// the direct cause of PR #152 round 6 Major finding M-R6-2's O(N^2) atomize
// amplification (reproduced empirically as 30 enrich calls -> a
// 15,058-rune final Notes field -> 233,370 CUMULATIVE runes sent to the
// atomizer across those 30 calls, ~15.5x the final document size, because
// every call re-atomized the FULL accumulated Notes text instead of just
// what it added).
//
// This cap and the O(N^2) atomize fix are two SEPARATE, complementary
// closes for the same underlying defect: tools_outcome.go's notesDelta now
// ensures each call only ever sends its OWN newly-added segment to the
// atomizer (bounding atomize cost independent of this cap), while
// MaxNotesTotalRunes independently bounds the stored field's own size (row
// storage, and list_recent_outcomes/find_failed_patterns response payload —
// see MaxRelatedRuleIDsTotal's doc comment above for the identical
// response-payload argument, which applies to Notes exactly the same way).
// Both backends' FinalizeDraft apply CapNotesTotal (store.go) to the
// merged/appended Notes value, mirroring CapRelatedRuleIDs's "existing
// content is never silently removed, even if it already exceeds the cap"
// rule.
//
// Derivation: chosen as 10x the per-call cap (500*10=5,000 runes). Ten
// enrich calls against the SAME draft before it is finalized is already
// well beyond any realistic personal-scale postmortem workflow — this
// codebase's own production usage pattern (and its test suite) tops out at
// 2-3 enrich calls per outcome: an initial record_outcome, maybe one
// correction, then finalize. 5,000 runes (~800-1,000 words) comfortably
// fits a genuine multi-paragraph audit trail with headroom to spare. This
// is not a tightly reasoned capacity limit derived from a hard cost model
// (mirroring MaxRelatedRuleIDsTotal's own derivation-quality caveat above)
// — it is a deliberately generous, round ceiling chosen to be far above any
// legitimate single-outcome usage while still being finite.
const MaxNotesTotalRunes = 5000

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
