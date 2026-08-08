package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// maxRelatedRuleIDs caps the number of behavior rule IDs that may be supplied
// in a SINGLE record_outcome call — this bounds parse/validation cost for
// one request, NOT the cumulative size an outcome's related_rule_ids array
// can grow to across repeated enrich calls (that's a separate guarantee,
// outcome.MaxRelatedRuleIDsTotal, enforced post-merge inside both backends'
// FinalizeDraft — see its doc comment for PR #152 round 5 Major M-R5-1). The
// Wednesday governance job does O(N*M) ApplyOutcome calls where N=outcomes
// and M=related_rule_ids; an unbounded array turns this into a quadratic
// CPU/DB amplification attack vector — this constant and
// outcome.MaxRelatedRuleIDsTotal together close two DIFFERENT ways an
// attacker could reach that amplification (one huge call vs many small
// calls that accumulate).
const maxRelatedRuleIDs = 20

// parseRelatedRuleIDs parses an optional JSON array of UUID strings from the
// MCP tool arguments. Returns an empty slice when the argument is absent or
// empty; returns an error when any element is not a valid UUID or when the
// array exceeds maxRelatedRuleIDs (20). The max-length check is against the
// RAW parsed count (before dedup), so repeating one ID cannot be used to
// smuggle more than maxRelatedRuleIDs distinct entries past the cap.
//
// De-duped before returning (PR #152 round 4 Major, finding F6): neither
// backend's CreateOutcome deduped related_rule_ids on the FRESH-row path
// (ActionCreated — no prior draft/outcome exists yet for the entity, so
// FinalizeDraft's own dedup — outcome.DedupeUUIDsPreserveOrder — never
// runs). A caller-supplied duplicate on that path would have been stored
// verbatim and, per maxRelatedRuleIDs's own doc comment, double the
// Wednesday governance job's O(N*M) ApplyOutcome calls for that rule.
// outcome.DedupeUUIDsPreserveOrder is reused (not reimplemented) here
// specifically so the MCP-boundary dedup and the store-layer dedup never
// drift into two different order semantics.
func parseRelatedRuleIDs(raw string) ([]uuid.UUID, error) {
	if raw == "" || raw == "[]" {
		return []uuid.UUID{}, nil
	}
	var strs []string
	if err := json.Unmarshal([]byte(raw), &strs); err != nil {
		return nil, fmt.Errorf("related_rule_ids must be a JSON array of UUID strings: %v", err)
	}
	if len(strs) > maxRelatedRuleIDs {
		return nil, fmt.Errorf(
			"related_rule_ids has %d entries; maximum is %d",
			len(strs), maxRelatedRuleIDs,
		)
	}
	out := make([]uuid.UUID, 0, len(strs))
	for _, s := range strs {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid UUID in related_rule_ids %q: %v", s, err)
		}
		out = append(out, id)
	}
	return outcome.DedupeUUIDsPreserveOrder(out), nil
}

const maxOutcomeLimit = 100

