package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerSkillTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"extract_skill",
		mcp.WithDescription(
			"Extracts and persists a reusable skill definition from the current session. "+
				"Provide a name, description, trigger conditions, step-by-step approach, "+
				"common failure modes, and a verification checklist.",
		),
		mcp.WithString("name",
			mcp.Description("Short unique name for the skill (max 200 chars)"),
			mcp.Required(), mcp.MaxLength(200)),
		mcp.WithString("description",
			mcp.Description("What the skill does and when to apply it (max 5000 chars)"),
			mcp.MaxLength(5000)),
		mcp.WithString("triggers",
			mcp.Description("Comma-separated trigger conditions that indicate this skill applies")),
		mcp.WithString("steps",
			mcp.Description("Comma-separated ordered steps for executing the skill")),
		mcp.WithString("failure_modes",
			mcp.Description("Comma-separated common failure modes to watch for")),
		mcp.WithString("verification_checklist",
			mcp.Description("Comma-separated verification checks to confirm success")),
		mcp.WithString("source_atom_ids",
			mcp.Description("Comma-separated memory atom IDs that inform this skill (no FK)")),
	), s.handleExtractSkill)

	ms.AddTool(mcp.NewTool(
		"search_skills",
		mcp.WithDescription(
			"Searches persisted skills by name or description. "+
				"Returns matching skills ordered by success_count DESC.",
		),
		mcp.WithString("query",
			mcp.Description("Search query (matches name and description)"),
			mcp.Required()),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 10)")),
	), s.handleSearchSkills)

	ms.AddTool(mcp.NewTool(
		"use_skill",
		mcp.WithDescription(
			"Records that a skill was applied successfully. Increments success_count "+
				"and updates last_used_at. Returns the updated skill.",
		),
		mcp.WithString("skill_id",
			mcp.Description("UUID of the skill to mark as used"),
			mcp.Required()),
	), s.handleUseSkill)

	ms.AddTool(mcp.NewTool(
		"update_skill_from_outcome",
		mcp.WithDescription(
			"Records a success or failure outcome for a skill execution. "+
				"Appends the outcome reference and notes to the skill's examples log "+
				"and increments the appropriate counter.",
		),
		mcp.WithString("skill_id",
			mcp.Description("UUID of the skill"),
			mcp.Required()),
		mcp.WithString("outcome_id",
			mcp.Description("Reference ID of the outcome (e.g. task ID, decision ID — no FK)")),
		mcp.WithBoolean("success",
			mcp.Description("REQUIRED: true = success outcome, false = failure outcome. No default — "+
				"omitting this is rejected rather than silently recorded as a failure."),
			mcp.Required()),
		mcp.WithString("notes",
			mcp.Description("Notes about the outcome (max 2000 chars)"),
			mcp.MaxLength(2000)),
	), s.handleUpdateSkillFromOutcome)

	ms.AddTool(mcp.NewTool(
		"list_relevant_skills",
		mcp.WithDescription(
			"Lists skills most relevant to the current task context. "+
				"Skills are ordered by success_count DESC, last_used_at DESC. "+
				"Optionally filter by keyword query.",
		),
		mcp.WithString("query",
			mcp.Description("Optional keyword to filter by name or description")),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default 10)")),
	), s.handleListRelevantSkills)
}

// validateSkillName returns an error message if name is empty or contains
// control characters. Returns empty string on success.
func validateSkillName(name string) string {
	if name == "" {
		return "name is required"
	}
	if len([]rune(name)) > 200 {
		return "name exceeds 200 character limit"
	}
	if strings.ContainsAny(name, "\x00\r\n") {
		return "name must not contain null bytes or newlines"
	}
	return ""
}

// validateSkillDescription returns an error message if description violates
// length or control-character constraints.
func validateSkillDescription(description string) string {
	if len([]rune(description)) > 5000 {
		return "description exceeds 5000 character limit"
	}
	if strings.ContainsAny(description, "\x00\r\n") {
		return "description must not contain null bytes or newlines"
	}
	return ""
}

// validateSkillCSVField returns an error message if a CSV-derived field string
// contains null bytes or newlines (validated before splitting).
func validateSkillCSVField(field, name string) string {
	if strings.ContainsAny(field, "\x00\r\n") {
		return name + " must not contain null bytes or newlines"
	}
	return ""
}

// validateNotes returns an error message if notes text is too long.
func validateNotes(notes string) string {
	if len([]rune(notes)) > 2000 {
		return "notes exceeds 2000 character limit"
	}
	return ""
}

// hasBoolArg reports whether key is present in args and holds a JSON boolean
// value (true or false) — distinguishes "omitted" from "explicitly false".
// Ω8 fix (mcp-surface spec, backend-security-design.md §2.1): boolArg's
// missing-key default of false made an omitted update_skill_from_outcome
// `success` argument silently record a FAILURE outcome — the opposite of
// "caller forgot to say" being a no-op or an error. mcp.Required() on the
// tool schema is client-side advisory only (mcp-go does not enforce it
// server-side, see the existing "X is required" checks throughout this
// package), so the server-side check below is what actually rejects it.
func hasBoolArg(args map[string]any, key string) bool {
	_, ok := args[key].(bool)
	return ok
}

