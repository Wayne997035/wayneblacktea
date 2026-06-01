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
		mcp.WithDescription("Returns the most recently created reflection of a given type."),
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
	return jsonText(r)
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
	return jsonText(reflections)
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
		if errors.Is(err, reflection.ErrNotFound) {
			return mcp.NewToolResultError("no reflection found"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("getting latest reflection: %v", err)), nil
	}
	return jsonText(r)
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
	return jsonText(reflections)
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
