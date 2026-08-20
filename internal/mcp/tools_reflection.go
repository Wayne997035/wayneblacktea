package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/reflection"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// reflectionSummaryMaxRunes / reflectionJSONLeafMaxRunes are read-time
// bounds for reflection.Reflection's free-text fields, applied by
// wrapUntrustedReflection before jsonText — U13 (2026-08-20-mcp-surface-
// spec.md). reflectionSummaryMaxRunes mirrors buildReflectionCreateParams'
// write-time maxSummary cap; reflectionJSONLeafMaxRunes is a generous
// read-time-only backstop for the string leaves inside
// Insights/PatternsDetected/SuggestedActions (parseOptionalJSON only
// validates JSON-ness, not length), same rationale as decisionBodyMaxRunes
// (tools_decision.go).
const (
	reflectionSummaryMaxRunes  = 5000
	reflectionJSONLeafMaxRunes = 20000
)

// wrapUntrustedReflection returns a copy of r with every free-text field
// neutralised — U13. Mirrors wrapUntrustedTask/wrapUntrustedDecision's
// copy-not-mutate contract. nil in, nil out.
//
// generate_reflection is explicitly an AI-generated record — the dispatch
// for this file calls that out by name: "LLM 生成的文字要當成對抗性輸入
// ... 不因為「是我們自己的模型寫的」就豁免" (backend-security-design.md
// §2.1). Insights/PatternsDetected/SuggestedActions are json.RawMessage
// (caller-chosen shape, validated only for JSON-well-formedness by
// parseOptionalJSON), the same "JSON-inside-a-field" pattern flagged for
// tools_proposal.go's Payload / tools_watchdog.go's Detail in the U13
// inventory — Phase A's own note called these "structured JSON, not free
// text" and scoped this file's conversion to Summary only, but an
// "insight"/"pattern"/"suggested action" IS free text by construction (an
// assistant's own generated analysis), and a forged marker embedded in a
// source decision's rationale can survive verbatim into a generated
// insight string. Widening the conversion here rather than deferring it —
// see the dispatch report for the explicit divergence note.
func wrapUntrustedReflection(r *reflection.Reflection) *reflection.Reflection {
	if r == nil {
		return nil
	}
	out := *r
	out.Summary = clipSafe(r.Summary, reflectionSummaryMaxRunes)
	out.Insights = neutralizeJSONRawMessage(r.Insights)
	out.PatternsDetected = neutralizeJSONRawMessage(r.PatternsDetected)
	out.SuggestedActions = neutralizeJSONRawMessage(r.SuggestedActions)
	return &out
}

// wrapUntrustedReflections maps wrapUntrustedReflection over a slice of
// pointers, preserving order. Callers already normalise nil results to
// []*reflection.Reflection{} before calling this (list tools MUST return []
// not null).
func wrapUntrustedReflections(reflections []*reflection.Reflection) []*reflection.Reflection {
	out := make([]*reflection.Reflection, len(reflections))
	for i, r := range reflections {
		out[i] = wrapUntrustedReflection(r)
	}
	return out
}

// neutralizeJSONRawMessage walks an arbitrary already-marshaled JSON value
// and neutralizes every string leaf it finds (via neutralizeJSONValue),
// then remarshals. Malformed input (raw is not valid JSON, or
// unmarshal/remarshal fails) is returned unchanged rather than dropped —
// parseOptionalJSON already validates well-formedness at write time, so
// this should never trigger in practice, but failing open here avoids
// silently deleting a caller's data over a formatting edge case this
// function isn't responsible for fixing.
func neutralizeJSONRawMessage(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(neutralizeJSONValue(v))
	if err != nil {
		return raw
	}
	return out
}

// neutralizeJSONValue recursively applies clipSafe to every string leaf in
// an arbitrary decoded JSON value (map[string]any / []any pass through
// recursively; string leaves get clipSafe'd; every other scalar — number,
// bool, nil — is returned unchanged).
func neutralizeJSONValue(v any) any {
	switch val := v.(type) {
	case string:
		return clipSafe(val, reflectionJSONLeafMaxRunes)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = neutralizeJSONValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = neutralizeJSONValue(vv)
		}
		return out
	default:
		return val
	}
}

