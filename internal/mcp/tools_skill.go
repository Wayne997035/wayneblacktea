package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/skill"
	"github.com/google/uuid"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerSkillTools(ms *server.MCPServer) {
	ms.AddTool(mcpgo.NewTool("extract_skill",
		mcpgo.WithDescription(
			"Extracts and persists a reusable skill definition from the current session. "+
				"Provide a name, description, trigger conditions, step-by-step approach, "+
				"common failure modes, and a verification checklist.",
		),
		mcpgo.WithString("name",
			mcpgo.Description("Short unique name for the skill (max 200 chars)"),
			mcpgo.Required(), mcpgo.MaxLength(200)),
		mcpgo.WithString("description",
			mcpgo.Description("What the skill does and when to apply it (max 5000 chars)"),
			mcpgo.MaxLength(5000)),
		mcpgo.WithString("triggers",
			mcpgo.Description("Comma-separated trigger conditions that indicate this skill applies")),
		mcpgo.WithString("steps",
			mcpgo.Description("Comma-separated ordered steps for executing the skill")),
		mcpgo.WithString("failure_modes",
			mcpgo.Description("Comma-separated common failure modes to watch for")),
		mcpgo.WithString("verification_checklist",
			mcpgo.Description("Comma-separated verification checks to confirm success")),
		mcpgo.WithString("source_atom_ids",
			mcpgo.Description("Comma-separated memory atom IDs that inform this skill (no FK)")),
	), s.handleExtractSkill)

	ms.AddTool(mcpgo.NewTool("search_skills",
		mcpgo.WithDescription(
			"Searches persisted skills by name or description. "+
				"Returns matching skills ordered by success_count DESC.",
		),
		mcpgo.WithString("query",
			mcpgo.Description("Search query (matches name and description)"),
			mcpgo.Required()),
		mcpgo.WithNumber("limit",
			mcpgo.Description("Max results (default 10)")),
	), s.handleSearchSkills)

	ms.AddTool(mcpgo.NewTool("use_skill",
		mcpgo.WithDescription(
			"Records that a skill was applied successfully. Increments success_count "+
				"and updates last_used_at. Returns the updated skill.",
		),
		mcpgo.WithString("skill_id",
			mcpgo.Description("UUID of the skill to mark as used"),
			mcpgo.Required()),
	), s.handleUseSkill)

	ms.AddTool(mcpgo.NewTool("update_skill_from_outcome",
		mcpgo.WithDescription(
			"Records a success or failure outcome for a skill execution. "+
				"Appends the outcome reference and notes to the skill's examples log "+
				"and increments the appropriate counter.",
		),
		mcpgo.WithString("skill_id",
			mcpgo.Description("UUID of the skill"),
			mcpgo.Required()),
		mcpgo.WithString("outcome_id",
			mcpgo.Description("Reference ID of the outcome (e.g. task ID, decision ID — no FK)")),
		mcpgo.WithBoolean("success",
			mcpgo.Description("true = success outcome, false = failure outcome")),
		mcpgo.WithString("notes",
			mcpgo.Description("Notes about the outcome (max 2000 chars)"),
			mcpgo.MaxLength(2000)),
	), s.handleUpdateSkillFromOutcome)

	ms.AddTool(mcpgo.NewTool("list_relevant_skills",
		mcpgo.WithDescription(
			"Lists skills most relevant to the current task context. "+
				"Skills are ordered by success_count DESC, last_used_at DESC. "+
				"Optionally filter by keyword query.",
		),
		mcpgo.WithString("query",
			mcpgo.Description("Optional keyword to filter by name or description")),
		mcpgo.WithNumber("limit",
			mcpgo.Description("Max results (default 10)")),
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

// handleExtractSkill implements the extract_skill MCP tool.
func (s *Server) handleExtractSkill(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	name := stringArg(args, "name")
	if msg := validateSkillName(name); msg != "" {
		return mcpgo.NewToolResultError(msg), nil
	}

	description := stringArg(args, "description")
	if msg := validateSkillDescription(description); msg != "" {
		return mcpgo.NewToolResultError(msg), nil
	}

	rawTriggers := stringArg(args, "triggers")
	if msg := validateSkillCSVField(rawTriggers, "triggers"); msg != "" {
		return mcpgo.NewToolResultError(msg), nil
	}
	rawSteps := stringArg(args, "steps")
	if msg := validateSkillCSVField(rawSteps, "steps"); msg != "" {
		return mcpgo.NewToolResultError(msg), nil
	}
	rawFailureModes := stringArg(args, "failure_modes")
	if msg := validateSkillCSVField(rawFailureModes, "failure_modes"); msg != "" {
		return mcpgo.NewToolResultError(msg), nil
	}
	rawVerification := stringArg(args, "verification_checklist")
	if msg := validateSkillCSVField(rawVerification, "verification_checklist"); msg != "" {
		return mcpgo.NewToolResultError(msg), nil
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
		return mcpgo.NewToolResultError(fmt.Sprintf("extracting skill: %v", err)), nil
	}

	s.launchAtomize("skills", mustParseUUID(sk.ID), name+" "+description)
	return jsonText(sk)
}

// handleSearchSkills implements the search_skills MCP tool.
func (s *Server) handleSearchSkills(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	query := stringArg(args, "query")
	if query == "" {
		return mcpgo.NewToolResultError("query is required"), nil
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
		return mcpgo.NewToolResultError(fmt.Sprintf("searching skills: %v", err)), nil
	}
	if results == nil {
		results = []*skill.Skill{}
	}
	return jsonText(results)
}

// handleUseSkill implements the use_skill MCP tool.
func (s *Server) handleUseSkill(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	skillID := stringArg(args, "skill_id")
	if skillID == "" {
		return mcpgo.NewToolResultError("skill_id is required"), nil
	}

	var wsStr *string
	if wsID := s.workspaceUUID(); wsID != nil {
		s := wsID.String()
		wsStr = &s
	}

	sk, err := s.skill.IncrementSuccess(ctx, skillID, wsStr)
	if err != nil {
		if errors.Is(err, skill.ErrNotFound) {
			return mcpgo.NewToolResultError("skill not found"), nil
		}
		return mcpgo.NewToolResultError(fmt.Sprintf("using skill: %v", err)), nil
	}
	return jsonText(sk)
}

// handleUpdateSkillFromOutcome implements the update_skill_from_outcome MCP tool.
func (s *Server) handleUpdateSkillFromOutcome(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	args := req.GetArguments()
	skillID := stringArg(args, "skill_id")
	if skillID == "" {
		return mcpgo.NewToolResultError("skill_id is required"), nil
	}

	notes := stringArg(args, "notes")
	if msg := validateNotes(notes); msg != "" {
		return mcpgo.NewToolResultError(msg), nil
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
			return mcpgo.NewToolResultError("skill not found"), nil
		}
		return mcpgo.NewToolResultError(fmt.Sprintf("updating skill from outcome: %v", err)), nil
	}

	if notes != "" {
		s.launchAtomize("skills", mustParseUUID(sk.ID), notes)
	}
	return jsonText(sk)
}

// handleListRelevantSkills implements the list_relevant_skills MCP tool.
func (s *Server) handleListRelevantSkills(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
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
		return mcpgo.NewToolResultError(fmt.Sprintf("listing relevant skills: %v", err)), nil
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