// handleExtractSkill implements the extract_skill MCP tool.
func (s *Server) handleExtractSkill(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name := stringArg(args, "name")
	if msg := validateSkillName(name); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	description := stringArg(args, "description")
	if msg := validateSkillDescription(description); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	rawTriggers := stringArg(args, "triggers")
	if msg := validateSkillCSVField(rawTriggers, "triggers"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	rawSteps := stringArg(args, "steps")
	if msg := validateSkillCSVField(rawSteps, "steps"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	rawFailureModes := stringArg(args, "failure_modes")
	if msg := validateSkillCSVField(rawFailureModes, "failure_modes"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	rawVerification := stringArg(args, "verification_checklist")
	if msg := validateSkillCSVField(rawVerification, "verification_checklist"); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	p := skill.AddParams{
		Name:                  name,
		Description:           description,
		Triggers:              splitCSV(rawTriggers),
		Steps:                 splitCSV(rawSteps),
		FailureModes:          splitCSV(rawFailureModes),
		VerificationChecklist: splitCSV(rawVerification),
		SourceAtomIDs:         splitCSV(stringArg(args, "source_atom_ids")),
		Examples:              []any{},
	}

	if wsID := s.workspaceUUID(); wsID != nil {
		wsStr := wsID.String()
		p.WorkspaceID = &wsStr
	}

	sk, err := s.skill.Add(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("extracting skill: %v", err)), nil
	}

	s.launchAtomize("skills", mustParseUUID(sk.ID), name+" "+description)
	return jsonText(sk)
}

// handleSearchSkills implements the search_skills MCP tool.
func (s *Server) handleSearchSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query := stringArg(args, "query")
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 10
	}

	f := skill.SearchFilter{
		Query: query,
		Limit: limit,
	}
	if wsID := s.workspaceUUID(); wsID != nil {
		wsStr := wsID.String()
		f.WorkspaceID = &wsStr
	}

	results, err := s.skill.Search(ctx, f)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("searching skills: %v", err)), nil
	}
	if results == nil {
		results = []*skill.Skill{}
	}
	return jsonText(results)
}

// handleUseSkill implements the use_skill MCP tool.
func (s *Server) handleUseSkill(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	skillID := stringArg(args, "skill_id")
	if skillID == "" {
		return mcp.NewToolResultError("skill_id is required"), nil
	}

	var wsStr *string
	if wsID := s.workspaceUUID(); wsID != nil {
		s := wsID.String()
		wsStr = &s
	}

	sk, err := s.skill.IncrementSuccess(ctx, skillID, wsStr)
	if err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			return mcp.NewToolResultError("skill not found"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("using skill: %v", err)), nil
	}
	return jsonText(sk)
}

// handleUpdateSkillFromOutcome implements the update_skill_from_outcome MCP tool.
func (s *Server) handleUpdateSkillFromOutcome(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	skillID := stringArg(args, "skill_id")
	if skillID == "" {
		return mcp.NewToolResultError("skill_id is required"), nil
	}

	notes := stringArg(args, "notes")
	if msg := validateNotes(notes); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	if !hasBoolArg(args, "success") {
		return mcp.NewToolResultError(
			"success is required: true = success outcome, false = failure outcome (no default)",
		), nil
	}
	success := boolArg(args, "success")

	var wsStr *string
	if wsID := s.workspaceUUID(); wsID != nil {
		s := wsID.String()
		wsStr = &s
	}

	p := skill.UpdateFromOutcomeParams{
		SkillID:   skillID,
		OutcomeID: stringArg(args, "outcome_id"),
		Success:   success,
		Notes:     notes,
	}

	sk, err := s.skill.UpdateFromOutcome(ctx, p, wsStr)
	if err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			return mcp.NewToolResultError("skill not found"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("updating skill from outcome: %v", err)), nil
	}

	if notes != "" {
		s.launchAtomize("skills", mustParseUUID(sk.ID), notes)
	}
	return jsonText(sk)
}

// handleListRelevantSkills implements the list_relevant_skills MCP tool.
func (s *Server) handleListRelevantSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	query := stringArg(args, "query")

	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 10
	}

	var wsStr *string
	if wsID := s.workspaceUUID(); wsID != nil {
		s := wsID.String()
		wsStr = &s
	}

	results, err := s.skill.ListRelevant(ctx, wsStr, query, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("listing relevant skills: %v", err)), nil
	}
	if results == nil {
		results = []*skill.Skill{}
	}
	return jsonText(results)
}

// mustParseUUID parses a UUID string and returns a zero UUID on failure.
// Used for launchAtomize where the ID is a DB-generated UUID string.
func mustParseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.UUID{}
	}
	return id
}
