package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// maxRelatedRuleIDs caps the number of behavior rule IDs that may be supplied
// per outcome. The Wednesday governance job does O(N*M) ApplyOutcome calls
// where N=outcomes and M=related_rule_ids; an unbounded array turns this into
// a quadratic CPU/DB amplification attack vector.
const maxRelatedRuleIDs = 20

// parseRelatedRuleIDs parses an optional JSON array of UUID strings from the
// MCP tool arguments. Returns an empty slice when the argument is absent or
// empty; returns an error when any element is not a valid UUID or when the
// array exceeds maxRelatedRuleIDs (20).
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
	return out, nil
}

const maxOutcomeLimit = 100

// registerOutcomeTools registers the 4 outcome/evaluation MCP tools.
func (s *Server) registerOutcomeTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"record_outcome",
		mcp.WithDescription(
			"Record the result of an executed task, decision, sprint, or project. "+
				"Closes the Action→Result loop by capturing what actually happened. "+
				"entity_type must be one of: task, decision, sprint, project. "+
				"result must be one of: success, failure, partial, unknown, regressed. "+
				"Optionally link to behavior rules via related_rule_ids so the behavior "+
				"governance scheduler can update their confidence scores.",
		),
		mcp.WithString("entity_type",
			mcp.Description("Type of entity: task | decision | sprint | project"),
			mcp.Required()),
		mcp.WithString("entity_id",
			mcp.Description("UUID of the entity (task_id, decision_id, etc.)"),
			mcp.Required()),
		mcp.WithString("result",
			mcp.Description("Execution result: success | failure | partial | unknown | regressed"),
			mcp.Required()),
		mcp.WithString("notes",
			mcp.Description("Free-text notes about the outcome (max 500 runes)")),
		mcp.WithString("metrics_json",
			mcp.Description("Optional JSON object with numeric metrics, e.g. {\"duration_ms\": 1200}")),
		mcp.WithString("related_rule_ids",
			mcp.Description("Optional JSON array of behavior rule UUIDs to link, e.g. [\"uuid1\",\"uuid2\"]. Absent or empty = no linked rules.")),
		mcp.WithString("session_id",
			mcp.Description("Optional work session UUID this outcome was recorded from — "+
				"best-effort linked back onto the session via SetOutcomeLink.")),
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
		if !json.Valid([]byte(raw)) {
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

// handleRecordOutcome validates input and creates a new outcome.
func (s *Server) handleRecordOutcome(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	in, errResult := parseRecordOutcomeArgs(args)
	if errResult != nil {
		return errResult, nil
	}

	wsID := s.workspaceUUID()
	o, action, err := outcome.RecordExecutionResult(ctx, s.outcome, outcome.CreateOutcomeParams{
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
		return mcp.NewToolResultError(fmt.Sprintf("recording outcome: %v", err)), nil
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

	// M9: atomize the outcome notes in the background when non-empty.
	if o.Notes != "" {
		s.launchAtomize("outcomes", o.ID, o.Notes)
	}
	return jsonText(o)
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