func (s *Server) registerReflectionTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("generate_reflection",
		mcp.WithDescription(
			"Persists an AI-generated reflection record. Does NOT call an LLM — the caller "+
				"provides the already-generated content. "+
				"Use to record daily/weekly reviews, task post-mortems, decision retrospectives, "+
				"and system-level pattern observations.",
		),
		mcp.WithString("type",
			mcp.Required(),
			mcp.Description("Reflection type. One of: daily, weekly, task, decision, proposal, knowledge, system.")),
		mcp.WithString("summary",
			mcp.Description("Reflection summary text. Max 5000 characters.")),
		mcp.WithString("insights",
			mcp.Description("JSON-encoded insights array or object. Must be valid JSON if provided.")),
		mcp.WithString("patterns_detected",
			mcp.Description("JSON-encoded detected patterns. Must be valid JSON if provided.")),
		mcp.WithString("suggested_actions",
			mcp.Description("JSON-encoded suggested actions array. Must be valid JSON if provided.")),
		mcp.WithNumber("confidence",
			mcp.Description("Confidence score between 0.0 and 1.0. Default: 0.0.")),
		mcp.WithString("related_entity_type",
			mcp.Description("Type of the related entity (e.g. 'task', 'decision'). Optional.")),
		mcp.WithString("related_entity_id",
			mcp.Description("UUID of the related entity. Optional.")),
	), s.handleGenerateReflection)

	ms.AddTool(mcp.NewTool("list_reflections",
		mcp.WithDescription("Lists persisted reflection records, ordered by creation time descending."),
		mcp.WithString("type",
			mcp.Description("Filter by reflection type. One of: daily, weekly, task, decision, proposal, knowledge, system.")),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of reflections to return. Default: 20.")),
	), s.handleListReflections)

	ms.AddTool(mcp.NewTool("get_latest_reflection",
		mcp.WithDescription("Returns the most recently created reflection of a given type, or null when none exists yet."),
		mcp.WithString("type",
			mcp.Required(),
			mcp.Description("Reflection type. One of: daily, weekly, task, decision, proposal, knowledge, system.")),
	), s.handleGetLatestReflection)

	ms.AddTool(mcp.NewTool("analyze_recent_patterns",
		mcp.WithDescription(
			"Returns recent reflection records that contain detected patterns. "+
				"Useful for surfacing recurring themes across the last N days.",
		),
		mcp.WithNumber("days",
			mcp.Description("Look-back window in days. Default: 7.")),
	), s.handleAnalyzeRecentPatterns)
}

// handleGenerateReflection persists a reflection record with the caller-supplied content.
func (s *Server) handleGenerateReflection(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	params, errResult := buildReflectionCreateParams(s.workspaceUUID(), args)
	if errResult != nil {
		return errResult, nil
	}

	r, err := s.reflection.Create(ctx, *params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating reflection: %v", err)), nil
	}
	// M9: atomize the reflection summary in the background when non-empty.
	if r.Summary != "" {
		s.launchAtomize("reflections", r.ID, r.Summary)
	}
	return jsonText(wrapUntrustedReflection(r))
}

// buildReflectionCreateParams validates and constructs a CreateParams from MCP
// tool arguments. Returns a non-nil *mcp.CallToolResult (error response) on
// validation failure, or a nil result on success.
func buildReflectionCreateParams(
	wsID *uuid.UUID, args map[string]any,
) (*reflection.CreateParams, *mcp.CallToolResult) {
	reflType := stringArg(args, "type")
	if reflType == "" {
		return nil, mcp.NewToolResultError("type is required")
	}
	if !reflection.AllowedTypes[reflType] {
		allowed := make([]string, 0, len(reflection.AllowedTypes))
		for k := range reflection.AllowedTypes {
			allowed = append(allowed, k)
		}
		return nil, mcp.NewToolResultError(fmt.Sprintf(
			"invalid type %q; must be one of: %s", reflType, strings.Join(allowed, ", "),
		))
	}

	summary := stringArg(args, "summary")
	if strings.ContainsAny(summary, "\x00\r\n") {
		return nil, mcp.NewToolResultError("summary must not contain null bytes or newlines")
	}
	const maxSummary = 5000
	if len([]rune(summary)) > maxSummary {
		return nil, mcp.NewToolResultError(fmt.Sprintf("summary exceeds %d characters", maxSummary))
	}

	confidence := floatArg(args, "confidence")
	if confidence < 0.0 || confidence > 1.0 {
		return nil, mcp.NewToolResultError("confidence must be between 0.0 and 1.0")
	}

	insights, err := parseOptionalJSON(args, "insights")
	if err != nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("insights: %v", err))
	}
	patternsDetected, err := parseOptionalJSON(args, "patterns_detected")
	if err != nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("patterns_detected: %v", err))
	}
	suggestedActions, err := parseOptionalJSON(args, "suggested_actions")
	if err != nil {
		return nil, mcp.NewToolResultError(fmt.Sprintf("suggested_actions: %v", err))
	}

	params := &reflection.CreateParams{
		WorkspaceID:      wsID,
		Type:             reflType,
		Summary:          summary,
		Insights:         insights,
		PatternsDetected: patternsDetected,
		SuggestedActions: suggestedActions,
		Confidence:       confidence,
	}

	if errResult := applyRelatedEntity(params, args); errResult != nil {
		return nil, errResult
	}

	return params, nil
}