// registerOutcomeTools registers the 4 outcome/evaluation MCP tools.
func (s *Server) registerOutcomeTools(ms *server.MCPServer) {
	// Description budget: record_outcome is in the always-visible core tool
	// set (toolgroups.go), so every byte here is paid by every client on every
	// tools/list. The accumulation-cap / truncation-flag semantics that used
	// to live in these param descriptions now live in mcpProtocolAppendix
	// ("Per-tool detail", tools_onboarding.go), served on demand by
	// initial_instructions. Keep the imperative MUST/NEVER wording here; move
	// explanation and examples there.
	ms.AddTool(mcp.NewTool(
		"record_outcome",
		mcp.WithDescription(
			"MUST call to close the Action→Result loop after an executed task, decision, "+
				"sprint, or project. Captures what actually happened. See initial_instructions "+
				"(Per-tool detail) for accumulation caps and truncation flags.",
		),
		mcp.WithString("entity_type",
			mcp.Description("task | decision | sprint | project"),
			mcp.Required()),
		mcp.WithString("entity_id",
			mcp.Description("UUID of the entity"),
			mcp.Required()),
		mcp.WithString("result",
			mcp.Description("success | failure | partial | unknown | regressed"),
			mcp.Required()),
		mcp.WithString("notes",
			mcp.Description(fmt.Sprintf(
				"Notes, max 500 runes per call; accumulate to %d runes per draft, "+
					"then notes_truncated=true.", outcome.MaxNotesTotalRunes,
			))),
		mcp.WithString("metrics_json",
			mcp.Description("JSON object of numeric metrics")),
		mcp.WithString("related_rule_ids",
			mcp.Description(fmt.Sprintf(
				"JSON array of behavior rule UUIDs, max %d per call; accumulate to %d "+
					"per draft, then related_rule_ids_truncated=true.",
				maxRelatedRuleIDs, outcome.MaxRelatedRuleIDsTotal,
			))),
		mcp.WithString("session_id",
			mcp.Description("Work session UUID to link this outcome back to")),
	), s.handleRecordOutcome)

	ms.AddTool(mcp.NewTool(
		"evaluate_outcome",
		mcp.WithDescription(
			"Attach structured analysis to an existing outcome. "+
				"Captures root-cause analysis, lessons learned, and improvement suggestions "+
				"to feed the reflection loop.",
		),
		mcp.WithString("outcome_id",
			mcp.Description("UUID of the outcome to evaluate"),
			mcp.Required()),
		mcp.WithString("analysis",
			mcp.Description("Root-cause analysis and key findings (max 500 runes)"),
			mcp.Required()),
		mcp.WithString("lessons_json",
			mcp.Description("JSON array of lesson strings, e.g. [\"clarify requirements first\"]")),
		mcp.WithString("suggestions_json",
			mcp.Description("JSON array of improvement suggestion strings")),
	), s.handleEvaluateOutcome)

	ms.AddTool(mcp.NewTool(
		"list_recent_outcomes",
		mcp.WithDescription(
			"List recent outcomes ordered by created_at DESC. "+
				"Optionally filter by entity_type. "+
				"Use to review recent execution results.",
		),
		mcp.WithString("entity_type",
			mcp.Description("Filter by entity type: task | decision | sprint | project (empty = all)")),
		mcp.WithNumber("limit",
			mcp.Description(fmt.Sprintf("Max results (default 20, max %d)", maxOutcomeLimit))),
	), s.handleListRecentOutcomes)

	ms.AddTool(mcp.NewTool(
		"find_failed_patterns",
		mcp.WithDescription(
			"Retrieve recent failure and regression outcomes together with their evaluations. "+
				"Use to identify recurring failure patterns and improvement opportunities.",
		),
		mcp.WithNumber("limit",
			mcp.Description(fmt.Sprintf("Max failed outcomes to return (default 10, max %d)", maxOutcomeLimit))),
	), s.handleFindFailedPatterns)
}

// recordOutcomeInput is the parsed+validated argument set for
// handleRecordOutcome, produced by parseRecordOutcomeArgs.
type recordOutcomeInput struct {
	entityType     string
	entityID       uuid.UUID
	result         string
	notes          string
	metricsJSON    []byte
	relatedRuleIDs []uuid.UUID
	sessionID      *uuid.UUID
}

