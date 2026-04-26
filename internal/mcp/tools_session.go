package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/waynechen/wayneblacktea/internal/session"
)

func (s *Server) registerSessionTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("set_session_handoff",
		mcp.WithDescription("CALL when user says tomorrow/next time/later. Records what to continue in next session."),
		mcp.WithString("intent", mcp.Description("What to continue next session"), mcp.Required()),
		mcp.WithString("repo_name", mcp.Description("Repository being worked on")),
		mcp.WithString("context_summary", mcp.Description("Current state and relevant context")),
		mcp.WithString("project_id", mcp.Description("Active project UUID")),
	), s.handleSetSessionHandoff)

	ms.AddTool(mcp.NewTool("resolve_handoff",
		mcp.WithDescription("Marks the pending session handoff as resolved (work resumed)."),
		mcp.WithString("handoff_id", mcp.Description("Handoff UUID to resolve"), mcp.Required()),
	), s.handleResolveHandoff)
}

func (s *Server) handleSetSessionHandoff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	intent := stringArg(args, "intent")
	if intent == "" {
		return mcp.NewToolResultError("intent is required"), nil
	}

	p := session.HandoffParams{
		Intent:         intent,
		RepoName:       stringArg(args, "repo_name"),
		ContextSummary: stringArg(args, "context_summary"),
	}
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid project_id UUID"), nil
		}
		p.ProjectID = &id
	}

	h, err := s.session.SetHandoff(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("setting handoff: %v", err)), nil
	}
	return jsonText(h)
}

func (s *Server) handleResolveHandoff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	raw := stringArg(args, "handoff_id")
	if raw == "" {
		return mcp.NewToolResultError("handoff_id is required"), nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return mcp.NewToolResultError("invalid handoff_id UUID"), nil
	}

	if err := s.session.Resolve(ctx, id); errors.Is(err, session.ErrNotFound) {
		return mcp.NewToolResultError("handoff not found"), nil
	} else if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("resolving handoff: %v", err)), nil
	}

	return mcp.NewToolResultText("handoff resolved"), nil
}
