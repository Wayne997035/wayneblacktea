package mcp

import (
	"context"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerDecisionTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("log_decision",
		mcp.WithDescription(
			"CALL when a technical decision is confirmed (user says go/start/好啊). "+
				"Records architectural and design decisions with context and rationale.",
		),
		mcp.WithString("title", mcp.Description("Short decision title"), mcp.Required()),
		mcp.WithString("context", mcp.Description("What problem or situation prompted this decision"), mcp.Required()),
		mcp.WithString("decision", mcp.Description("What was decided"), mcp.Required()),
		mcp.WithString("rationale", mcp.Description("Why this decision was made"), mcp.Required()),
		mcp.WithString("repo_name", mcp.Description("Repository this decision relates to")),
		mcp.WithString("project_id", mcp.Description("Project UUID this decision relates to")),
		mcp.WithString("alternatives", mcp.Description("Other options that were considered")),
	), s.handleLogDecision)

	ms.AddTool(mcp.NewTool("list_decisions",
		mcp.WithDescription(
			"CALL BEFORE scanning code — check if the answer is already stored. "+
				"Returns decisions filtered by repo_name or project.",
		),
		mcp.WithString("repo_name", mcp.Description("Filter by repository name")),
		mcp.WithString("project_id", mcp.Description("Filter by project UUID")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 20)")),
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

	p := decision.LogParams{
		Title:        title,
		Context:      decCtx,
		Decision:     dec,
		Rationale:    rationale,
		RepoName:     stringArg(args, "repo_name"),
		Alternatives: stringArg(args, "alternatives"),
	}
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid project_id UUID"), nil
		}
		p.ProjectID = &id
	}

	d, err := s.decision.Log(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("logging decision: %v", err)), nil
	}
	return jsonText(d)
}

func (s *Server) handleListDecisions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	limit := numberArg(args, "limit")
	if limit <= 0 {
		limit = 20
	}

	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid project_id UUID"), nil
		}
		decisions, err := s.decision.ByProject(ctx, id, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loading decisions: %v", err)), nil
		}
		return jsonText(decisions)
	}

	repoName := stringArg(args, "repo_name")
	decisions, err := s.decision.ByRepo(ctx, repoName, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading decisions: %v", err)), nil
	}
	return jsonText(decisions)
}
