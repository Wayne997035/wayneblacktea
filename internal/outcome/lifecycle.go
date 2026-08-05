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

// resultUnknown mirrors the "unknown" value of AllowedResults (domain.go) —
// named here so the repeated draft-state comparisons below (and their test
// counterparts) satisfy goconst instead of re-typing the literal.
const resultUnknown = "unknown"

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
	// ActionDraftPreserved: the entity's latest outcome is a result='unknown'
	// draft, this call ALSO requested result='unknown', AND this call carries
	// no new content at all (Notes/Metrics/RelatedRuleIDs/WorkSessionID all
	// empty). "unknown" is definitionally the non-terminal draft state and
	// there is nothing to write, so no row is touched; the draft is returned
	// unchanged. See PR #152 finding M-1: a prompt-injected
	// record_outcome(result="unknown") call carrying no content against a
	// draft with real content (e.g. postmortem notes) must not silently wipe
	// it. A result="unknown" call that DOES carry new content takes the
	// ActionDraftEnriched path instead — see its doc comment and finding
	// M-2a (an earlier fix that only checked params.Result=="unknown" here,
	// without also checking for new content, silently dropped legitimate
	// writes carrying real notes).
	ActionDraftPreserved LifecycleAction = "draft_preserved"
	// ActionDraftEnriched: the entity's latest outcome is a result='unknown'
	// draft, this call ALSO requested result='unknown', but this call DOES
	// carry new content (at least one of Notes/Metrics/RelatedRuleIDs/
	// WorkSessionID is non-empty). The content is merged into the draft in
	// place via FinalizeDraft's COALESCE-based UPDATE (store.go /
	// storage/sqlite/outcome.go) — empty fields on this call leave the
	// draft's existing values untouched, non-empty fields overwrite them.
	// Result stays "unknown" (this is not a finalization, hence a distinct
	// action from ActionFinalizedDraft — a caller that maps action names to
	// "did this become terminal?" must not conflate the two). See PR #152
	// finding M-2a: the MCP instructions (server.go) document exactly this
	// call shape — record_outcome(result="unknown", notes=...) to enrich a
	// draft seeded by complete_task — so this path MUST write, not no-op.
	ActionDraftEnriched LifecycleAction = "draft_enriched"
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
//   - no prior outcome for the entity              -> fresh row (ActionCreated)
//   - prior outcome is a draft, this call is also
//     result='unknown', with NO new content         -> no-op, draft returned
//     unchanged (ActionDraftPreserved) — "unknown" is never a finalization,
//     see PR #152 finding M-1
//   - prior outcome is a draft, this call is also
//     result='unknown', but DOES carry new content   -> merge that content
//     into the draft IN PLACE via FinalizeDraft's COALESCE UPDATE
//     (ActionDraftEnriched) — result stays 'unknown', this is not a
//     finalization; see PR #152 finding M-2a
//   - prior outcome is a result='unknown' draft,
//     this call has a terminal result             -> finalize IN PLACE
//     (ActionFinalizedDraft) — never a second row for the same draft
//   - prior outcome already terminal, identical    -> no-op replay
//     (ActionReplayedIdempotent)
//   - prior outcome already terminal, different     -> explicit supersession
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

	if latest.Result == resultUnknown {
		// M-1 (PR #152): a call that ALSO says result="unknown" AND carries no
		// new content is never a legitimate write — "unknown" is
		// definitionally non-terminal, so there is nothing to finalize, and
		// nothing to merge either. Return the draft unchanged — no write
		// occurs, no side effect fires.
		//
		// M-2a (PR #152): the earlier fix stopped here, checking only
		// params.Result=="unknown" — which also silently dropped a call that
		// DOES carry new content (e.g. record_outcome(result="unknown",
		// notes="...") as the MCP instructions in server.go explicitly
		// document callers doing to enrich a complete_task-seeded draft).
		// That case must still reach FinalizeDraft (now merge-only, so it's
		// safe) so the content actually gets written — see ActionDraftEnriched.
		if params.Result == resultUnknown && !hasNewContent(params) {
			return latest, ActionDraftPreserved, nil
		}
		// Everything below writes. The only thing the result value changes is
		// which action we report: a terminal result finalizes the draft, an
		// "unknown" result that got this far carries new content and merely
		// enriches it (result stays non-terminal). Both go through the same
		// merge-only FinalizeDraft.
		action := ActionFinalizedDraft
		if params.Result == resultUnknown {
			action = ActionDraftEnriched
		}
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
		return finalized, action, nil
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

// hasNewContent reports whether params carries any payload beyond bare
// identity + result. Used by RecordExecutionResult's draft branch to decide
// between ActionDraftPreserved (nothing to write) and ActionDraftEnriched
// (merge the supplied content into the draft via FinalizeDraft) when the
// call also specifies result="unknown". See PR #152 finding M-2a.
func hasNewContent(params CreateOutcomeParams) bool {
	return params.Notes != "" ||
		len(params.Metrics) > 0 ||
		len(params.RelatedRuleIDs) > 0 ||
		params.WorkSessionID != nil
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
