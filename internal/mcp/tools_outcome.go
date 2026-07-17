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

// handleRecordOutcome validates input and creates a new outcome.
func (s *Server) handleRecordOutcome(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	entityType := stringArg(args, "entity_type")
	if !outcome.AllowedEntityTypes[entityType] {
		return mcp.NewToolResultError(
			fmt.Sprintf("invalid entity_type %q: must be one of task, decision, sprint, project", entityType),
		), nil
	}

	rawEntityID := stringArg(args, "entity_id")
	entityID, err := uuid.Parse(rawEntityID)
	if err != nil {
		return mcp.NewToolResultError("invalid entity_id UUID"), nil
	}

	result := stringArg(args, "result")
	if !outcome.AllowedResults[result] {
		return mcp.NewToolResultError(
			fmt.Sprintf("invalid result %q: must be one of success, failure, partial, unknown, regressed", result),
		), nil
	}

	notes := sanitize.Notes(stringArg(args, "notes"))
	if err := sanitize.ValidateNoTagNoise(notes); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("invalid notes: %v", err)), nil
	}

	var metricsJSON []byte
	if raw := stringArg(args, "metrics_json"); raw != "" {
		if !json.Valid([]byte(raw)) {
			return mcp.NewToolResultError("metrics_json must be a valid JSON object"), nil
		}
		metricsJSON = []byte(raw)
	}

	relatedRuleIDs, rridErr := parseRelatedRuleIDs(stringArg(args, "related_rule_ids"))
	if rridErr != nil {
		return mcp.NewToolResultError(rridErr.Error()), nil
	}

	// session_id is optional. When present it MUST be a well-formed UUID —
	// validate the format up front so malformed input is rejected outright,
	// even though an unknown-but-valid UUID is tolerated below (no-FK design;
	// backend-security-design.md §6 migration comment on work_session_id).
	var sessionID *uuid.UUID
	if raw := stringArg(args, "session_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid session_id UUID"), nil
		}
		sessionID = &id
	}

	wsID := s.workspaceUUID()
	o, err := s.outcome.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID:    wsID,
		EntityType:     entityType,
		EntityID:       entityID,
		Result:         result,
		Notes:          notes,
		Metrics:        metricsJSON,
		RelatedRuleIDs: relatedRuleIDs,
		WorkSessionID:  sessionID,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("recording outcome: %v", err)), nil
	}

	// Best-effort: also set work_sessions.outcome_id so the link is
	// bidirectional. A stale/unknown session_id (worksession.ErrNotFound) is
	// tolerated per the no-FK design — record_outcome never fails over this.
	if sessionID != nil && s.workSession != nil {
		if linkErr := s.workSession.SetOutcomeLink(ctx, *sessionID, o.ID); linkErr != nil {
			slog.Warn("record_outcome: SetOutcomeLink failed (non-fatal)",
				"outcome_id", o.ID, "session_id", *sessionID, "err", linkErr)
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