// parseRecordOutcomeArgs validates and parses the record_outcome tool
// arguments. On validation failure it returns a non-nil errResult that the
// caller must return directly (with a nil error); errResult is nil on
// success. Split out of handleRecordOutcome to keep cyclomatic complexity
// under the gocyclo threshold — this function is pure input validation, the
// caller retains all side-effecting logic.
func parseRecordOutcomeArgs(args map[string]any) (recordOutcomeInput, *mcp.CallToolResult) {
	var in recordOutcomeInput

	in.entityType = stringArg(args, "entity_type")
	if !outcome.AllowedEntityTypes[in.entityType] {
		return in, mcp.NewToolResultError(
			fmt.Sprintf("invalid entity_type %q: must be one of task, decision, sprint, project", in.entityType),
		)
	}

	rawEntityID := stringArg(args, "entity_id")
	entityID, err := uuid.Parse(rawEntityID)
	if err != nil {
		return in, mcp.NewToolResultError("invalid entity_id UUID")
	}
	in.entityID = entityID

	in.result = stringArg(args, "result")
	if !outcome.AllowedResults[in.result] {
		return in, mcp.NewToolResultError(
			fmt.Sprintf("invalid result %q: must be one of success, failure, partial, unknown, regressed", in.result),
		)
	}

	in.notes = sanitize.Notes(stringArg(args, "notes"))
	if err := sanitize.ValidateNoTagNoise(in.notes); err != nil {
		return in, mcp.NewToolResultError(fmt.Sprintf("invalid notes: %v", err))
	}

	if raw := stringArg(args, "metrics_json"); raw != "" {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
			// json.Valid alone accepted ANY valid JSON value — arrays, numbers,
			// strings, null — despite the error message already promising "must
			// be a valid JSON object" (PR #152 round 4 Major). PG's jsonb `||`
			// merge in FinalizeDraft only behaves correctly when both operands
			// are objects: a non-object left operand (e.g. `[1]`) silently
			// concatenates instead of merging, letting a repeated
			// record_outcome(metrics_json="[1]") grow the column without bound
			// and, via metricsHasNewKey's fail-open-to-true default on
			// unmarshal-into-map failure, re-fire draft_enriched (SetOutcomeLink
			// / atomize) on every single retry. Unmarshalling into
			// map[string]json.RawMessage rejects arrays/scalars/null outright;
			// `obj == nil` additionally rejects the literal `null` (which
			// unmarshals into a nil map with no error).
			return in, mcp.NewToolResultError("metrics_json must be a valid JSON object")
		}
		in.metricsJSON = []byte(raw)
	}

	relatedRuleIDs, rridErr := parseRelatedRuleIDs(stringArg(args, "related_rule_ids"))
	if rridErr != nil {
		return in, mcp.NewToolResultError(rridErr.Error())
	}
	in.relatedRuleIDs = relatedRuleIDs

	// session_id is optional. When present it MUST be a well-formed UUID —
	// validate the format up front so malformed input is rejected outright,
	// even though an unknown-but-valid UUID is tolerated below (no-FK design;
	// backend-security-design.md §6 migration comment on work_session_id).
	if raw := stringArg(args, "session_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return in, mcp.NewToolResultError("invalid session_id UUID")
		}
		in.sessionID = &id
	}

	return in, nil
}

// recordOutcomeResponse wraps outcome.Outcome with truncation signals for
// the record_outcome MCP response — added PR #152 round 7 Major m-R7-1.
// Before this, once a draft's cumulative Notes/RelatedRuleIDs hit their
// respective caps (outcome.MaxNotesTotalRunes / outcome.MaxRelatedRuleIDsTotal,
// enforced inside FinalizeDraft), a call whose content got silently dropped
// still returned IsError=false with a complete-looking Outcome JSON body —
// the caller (an LLM) had no way to tell its content never landed. The
// struct embeds outcome.Outcome so its fields are promoted to the top level
// of the JSON body (identical shape to the old `jsonText(o)` response); the
// two *Truncated fields are `omitempty` so the common, non-truncated case
// produces byte-identical output to before this change.
//
// This is deliberately NOT surfaced as an error: the write itself succeeded
// (the row WAS updated) — only part of its content didn't make it in. See
// handleRecordOutcome's dispatch note: a partial write is still a write.
type recordOutcomeResponse struct {
	outcome.Outcome
	// NotesTruncated is true when this call's notes text was NOT fully
	// appended onto the stored Notes field because the cumulative cap
	// (outcome.MaxNotesTotalRunes) was already at or past capacity.
	NotesTruncated bool `json:"notes_truncated,omitempty"`
	// RelatedRuleIDsTruncated is true when one or more of this call's
	// related_rule_ids were NOT added to the stored array because the
	// cumulative cap (outcome.MaxRelatedRuleIDsTotal) was already at or past
	// capacity.
	RelatedRuleIDsTruncated bool `json:"related_rule_ids_truncated,omitempty"`
	// TruncationNote is a human-readable explanation of what got dropped,
	// populated only when either flag above is true — callers are LLMs, they
	// read prose, not just booleans.
	TruncationNote string `json:"truncation_note,omitempty"`
}

