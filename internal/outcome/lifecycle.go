package outcome

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
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
	// backend-security-design.md's threat model on audit-trail loss). This
	// new row's own Notes (params.Notes, whatever this call supplied — may
	// be byte-identical to the prior row's Notes, e.g. a re-evaluation that
	// keeps the same summary but reaches a different Result) has NEVER been
	// atomized before: it lives on a DIFFERENT outcome_id than the row it
	// supersedes, so it must be treated exactly like ActionCreated for the
	// purpose of the atomize side effect — see RecordExecutionResult's
	// previousNotes doc comment and PR #152 round 6 Major finding M-R6-1
	// (before this fix, this branch returned the PRIOR row's Notes as
	// previousNotes, so a supersede call whose own notes happened to be
	// byte-identical to the row it supersedes — a common shape: "same
	// summary, different verdict" — compared equal to that unrelated prior
	// row's Notes and silently skipped atomize for content that had never
	// actually been atomized under THIS row's outcome_id).
	ActionSuperseded LifecycleAction = "superseded"
	// ActionReplayedIdempotent: this call carries zero net-new information
	// relative to the entity's latest outcome — no row was written; the
	// existing row is returned unchanged. Reached from two different
	// comparisons depending on where latest sits:
	//   - latest is already terminal: isIdempotentReplay compares
	//     Result+Notes+Metrics byte-for-byte (see its doc comment).
	//   - latest is still a result='unknown' draft AND this call also
	//     requests result='unknown' WITH new content: isDraftIdempotentReplay
	//     compares every field FinalizeDraft would actually write (Notes,
	//     Metrics, RelatedRuleIDs, WorkSessionID) against what COALESCE-ing
	//     this call's params into latest would produce (see its doc
	//     comment). See PR #152 round 3 Major: before this branch existed, a
	//     byte-identical retry of a content-bearing enrich call re-entered
	//     FinalizeDraft a second time and re-reported ActionDraftEnriched,
	//     which re-fires SetOutcomeLink/atomize in tools_outcome.go for
	//     content that was already recorded.
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
	// carry genuinely new content (per isDraftIdempotentReplay — at least
	// one field would actually change something). The content is merged
	// into the draft IN PLACE via FinalizeDraft's append-only UPDATE
	// (store.go / storage/sqlite/outcome.go, migration 000075): existing
	// notes/metrics/related_rule_ids/work_session_id content is appended
	// to or added alongside, never overwritten or removed. Result stays
	// "unknown" (this is not a finalization, hence a distinct action from
	// ActionFinalizedDraft — a caller that maps action names to "did this
	// become terminal?" must not conflate the two). See PR #152 finding
	// M-2a: the MCP instructions (server.go) tell callers to "upgrade" a
	// complete_task-seeded draft via evaluate_outcome/record_outcome —
	// they don't pin an exact call shape, but a content-bearing
	// record_outcome(result="unknown", notes=...) enrich call is the
	// natural reading of "upgrade it", so this path MUST write, not no-op.
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
//     result='unknown', DOES carry new content, but that content is a
//     byte-identical retry of what's already stored (per
//     isDraftIdempotentReplay)                        -> no-op replay
//     (ActionReplayedIdempotent), FinalizeDraft is NOT called a second
//     time; see PR #152 round 3 Major
//   - prior outcome is a draft, this call is also
//     result='unknown', and DOES carry genuinely new content -> merge that
//     content into the draft IN PLACE via FinalizeDraft's COALESCE UPDATE
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
//
// previousNotes is "" for every branch that returns a row whose Notes has
// NEVER been atomized under that row's own outcome_id — ActionCreated (no
// prior row existed at all) AND ActionSuperseded (a NEW row, see its own
// doc comment for why PR #152 round 6 Major M-R6-1 moved it into this
// group: a supersede row's Notes lives under a fresh outcome_id regardless
// of whether its text happens to match the row it supersedes). For every
// OTHER branch — the ones that merge INTO the same pre-existing row via
// FinalizeDraft (ActionFinalizedDraft, ActionDraftEnriched, and the no-op
// replays ActionReplayedIdempotent/ActionDraftPreserved, which never reach
// the atomize call site anyway) — previousNotes is latest.Notes, the exact
// pre-write value of that SAME row.
//
// Added for PR #152 round 5 Major (M-R5-1) guarantee B: callers
// (tools_outcome.go's handleRecordOutcome) compare previousNotes against
// the returned Outcome's post-write Notes to decide whether a content-bearing
// side effect (background atomization) should fire — a call that only
// supplies new content in some OTHER field (e.g. related_rule_ids) leaves
// Notes byte-identical to previousNotes, so no re-atomize of already-atomized
// text should happen. Deliberately NOT keyed off LifecycleAction (e.g. "is
// this ActionDraftEnriched") — that was the exact classification that
// caused the bug: ActionDraftEnriched fires for ANY new content, not just
// new notes, so gating atomize on the action name alone re-fires it for a
// related_rule_ids-only enrich call. Comparing the actual before/after notes
// text is the only classification that's correct for every branch uniformly.
func RecordExecutionResult(ctx context.Context, store StoreIface, params CreateOutcomeParams) (Outcome, LifecycleAction, string, error) {
	latest, err := store.GetLatestForEntity(ctx, params.WorkspaceID, params.EntityType, params.EntityID)
	if errors.Is(err, ErrNotFound) {
		o, cErr := store.CreateOutcome(ctx, params)
		if cErr != nil {
			return Outcome{}, "", "", fmt.Errorf("recording execution result: %w", cErr)
		}
		return o, ActionCreated, "", nil
	}
	if err != nil {
		return Outcome{}, "", "", fmt.Errorf("recording execution result: resolving latest outcome: %w", err)
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
		// notes="...")). The MCP instructions in server.go tell callers to
		// "upgrade" a complete_task-seeded draft via evaluate_outcome/
		// record_outcome once real context is known — they don't pin this
		// exact call shape, but it's the natural way to carry that upgrade
		// out. That case must still reach FinalizeDraft (now append-only, so
		// it's safe — see store.go's FinalizeDraft) so the content actually
		// gets written — see ActionDraftEnriched.
		if params.Result == resultUnknown && !hasNewContent(params) {
			return latest, ActionDraftPreserved, latest.Notes, nil
		}
		// PR #152 round 3 Major: hasNewContent only established that params
		// carries SOME content — it says nothing about whether that content
		// is NEW relative to what's already stored. Because an enrich call
		// (params.Result == resultUnknown) never flips latest.Result away
		// from "unknown", a retried call lands back in THIS branch (not the
		// terminal-branch isIdempotentReplay below, which is unreachable for
		// draft rows). Without this check, a byte-identical retry of an
		// already-applied enrich call would re-invoke FinalizeDraft and
		// re-report ActionDraftEnriched, re-firing tools_outcome.go's
		// SetOutcomeLink/atomize side effects for content already recorded.
		// Scoped to the enrich path only: a terminal params.Result reaching
		// this branch is always the FIRST finalization of this draft (once
		// finalized, latest.Result is no longer "unknown" and any further
		// call goes through the terminal branch's isIdempotentReplay
		// instead), so there is no equivalent duplicate-finalize case here.
		if params.Result == resultUnknown && isDraftIdempotentReplay(latest, params) {
			return latest, ActionReplayedIdempotent, latest.Notes, nil
		}
		// Everything below writes. The only thing the result value changes is
		// which action we report: a terminal result finalizes the draft, an
		// "unknown" result that got this far carries genuinely new content
		// and merely enriches it (result stays non-terminal). Both go
		// through the same merge-only FinalizeDraft.
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
			return Outcome{}, "", "", fmt.Errorf("recording execution result: finalizing draft: %w", ferr)
		}
		return finalized, action, latest.Notes, nil
	}

	if isIdempotentReplay(latest, params) {
		return latest, ActionReplayedIdempotent, latest.Notes, nil
	}

	supersedeParams := params
	latestID := latest.ID
	supersedeParams.SupersedesID = &latestID
	o, err := store.CreateOutcome(ctx, supersedeParams)
	if err != nil {
		return Outcome{}, "", "", fmt.Errorf("recording execution result: superseding: %w", err)
	}
	// PR #152 round 6 Major M-R6-1: return "" here, NOT latest.Notes. This is
	// a brand-new row (a fresh outcome_id, never atomized before) — exactly
	// like ActionCreated below the ErrNotFound branch, not a merge into an
	// existing row. Returning latest.Notes (the PRIOR row's Notes) let a
	// supersede call whose own Notes happened to be byte-identical to the
	// row it supersedes (e.g. "same summary, different verdict":
	// result="success" then result="regressed" with unchanged notes) compare
	// equal at tools_outcome.go's `o.Notes != previousNotes` gate and
	// silently skip atomize for content that had never been atomized under
	// THIS row's outcome_id. See ActionSuperseded's doc comment above.
	return o, ActionSuperseded, "", nil
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

