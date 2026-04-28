package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/Wayne997035/wayneblacktea/internal/workspace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerContextTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("get_today_context",
		mcp.WithDescription(
			"CALL AT SESSION START. Returns active goals, projects, weekly progress, and pending session handoff.",
		),
	), s.handleGetTodayContext)

	ms.AddTool(mcp.NewTool("list_active_repos",
		mcp.WithDescription("Returns all active repositories in the workspace."),
	), s.handleListActiveRepos)

	ms.AddTool(mcp.NewTool("sync_repo",
		mcp.WithDescription("Creates or updates a repository entry with current state."),
		mcp.WithString("name", mcp.Description("Repository name (unique key)"), mcp.Required()),
		mcp.WithString("path", mcp.Description("Local filesystem path")),
		mcp.WithString("description", mcp.Description("Short description")),
		mcp.WithString("language", mcp.Description("Primary programming language")),
		mcp.WithString("current_branch", mcp.Description("Current git branch")),
		mcp.WithString("next_planned_step", mcp.Description("What to work on next")),
	), s.handleSyncRepo)
}

type weeklyProgress struct {
	Completed int64 `json:"completed"`
	Total     int64 `json:"total"`
}

type todayContext struct {
	Goals          []db.Goal          `json:"goals"`
	Projects       []db.Project       `json:"projects"`
	WeeklyProgress weeklyProgress     `json:"weekly_progress"`
	PendingHandoff *db.SessionHandoff `json:"pending_handoff"`
}

func (s *Server) handleGetTodayContext(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	goals, err := s.gtd.ActiveGoals(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading goals: %v", err)), nil
	}

	projects, err := s.gtd.ListActiveProjects(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading projects: %v", err)), nil
	}

	completed, total, err := s.gtd.WeeklyProgress(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading progress: %v", err)), nil
	}

	handoff, err := s.session.LatestHandoff(ctx)
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		return mcp.NewToolResultError(fmt.Sprintf("loading handoff: %v", err)), nil
	}

	return jsonText(todayContext{
		Goals:          goals,
		Projects:       projects,
		WeeklyProgress: weeklyProgress{Completed: completed, Total: total},
		PendingHandoff: handoff,
	})
}

func (s *Server) handleListActiveRepos(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	repos, err := s.workspace.ActiveRepos(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading repos: %v", err)), nil
	}
	return jsonText(repos)
}

func (s *Server) handleSyncRepo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name := stringArg(args, "name")
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	repo, err := s.workspace.UpsertRepo(ctx, workspace.UpsertRepoParams{
		Name:            name,
		Path:            stringArg(args, "path"),
		Description:     stringArg(args, "description"),
		Language:        stringArg(args, "language"),
		CurrentBranch:   stringArg(args, "current_branch"),
		NextPlannedStep: stringArg(args, "next_planned_step"),
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("syncing repo: %v", err)), nil
	}
	return jsonText(repo)
}