// buildTruncationNote returns the human-readable explanation for
// recordOutcomeResponse.TruncationNote. Only called when at least one of
// notesTruncated/idsTruncated is true.
func buildTruncationNote(notesTruncated, idsTruncated bool) string {
	switch {
	case notesTruncated && idsTruncated:
		return fmt.Sprintf(
			"This call's notes and related_rule_ids were only partially recorded: the outcome's "+
				"cumulative notes (%d runes) and related_rule_ids (%d entries) caps were already "+
				"reached, so the new content this call supplied was not written. Existing content "+
				"was NOT lost — only what THIS call tried to add.",
			outcome.MaxNotesTotalRunes, outcome.MaxRelatedRuleIDsTotal,
		)
	case notesTruncated:
		return fmt.Sprintf(
			"This call's notes were NOT fully recorded: the outcome's cumulative notes cap (%d runes) "+
				"was already reached, so some or all of the new text this call supplied was not "+
				"written. Existing notes were NOT lost — only what THIS call tried to add.",
			outcome.MaxNotesTotalRunes,
		)
	default:
		return fmt.Sprintf(
			"This call's related_rule_ids were NOT fully recorded: the outcome's cumulative "+
				"related_rule_ids cap (%d entries) was already reached, so one or more of the new "+
				"IDs this call supplied were not added. Existing links were NOT lost — only what "+
				"THIS call tried to add.",
			outcome.MaxRelatedRuleIDsTotal,
		)
	}
}