// isDraftIdempotentReplay reports whether params, once merged into latest
// via FinalizeDraft's APPEND-ONLY semantics (store.go / storage/sqlite/
// outcome.go, migration 000075), would leave every written column exactly
// as it already is — i.e. this call carries zero net-new information over
// what's already stored. Called only from RecordExecutionResult's draft
// branch, AFTER hasNewContent has already established params carries SOME
// content (hasNewContent's "no content at all" case is handled separately
// by ActionDraftPreserved) — this function answers the narrower question of
// whether that content is actually NEW.
//
// Deliberately NOT a reuse of isIdempotentReplay: that function only
// compares Result/Notes/Metrics, which is correct for the terminal branch
// (a full-row CreateOutcome/supersede compare) but is UNDER-inclusive here —
// FinalizeDraft also conditionally writes RelatedRuleIDs and WorkSessionID,
// and a call that repeats identical Notes while introducing a new
// WorkSessionID or RelatedRuleIDs set must NOT be classified as a replay
// (that would silently drop the new link/rules, reintroducing PR #152
// finding M-2a for those two fields specifically — see the acceptance
// criteria on the round-3 Major fix this function closes, and the
// TestRecordExecutionResult_DraftEnrichRetry_New*StillWrites tests that pin
// it).
//
// Each field mirrors FinalizeDraft's own append-only merge rule exactly —
// "new" here means "would actually change the stored value", NOT "differs
// byte-for-byte from what's stored" (that would be over-cautious under
// append semantics: e.g. resupplying an already-occupied metrics key with a
// DIFFERENT value is not new information, because FinalizeDraft will
// silently keep the existing value regardless):
//   - Notes: no new info if incoming is empty, OR if incoming already
//     matches ANY "\n\n"-delimited block of existing — not just the
//     trailing one (PR #152 round 4 Major: an interleaved resend that
//     repeats an earlier block must also be recognized as a replay, see
//     notesHasNewContent's slices.Contains check), OR incoming equals
//     existing outright (the direct-write case when existing was
//     originally empty).
//   - Metrics: no new info if incoming is empty, OR if every key in
//     incoming is already present in existing — the VALUE doesn't matter,
//     only key membership, because FinalizeDraft never overwrites an
//     existing key's value.
//   - RelatedRuleIDs: no new info if incoming is empty, OR if every ID in
//     incoming is already present in existing — order-insensitive, because
//     FinalizeDraft now unions (not replaces), so a differently-ordered
//     resubmission of the same ID set produces an IDENTICAL stored array,
//     unlike the old whole-array-replace semantics this function used to
//     mirror.
//   - WorkSessionID: no new info if incoming is nil, OR if existing is
//     already non-nil (set-once: once a session is linked, no later call —
//     regardless of what ID it supplies — can change it, so supplying any
//     ID against an already-set draft is never "new" even if the values
//     differ).
func isDraftIdempotentReplay(latest Outcome, params CreateOutcomeParams) bool {
	return !notesHasNewContent(latest.Notes, params.Notes) &&
		!metricsHasNewKey(latest.Metrics, params.Metrics) &&
		!relatedRuleIDsHasNew(latest.RelatedRuleIDs, params.RelatedRuleIDs) &&
		!workSessionIDHasNew(latest.WorkSessionID, params.WorkSessionID)
}