// applyRelatedEntity validates and sets optional RelatedEntityType and RelatedEntityID.
func applyRelatedEntity(params *reflection.CreateParams, args map[string]any) *mcp.CallToolResult {
	relEntityType := stringArg(args, "related_entity_type")
	if strings.ContainsAny(relEntityType, "\x00\r\n") {
		return mcp.NewToolResultError("related_entity_type must not contain null bytes or newlines")
	}
	if relEntityType != "" {
		params.RelatedEntityType = &relEntityType
	}

	relEntityIDStr := stringArg(args, "related_entity_id")
	if relEntityIDStr != "" {
		relID, parseErr := uuid.Parse(relEntityIDStr)
		if parseErr != nil {
			return mcp.NewToolResultError(fmt.Sprintf("related_entity_id: invalid UUID: %v", parseErr))
		}
		params.RelatedEntityID = &relID
	}
	return nil
}

// handleListReflections returns reflection records, optionally filtered by type.
func (s *Server) handleListReflections(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	params := reflection.ListParams{
		WorkspaceID: s.workspaceUUID(),
	}

	if t := stringArg(args, "type"); t != "" {
		if !reflection.AllowedTypes[t] {
			return mcp.NewToolResultError(fmt.Sprintf("invalid type %q", t)), nil
		}
		params.Type = &t
	}

	limit := int(numberArg(args, "limit"))
	if limit > 0 {
		params.Limit = limit
	}

	reflections, err := s.reflection.List(ctx, params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing reflections: %v", err)), nil
	}
	if reflections == nil {
		reflections = []*reflection.Reflection{}
	}
	return jsonText(wrapUntrustedReflections(reflections))
}

// handleGetLatestReflection returns the most recent reflection of the given type.
func (s *Server) handleGetLatestReflection(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	reflType := stringArg(args, "type")
	if reflType == "" {
		return mcp.NewToolResultError("type is required"), nil
	}
	if !reflection.AllowedTypes[reflType] {
		return mcp.NewToolResultError(fmt.Sprintf("invalid type %q", reflType)), nil
	}

	r, err := s.reflection.GetLatest(ctx, s.workspaceUUID(), reflType)
	if err != nil {
		// No reflection yet is a valid empty state, not an operation failure:
		// return null content (isError=false) so callers distinguish "no data"
		// from a genuine error. Mirrors analyze_recent_patterns returning [].
		if errors.Is(err, reflection.ErrNotFound) {
			return jsonText(nil)
		}
		return mcp.NewToolResultError(fmt.Sprintf("getting latest reflection: %v", err)), nil
	}
	return jsonText(wrapUntrustedReflection(r))
}

// handleAnalyzeRecentPatterns returns reflections with detected patterns within the look-back window.
func (s *Server) handleAnalyzeRecentPatterns(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	days := int(numberArg(args, "days"))
	if days <= 0 {
		days = 7
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour)

	reflections, err := s.reflection.RecentWithPatterns(ctx, s.workspaceUUID(), since, 50)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("analyzing recent patterns: %v", err)), nil
	}
	if reflections == nil {
		reflections = []*reflection.Reflection{}
	}
	return jsonText(wrapUntrustedReflections(reflections))
}

// parseOptionalJSON extracts a string argument and validates it as JSON.
// Returns nil json.RawMessage when the argument is absent or empty.
func parseOptionalJSON(args map[string]any, key string) (json.RawMessage, error) {
	raw := stringArg(args, key)
	if raw == "" {
		return nil, nil
	}
	if strings.ContainsRune(raw, '\x00') {
		return nil, fmt.Errorf("must not contain null bytes")
	}
	if !json.Valid([]byte(raw)) {
		return nil, fmt.Errorf("must be valid JSON")
	}
	return json.RawMessage(raw), nil
}