// handleRecordOutcome validates input and creates a new outcome.
func (s *Server) handleRecordOutcome(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	in, errResult := parseRecordOutcomeArgs(args)
	if errResult != nil {
		return errResult, nil
	}

	wsID := s.workspaceUUID()
	o, action, previousNotes, err := outcome.RecordExecutionResult(ctx, s.outcome, outcome.CreateOutcomeParams{
		WorkspaceID:    wsID,
		EntityType:     in.entityType,
		EntityID:       in.entityID,
		Result:         in.result,
		Notes:          in.notes,
		Metrics:        in.metricsJSON,
		RelatedRuleIDs: in.relatedRuleIDs,
		WorkSessionID:  in.sessionID,
	})
	if err != nil {
		// Never surface the raw store error to the MCP caller: it can carry
		// driver/schema internals (e.g. a UNIQUE constraint violation names
		// idx_outcomes_one_open_draft literally) — security-review-reproduced
		// leak. Full detail stays server-side via slog so the failure is still
		// diagnosable from logs; the caller gets a generic message. Mirrors
		// the sanitize-then-log pattern in tools_arch.go's store-error paths.
		slog.Error("record_outcome: store error",
			"entity_type", in.entityType, "entity_id", in.entityID, "err", err)
		return mcp.NewToolResultError("failed to record outcome"), nil
	}

	// Idempotent replay (decision 80c1e8ae): the entity's latest outcome is
	// byte-identical to this call — no row was written, so the follow-on
	// side effects below (SetOutcomeLink, atomize) must not re-fire either,
	// or a retried record_outcome call would silently duplicate atoms for
	// notes text that was already atomized on the first call.
	//
	// Draft preserved (PR #152 finding M-1): result="unknown" against an
	// existing draft with NO new content is also a no-write no-op (see
	// outcome.ActionDraftPreserved doc comment) — same reasoning applies:
	// nothing was recorded, so no session link or atomize side effect should
	// fire either.
	//
	// Deliberately NOT in this skip list: outcome.ActionDraftEnriched. That
	// action (PR #152 finding M-2a) is also result="unknown" against an
	// existing draft, but DOES carry new content that FinalizeDraft merged
	// into the row — a real write occurred, so SetOutcomeLink and atomize
	// below must fire exactly as they would for ActionFinalizedDraft or
	// ActionCreated. Adding it here would resurrect M-2a (silently dropping
	// the session link / atomization for a legitimate content-bearing write).
	if action == outcome.ActionReplayedIdempotent || action == outcome.ActionDraftPreserved {
		return jsonText(o)
	}

	// Best-effort: also set work_sessions.outcome_id so the link is
	// bidirectional. A stale/unknown session_id (worksession.ErrNotFound) is
	// tolerated per the no-FK design — record_outcome never fails over this.
	if in.sessionID != nil && s.workSession != nil {
		if linkErr := s.workSession.SetOutcomeLink(ctx, *in.sessionID, o.ID); linkErr != nil {
			slog.Warn("record_outcome: SetOutcomeLink failed (non-fatal)",
				"outcome_id", o.ID, "session_id", *in.sessionID, "err", linkErr)
		}
	}

	// M9: atomize the outcome notes in the background when non-empty AND the
	// notes text actually changed by this call (PR #152 round 5 Major
	// M-R5-1 guarantee B). Before this guard, ANY write that reached this
	// point (including ActionDraftEnriched calls carrying ONLY new
	// related_rule_ids, notes untouched) re-atomized o.Notes unconditionally
	// — o.Notes is the FULL accumulated notes text, not just what this call
	// wrote, so a caller retrying record_outcome with fresh related_rule_ids
	// and no notes change triggered a fresh paid Haiku atomize call over
	// text already atomized on a prior call, and AddAtom does not dedupe
	// (internal/mcp/tools_atom.go), so each retry also grew the knowledge
	// base with duplicate atoms. previousNotes (RecordExecutionResult's new
	// return value) is the entity's notes value immediately before this
	// call ran — comparing it against o.Notes is the only classification
	// that's correct across every LifecycleAction uniformly (see
	// RecordExecutionResult's doc comment for why gating on the action name
	// itself is wrong).
	//
	// PR #152 round 6 Major M-R6-2a: this guard alone only decided WHETHER
	// to atomize — it still atomized the FULL o.Notes (the entire
	// accumulated field), not just what THIS call added. Against a draft
	// enriched N times, call k re-sent the ENTIRE text accumulated through
	// call k (not just call k's own new segment) to the atomizer, making
	// total characters HANDED TO launchAtomize across the draft's lifetime
	// grow O(N^2) with the number of enrich calls — reproduced empirically
	// as 30 enrich calls / a 15,058-rune final notes field / 233,370
	// CUMULATIVE runes handed to launchAtomize, ~15.5x the final document
	// size. Correction (PR #152 round 7 Minor s-R7-3): that 233,370 figure
	// is the volume passed as ai.Atomizer.Atomize's INPUT, not the volume
	// that actually reached the paid Haiku model — Atomize caps its own
	// input to 4,000 runes (internal/ai/atomizer.go's maxAtomizeInputChars)
	// before calling the Anthropic API. The real defect this fixed was
	// worse than extra API spend: past 4,000 accumulated runes, resending
	// the FULL text every call meant Atomize's own truncation always cut
	// off the newest (trailing) segment, so it was NEVER atomized — silent,
	// permanent knowledge loss with no signal. notesDelta extracts only the
	// segment THIS call actually added, relative to previousNotes (at most
	// sanitize.Notes's 500-rune per-call cap, always well inside Atomize's
	// 4,000-rune window), so atomize cost scales with what was written, not
	// the field's accumulated size — and, as a direct consequence, the
	// newest segment can no longer be truncated away by Atomize's own cap.
	if o.Notes != "" && o.Notes != previousNotes {
		if delta := notesDelta(o.Notes, previousNotes); delta != "" {
			s.launchAtomize("outcomes", o.ID, delta)
		}
	}

	// PR #152 round 7 Major m-R7-1: signal to the caller when its OWN
	// supplied content didn't fully land, because FinalizeDraft's cumulative
	// caps (outcome.MaxNotesTotalRunes / outcome.MaxRelatedRuleIDsTotal) were
	// already at or past capacity. outcome.NotesTruncated / RelatedRuleIDsTruncated
	// compare in.notes/in.relatedRuleIDs (what THIS call asked to write)
	// against o.Notes/o.RelatedRuleIDs (what actually landed) — see their doc
	// comments (internal/outcome/store.go) for why that comparison is exact.
	// Deliberately computed here, AFTER the early idempotent-replay/
	// draft-preserved return above (line ~292): a replay that resubmits an
	// EARLIER (non-trailing) notes segment is legitimately not a suffix of
	// o.Notes even though nothing was truncated — see
	// lifecycle.go's notesHasNewContent doc comment on interleaved resends —
	// so running this check against a no-write no-op response would produce
	// a false positive.
	resp := recordOutcomeResponse{Outcome: o}
	resp.NotesTruncated = outcome.NotesTruncated(o.Notes, in.notes)
	resp.RelatedRuleIDsTruncated = outcome.RelatedRuleIDsTruncated(o.RelatedRuleIDs, in.relatedRuleIDs)
	if resp.NotesTruncated || resp.RelatedRuleIDsTruncated {
		resp.TruncationNote = buildTruncationNote(resp.NotesTruncated, resp.RelatedRuleIDsTruncated)
	}
	return jsonText(resp)
}