// notesHasNewContent reports whether appending incoming to existing (per
// FinalizeDraft's appendNotes rule: direct write when existing is empty,
// "\n\n"-joined append otherwise) would actually change the stored value.
//
// Checks every "\n\n"-delimited segment of existing, not just the trailing
// one (PR #152 round 4 Major): the old HasSuffix-only check only recognized
// a byte-identical retry of the MOST RECENT enrich call as a replay. Any
// earlier segment resubmitted out of order — e.g. an interleaved AAA, BBB,
// AAA, BBB, AAA resend sequence — passed this check as "new" every time,
// because it was never the trailing segment, so each resend appended a
// fresh duplicate AND re-fired ActionDraftEnriched's SetOutcomeLink/atomize
// side effects. slices.Contains against the full split gives idempotency
// regardless of resend order, matching what FinalizeDraft's appendNotes
// would actually change: appending a segment ALREADY present anywhere in
// existing produces string-different-but-not-semantically-new content.
//
// Correctness precondition (PR #152 round 5 Minor s-R5-3): splitting on the
// literal "\n\n" separator is only a safe segment boundary because every
// current caller of RecordExecutionResult passes params.Notes through
// sanitize.Notes (internal/sanitize/notes.go) first, which strips every
// control character below 0x20 EXCEPT '\t' — including '\n' itself. A
// caller that supplied incoming containing a raw "\n\n" WITHOUT going
// through that sanitizer could smuggle a payload that collides with an
// existing segment boundary and gets misclassified as "not new". Both
// current callers (internal/mcp/tools_outcome.go's parseRecordOutcomeArgs,
// tools_worksession.go's autoCreateOutcomeOnFailure) sanitize before this
// function ever sees the value — this comment exists so a FUTURE caller of
// RecordExecutionResult that skips sanitize.Notes is not silently exposed
// to that gap.
func notesHasNewContent(existing, incoming string) bool {
	if incoming == "" {
		return false
	}
	if existing == "" {
		return true
	}
	if existing == incoming {
		return false
	}
	return !slices.Contains(strings.Split(existing, "\n\n"), incoming)
}

