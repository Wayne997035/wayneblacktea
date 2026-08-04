package outcome

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// LifecycleAction reports which branch RecordExecutionResult took, so
// callers (MCP handlers) can decide whether follow-on side effects
// (SetOutcomeLink, background atomization) should fire. See GTD decision
// 80c1e8ae (G7): one logical execution maps to one canonical outcome.
type LifecycleAction string

const (
	// ActionCreated: no prior outcome existed for this entity — a fresh row
	// was inserted (SupersedesID left nil).
	ActionCreated LifecycleAction = "created"
	// ActionFinalizedDraft: an existing result='unknown' draft (seeded by
	// complete_task's seedDraftOutcome) was updated in place to a terminal
	// result. Same row, same ID — never a second row for the same draft.
	ActionFinalizedDraft LifecycleAction = "finalized_draft"
	// ActionSuperseded: the entity's latest outcome already had a terminal
	// result different from this call's — a NEW row was created with
	// SupersedesID pointing at the prior row, which is left unmodified
	// (explicit supersession, never a silent overwrite — see
	// backend-security-design.md's threat model on audit-trail loss).
	ActionSuperseded LifecycleAction = "superseded"
	// ActionReplayedIdempotent: the entity's latest outcome already has the
	// exact same result+notes+metrics as this call — no row was written;
	// the existing row is returned unchanged.
	ActionReplayedIdempotent LifecycleAction = "replayed_idempotent"
)

// RecordExecutionResult is the single convergence point for the outcome-
// mutating call sites that record a *result for an entity* — record_outcome
// and finish_work's autoCreateOutcomeOnFailure (internal/mcp/tools_outcome.go,
// tools_worksession.go). complete_task's seedDraftOutcome does NOT go through
// this function: it only ever wants a fresh result="unknown" placeholder and
// uses the dedicated atomic StoreIface.SeedDraft instead (see its doc
// comment for why the two have different atomicity requirements).
//
// Decision 80c1e8ae (G7) convergence rule:
//   - no prior outcome for the entity           -> fresh row (ActionCreated)
//   - prior outcome is a result='unknown' draft -> finalize IN PLACE
//     (ActionFinalizedDraft) — never a second row for the same draft
//   - prior outcome already terminal, identical -> no-op replay
//     (ActionReplayedIdempotent)
//   - prior outcome already terminal, different -> explicit supersession
//     (ActionSuperseded): new row, SupersedesID set, prior row untouched
//
// This is ordinary domain-layer orchestration against StoreIface — no SQL,
// no transaction spanning multiple statements — so it does not need the ADR
// 0003 dual-backend orchestration-seam exception (docs/adr/0003-*.md), which
// governs a narrower problem (duplicated per-backend transaction/policy
// code). Both outcome.Store (Postgres) and sqlite.OutcomeStore already
// implement the granular StoreIface methods this function composes
// (GetLatestForEntity, FinalizeDraft, CreateOutcome), exactly like every
// other StoreIface method — no new exception is being opened here.
//
// Known residual race (accepted, not closed by this function): if two
// different tools race on the SAME entity with NO prior outcome at all
// (e.g. complete_task's SeedDraft and a concurrent record_outcome both see
// "no outcome yet" in the same instant), each can independently create a
// fresh row. Closing that would require SeedDraft and CreateOutcome's
// fresh-insert path to share one collision domain, which is a larger change
// than the TOCTOU this unit was scoped to fix (concurrent complete_task
// calls on the SAME task, closed by SeedDraft's atomic upsert). Given
// personal-scale single-agent usage this is accepted and documented rather
// than silently ignored.
func RecordExecutionResult(ctx context.Context, store StoreIface, params CreateOutcomeParams) (Outcome, LifecycleAction, error) {
	latest, err := store.GetLatestForEntity(ctx, params.WorkspaceID, params.EntityType, params.EntityID)
	if errors.Is(err, ErrNotFound) {
		o, cErr := store.CreateOutcome(ctx, params)
		if cErr != nil {
			return Outcome{}, "", fmt.Errorf("recording execution result: %w", cErr)
		}
		return o, ActionCreated, nil
	}
	if err != nil {
		return Outcome{}, "", fmt.Errorf("recording execution result: resolving latest outcome: %w", err)
	}

	if latest.Result == "unknown" {
		finalized, ferr := store.FinalizeDraft(ctx, latest.ID, params)
		if errors.Is(ferr, ErrDraftAlreadyFinalized) {
			// Lost a race to a concurrent finalizer. Re-resolve: the draft is
			// now terminal, so this recursion lands in the terminal branch
			// below (bounded — a draft can only be finalized once).
			return RecordExecutionResult(ctx, store, params)
		}
		if ferr != nil {
			return Outcome{}, "", fmt.Errorf("recording execution result: finalizing draft: %w", ferr)
		}
		return finalized, ActionFinalizedDraft, nil
	}

	if isIdempotentReplay(latest, params) {
		return latest, ActionReplayedIdempotent, nil
	}

	supersedeParams := params
	latestID := latest.ID
	supersedeParams.SupersedesID = &latestID
	o, err := store.CreateOutcome(ctx, supersedeParams)
	if err != nil {
		return Outcome{}, "", fmt.Errorf("recording execution result: superseding: %w", err)
	}
	return o, ActionSuperseded, nil
}

// isIdempotentReplay reports whether params describes exactly the same
// operation as latest — same result, notes, and metrics payload — so a
// retried call returns the existing row instead of writing a semantically
// duplicate one. Metrics is compared byte-for-byte against what round-tripped
// from the store; a caller resubmitting differently-formatted-but-equivalent
// JSON (e.g. different key order) fails this check and falls through to the
// supersede branch — an extra explicit audit row, never a silent skip, so
// this is a conservative (fail-open-to-audit) heuristic.
func isIdempotentReplay(latest Outcome, params CreateOutcomeParams) bool {
	if latest.Result != params.Result {
		return false
	}
	if latest.Notes != params.Notes {
		return false
	}
	return bytes.Equal(latest.Metrics, params.Metrics)
}