// notesDelta returns the text THIS call actually added to Notes, relative to
// previousNotes — the entity's Notes value immediately before this call
// wrote anything (RecordExecutionResult's previousNotes return value, see
// its doc comment). launchAtomize must only ever receive the NEW segment,
// never the full accumulated o.Notes: see handleRecordOutcome's M-R6-2a
// comment for the O(N^2) amplification this closes.
//
// previousNotes == "" covers both a genuinely fresh row (ActionCreated,
// and — since PR #152 round 6 Major M-R6-1 — ActionSuperseded, see its own
// doc comment for why supersede is treated like a fresh row here) and the
// first enrich of a draft whose Notes started empty: in both cases the
// entirety of oNotes is new, so the full text is returned.
//
// Otherwise oNotes is expected to be exactly previousNotes + "\n\n" + <new
// segment> (FinalizeDraft's appendNotes rule, both backends) — UNLESS the
// cumulative notes cap (outcome.MaxNotesTotalRunes, M-R6-2 guarantee B)
// truncated the merge before the new segment could be told apart from
// previousNotes. That can only happen when previousNotes itself already
// sits at or past the cap (only reachable via data written before the cap
// existed — the cap is enforced on every write going forward, so
// previousNotes can never newly reach that state under normal operation),
// in which case oNotes ends up being a byte-for-byte prefix of previousNotes
// itself, and the outer `o.Notes != previousNotes` gate at the call site
// already prevents this function from being reached at all (oNotes would
// equal previousNotes exactly). The HasPrefix check below is defence in
// depth against that invariant ever being violated by a future change:
// rather than guess at a partial suffix, it returns "" — skipping atomize
// for that one call is a far smaller cost than risking a duplicate atomize
// of text already atomized on a prior call.
func notesDelta(oNotes, previousNotes string) string {
	if previousNotes == "" {
		return oNotes
	}
	sep := previousNotes + "\n\n"
	if !strings.HasPrefix(oNotes, sep) {
		return ""
	}
	return oNotes[len(sep):]
}

