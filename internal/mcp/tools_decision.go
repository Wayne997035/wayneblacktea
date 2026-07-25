package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const maxListDecisionsLimit = 100

func (s *Server) registerDecisionTools(ms *server.MCPServer) {
	// The "CALL when ... confirmed" wording below is calling-convention
	// guidance for the LLM, not a security control — log_decision has no
	// confirmation gate of its own, so it cannot verify that a human
	// actually said go/start/好啊 (security review round 2, M-2(a); real
	// gate tracked at GTD task 41ef0520-ad5a-4aa0-805b-4ba13ba927fd). See
	// decision.Source's doc comment for the broader provenance caveat.
	ms.AddTool(mcp.NewTool(
		"log_decision",
		mcp.WithDescription(
			"CALL when the user signals go-ahead on a technical decision (e.g. says go/start/好啊) — "+
				"this is a usage convention, not a verified confirmation (the tool has no confirmation "+
				"gate of its own). Records architectural and design decisions with context and rationale.",
		),
		mcp.WithString("title", mcp.Description("Short decision title"), mcp.Required()),
		mcp.WithString("context", mcp.Description("What problem or situation prompted this decision"), mcp.Required()),
		mcp.WithString("decision", mcp.Description("What was decided"), mcp.Required()),
		mcp.WithString("rationale", mcp.Description("Why this decision was made"), mcp.Required()),
		mcp.WithString("repo_name", mcp.Description("Repository this decision relates to")),
		mcp.WithString("project_id", mcp.Description("Project UUID this decision relates to")),
		mcp.WithString("task_id", mcp.Description("Task UUID this decision relates to")),
		mcp.WithString("alternatives", mcp.Description("Other options that were considered")),
	), s.handleLogDecision)

	ms.AddTool(mcp.NewTool(
		"list_decisions",
		mcp.WithDescription(
			"CALL BEFORE scanning code — check if the answer is already stored. "+
				"Returns manual decisions by default; set include_auto=true to also include "+
				"system-inferred decisions. Filtered by repo_name or project (project wins if both given).",
		),
		mcp.WithString("repo_name", mcp.Description("Filter by repository name")),
		mcp.WithString("project_id", mcp.Description("Filter by project UUID (wins over repo_name if both given)")),
		mcp.WithBoolean("include_auto", mcp.Description("Include system-inferred (auto) decisions in addition to manual ones. Default false.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20, max 100)")),
	), s.handleListDecisions)
}

func (s *Server) handleLogDecision(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title := stringArg(args, "title")
	decCtx := stringArg(args, "context")
	dec := stringArg(args, "decision")
	rationale := stringArg(args, "rationale")
	if title == "" || decCtx == "" || dec == "" || rationale == "" {
		return mcp.NewToolResultError("title, context, decision and rationale are required"), nil
	}

	if reason := checkDecisionNoise(title, decCtx, dec, rationale); reason != "" {
		return mcp.NewToolResultError("invalid params: " + reason), nil
	}

	p := decision.LogParams{
		Title:        title,
		Context:      decCtx,
		Decision:     dec,
		Rationale:    rationale,
		RepoName:     stringArg(args, "repo_name"),
		Alternatives: stringArg(args, "alternatives"),
		Source:       decision.SourceManual,
	}
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
		}
		p.ProjectID = &id
	}
	if raw := stringArg(args, "task_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidTaskIDUUID), nil
		}
		p.TaskID = &id
	}

	d, err := s.decision.Log(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("logging decision: %v", err)), nil
	}
	s.launchAtomize("decisions", d.ID, d.Decision+" "+d.Rationale)
	return jsonText(d)
}

// handleListDecisions implements the P3.0a Stage B truth table:
//   - invalid project_id UUID -> tool error, store never called
//   - project_id and repo_name both given -> project wins (repo_name dropped)
//   - repo_name only -> filtered by repo
//   - neither -> workspace-wide
//   - project not owned / nonexistent -> [] (store's workspace scoping and
//     the WHERE project_id = ? predicate both fail closed, no error)
//   - include_auto omitted or non-bool -> boolArg fails closed to false
//   - include_auto=true -> manual + auto
func (s *Server) handleListDecisions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	limit := numberArg(args, "limit")
	if limit <= 0 {
		limit = 20
	}
	if limit > maxListDecisionsLimit {
		slog.Warn("list_decisions limit clamped", "requested", limit, "max", maxListDecisionsLimit)
		limit = maxListDecisionsLimit
	}

	p := decision.ListParams{
		RepoName:    stringArg(args, "repo_name"),
		IncludeAuto: boolArg(args, "include_auto"),
		Limit:       limit,
	}
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
		}
		p.ProjectID = &id
		p.RepoName = "" // project wins over repo_name when both are given
	}

	decisions, err := s.decision.List(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading decisions: %v", err)), nil
	}
	return jsonText(decisions)
}