// metricsHasNewKey reports whether incoming (a JSON object) contains at
// least one top-level key not already present in existing — the
// append-semantics definition of "new information" for the metrics field
// (FinalizeDraft only ever ADDS keys the draft doesn't already have;
// existing key values are never overwritten regardless of what incoming
// supplies for them, so a differing value for an already-present key is NOT
// new information). Malformed incoming JSON is conservatively treated as
// new content, so the call falls through to FinalizeDraft (and its own
// error handling) rather than this comparison silently swallowing it.
func metricsHasNewKey(existing, incoming []byte) bool {
	if len(incoming) == 0 {
		return false
	}
	var incomingMap map[string]json.RawMessage
	if err := json.Unmarshal(incoming, &incomingMap); err != nil {
		return true
	}
	var existingMap map[string]json.RawMessage
	if len(existing) > 0 {
		// Best-effort: a malformed existing value (shouldn't happen — only
		// ever written by this same merge logic) falls through to a nil
		// existingMap, which treats every incoming key as new — safe
		// (over-writes, never under-writes) rather than panicking or
		// propagating an error from a read-only comparison helper.
		_ = json.Unmarshal(existing, &existingMap)
	}
	for k := range incomingMap {
		if _, ok := existingMap[k]; !ok {
			return true
		}
	}
	return false
}

// relatedRuleIDsHasNew reports whether incoming contains at least one UUID
// not already present in existing — order-insensitive, since FinalizeDraft
// now unions (migration 000075) rather than whole-array-replacing.
func relatedRuleIDsHasNew(existing, incoming []uuid.UUID) bool {
	if len(incoming) == 0 {
		return false
	}
	existingSet := make(map[uuid.UUID]bool, len(existing))
	for _, id := range existing {
		existingSet[id] = true
	}
	for _, id := range incoming {
		if !existingSet[id] {
			return true
		}
	}
	return false
}

// workSessionIDHasNew reports whether incoming would actually change the
// stored work_session_id — only possible when existing is still nil
// (set-once: FinalizeDraft's `COALESCE(work_session_id, new)` / "only write
// when existing is NULL" rule means a non-nil existing value can never be
// changed by ANY later call, regardless of what ID that call supplies).
func workSessionIDHasNew(existing, incoming *uuid.UUID) bool {
	return incoming != nil && existing == nil
}