// handleEvaluateOutcome validates input, verifies the outcome exists, and
// creates an evaluation record.
func (s *Server) handleEvaluateOutcome(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	rawOutcomeID := stringArg(args, "outcome_id")
	outcomeID, err := uuid.Parse(rawOutcomeID)
	if err != nil {
		return mcp.NewToolResultError("invalid outcome_id UUID"), nil
	}

	analysis := sanitize.Notes(stringArg(args, "analysis"))
	if analysis == "" {
		return mcp.NewToolResultError("analysis is required"), nil
	}
	if err := sanitize.ValidateNoTagNoise(analysis); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid analysis: %v", err)), nil
	}

	wsID := s.workspaceUUID()

	// Verify outcome exists (code-layer referential integrity per red-line §9).
	if _, err := s.outcome.GetOutcomeByID(ctx, outcomeID, wsID); err != nil {
		if errors.Is(err, outcome.ErrNotFound) {
			return mcp.NewToolResultError(fmt.Sprintf("outcome %s not found", outcomeID)), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("looking up outcome: %v", err)), nil
	}

	lessonsJSON, err := validateJSONArrayArg(args, "lessons_json")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	suggestionsJSON, err := validateJSONArrayArg(args, "suggestions_json")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	eval, err := s.outcome.CreateEvaluation(ctx, outcome.CreateEvaluationParams{
		WorkspaceID:            wsID,
		OutcomeID:              outcomeID,
		Analysis:               analysis,
		Lessons:                lessonsJSON,
		ImprovementSuggestions: suggestionsJSON,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating evaluation: %v", err)), nil
	}
	return jsonText(eval)
}

// handleListRecentOutcomes returns outcomes ordered by created_at DESC.
func (s *Server) handleListRecentOutcomes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	entityType := stringArg(args, "entity_type")
	if entityType != "" && !outcome.AllowedEntityTypes[entityType] {
		return mcp.NewToolResultError(
			fmt.Sprintf("invalid entity_type %q: must be one of task, decision, sprint, project", entityType),
		), nil
	}

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 20
	}
	if limit > maxOutcomeLimit {
		limit = maxOutcomeLimit
	}

	wsID := s.workspaceUUID()
	outcomes, err := s.outcome.ListRecentOutcomes(ctx, wsID, entityType, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing outcomes: %v", err)), nil
	}
	if outcomes == nil {
		outcomes = []outcome.Outcome{}
	}
	return jsonText(outcomes)
}

// failedOutcomeWithEvals bundles a failed outcome with its evaluations for
// find_failed_patterns output.
type failedOutcomeWithEvals struct {
	Outcome     outcome.Outcome      `json:"outcome"`
	Evaluations []outcome.Evaluation `json:"evaluations"`
}

// handleFindFailedPatterns returns failure/regressed outcomes with their
// evaluations. N+1 query is intentional and acceptable at personal scale
// (max 100 outcomes × list evaluations per outcome).
func (s *Server) handleFindFailedPatterns(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 10
	}
	if limit > maxOutcomeLimit {
		limit = maxOutcomeLimit
	}

	wsID := s.workspaceUUID()
	failed, err := s.outcome.ListFailedOutcomes(ctx, wsID, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing failed outcomes: %v", err)), nil
	}

	// N+1: fetch evaluations per outcome. Acceptable at personal-OS scale
	// (single-tenant, small dataset, no join complexity needed).
	result := make([]failedOutcomeWithEvals, 0, len(failed))
	for _, o := range failed {
		evals, err := s.outcome.ListEvaluationsByOutcomeID(ctx, o.ID, wsID)
		if err != nil {
			evals = []outcome.Evaluation{} // degrade gracefully, don't abort
		}
		if evals == nil {
			evals = []outcome.Evaluation{}
		}
		result = append(result, failedOutcomeWithEvals{
			Outcome:     o,
			Evaluations: evals,
		})
	}
	return jsonText(result)
}

// validateJSONArrayArg extracts a named string arg and validates it is a JSON
// array. Empty string is allowed (returns nil, which the store interprets as []).
func validateJSONArrayArg(args map[string]any, key string) ([]byte, error) {
	raw := stringArg(args, key)
	if raw == "" {
		return nil, nil
	}
	if !isJSONArray(raw) {
		return nil, fmt.Errorf("%s must be a JSON array, e.g. [\"item1\", \"item2\"]", key)
	}
	return []byte(raw), nil
}

// isJSONArray returns true when s is a valid JSON array.
func isJSONArray(s string) bool {
	var v []any
	return json.Unmarshal([]byte(s), &v) == nil
}
