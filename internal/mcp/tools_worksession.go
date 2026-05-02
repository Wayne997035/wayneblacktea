package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerWorkSessionTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("start_work",
		mcp.WithDescription(
			"Start a new work session for a repository. "+
				"Call this when beginning focused work on a repo that doesn't already have an active session. "+
				"Links the supplied task_ids as primary tasks and sets current_task_id to the first one.",
		),
		mcp.WithString("repo_name", mcp.Description("Repository name (required)"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Short title for this work session (required)"), mcp.Required()),
		mcp.WithString("goal", mcp.Description("One-paragraph goal for this session (required)"), mcp.Required()),
		mcp.WithString("task_ids", mcp.Description(`JSON array of task UUIDs to link as primary (e.g. ["uuid1","uuid2"])`)),
		mcp.WithString("project_id", mcp.Description("Project UUID (optional)")),
		mcp.WithString("source", mcp.Description("Source trigger (e.g. 'manual','confirm_plan'). Defaults to 'manual'.")),
	), s.handleStartWork)

	ms.AddTool(mcp.NewTool("get_active_work",
		mcp.WithDescription(
			"Get the current in_progress work session for a repository. "+
				"Returns {active:false} when no session exists. "+
				"Check implementation_allowed before editing code.",
		),
		mcp.WithString("repo_name", mcp.Description("Repository name (required)"), mcp.Required()),
	), s.handleGetActiveWork)

	ms.AddTool(mcp.NewTool("checkpoint_work",
		mcp.WithDescription(
			"Save progress on the current work session without ending it. "+
				"Sets status=checkpointed and records last_checkpoint_at. "+
				"Use when taking a break or switching context temporarily.",
		),
		mcp.WithString("session_id", mcp.Description("Work session UUID (required)"), mcp.Required()),
		mcp.WithString("summary", mcp.Description("What was accomplished since last checkpoint (required)"), mcp.Required()),
		mcp.WithString("completed_task_ids", mcp.Description(`JSON array of task UUIDs completed in this segment`)),
		mcp.WithString("new_task_titles", mcp.Description(`JSON array of new task titles to add`)),
		mcp.WithString("new_decisions", mcp.Description(`JSON array of decision titles to log`)),
		mcp.WithString("blockers", mcp.Description(`JSON array of blocker descriptions`)),
		mcp.WithString("next_actions", mcp.Description(`JSON array of next-action descriptions`)),
	), s.handleCheckpointWork)

	ms.AddTool(mcp.NewTool("finish_work",
		mcp.WithDescription(
			"Close the current work session as completed. "+
				"Sets status=completed, records completed_at and final_summary. "+
				"Always call this when work on a session is done, even if tasks remain.",
		),
		mcp.WithString("session_id", mcp.Description("Work session UUID (required)"), mcp.Required()),
		mcp.WithString("summary", mcp.Description("Final summary of what was accomplished (required)"), mcp.Required()),
		mcp.WithString("completed_task_ids", mcp.Description(`JSON array of task UUIDs completed`)),
		mcp.WithString("deferred_task_ids", mcp.Description(`JSON array of task UUIDs deferred to next session`)),
		mcp.WithString("artifact", mcp.Description("PR URL or artifact reference (optional)")),
		mcp.WithString("follow_up_tasks", mcp.Description(`JSON array of new follow-up task titles`)),
	), s.handleFinishWork)
}

// ---- handleStartWork ----

func (s *Server) handleStartWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()

	repoName := stringArg(args, "repo_name")
	title := stringArg(args, "title")
	goal := stringArg(args, "goal")
	if repoName == "" || title == "" || goal == "" {
		return mcp.NewToolResultError("repo_name, title, and goal are required"), nil
	}

	source := stringArg(args, "source")
	if source == "" {
		source = "manual"
	}

	var projectID *uuid.UUID
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid project_id UUID"), nil
		}
		projectID = &id
	}

	var taskIDs []uuid.UUID
	if raw := stringArg(args, "task_ids"); raw != "" {
		var rawIDs []string
		if err := json.Unmarshal([]byte(raw), &rawIDs); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("invalid task_ids JSON: %v", err)), nil
		}
		for _, rawID := range rawIDs {
			id, err := uuid.Parse(rawID)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid task_id UUID %q: %v", rawID, err)), nil
			}
			taskIDs = append(taskIDs, id)
		}
	}

	wsID := s.workspaceUUIDVal()

	sess, err := s.workSession.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    repoName,
		ProjectID:   projectID,
		Title:       title,
		Goal:        goal,
		Source:      source,
		TaskIDs:     taskIDs,
	})
	if err != nil {
		if errors.Is(err, worksession.ErrAlreadyActive) {
			return mcp.NewToolResultError("another session is already in_progress for this repo — call finish_work or get_active_work first"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("start_work failed: %v", err)), nil
	}

	return jsonText(map[string]any{
		"session_id":   sess.ID,
		"status":       sess.Status,
		"title":        sess.Title,
		"goal":         sess.Goal,
		"repo_name":    sess.RepoName,
		"started_at":   sess.StartedAt,
		"linked_tasks": len(taskIDs),
	})
}

// ---- handleGetActiveWork ----

func (s *Server) handleGetActiveWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()
	repoName := stringArg(args, "repo_name")
	if repoName == "" {
		return mcp.NewToolResultError("repo_name is required"), nil
	}

	wsID := s.workspaceUUIDVal()
	result, err := s.workSession.GetActive(ctx, wsID, repoName)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("get_active_work failed: %v", err)), nil
	}
	return jsonText(result)
}

// ---- handleCheckpointWork ----

func (s *Server) handleCheckpointWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()

	rawSessID := stringArg(args, "session_id")
	summary := stringArg(args, "summary")
	if rawSessID == "" || summary == "" {
		return mcp.NewToolResultError("session_id and summary are required"), nil
	}
	sessID, err := uuid.Parse(rawSessID)
	if err != nil {
		return mcp.NewToolResultError("invalid session_id UUID"), nil
	}

	sess, err := s.workSession.Checkpoint(ctx, worksession.CheckpointParams{
		SessionID: sessID,
		Summary:   summary,
	})
	if err != nil {
		if errors.Is(err, worksession.ErrNotFound) {
			return mcp.NewToolResultError("session not found or not in checkpointable state"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("checkpoint_work failed: %v", err)), nil
	}

	return jsonText(map[string]any{
		"session_id":    sess.ID,
		"status":        sess.Status,
		"checkpoint_at": sess.LastCheckpointAt,
	})
}

// ---- handleFinishWork ----

func (s *Server) handleFinishWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.workSession == nil {
		return mcp.NewToolResultError("work session store not configured"), nil
	}
	args := req.GetArguments()

	rawSessID := stringArg(args, "session_id")
	summary := stringArg(args, "summary")
	if rawSessID == "" || summary == "" {
		return mcp.NewToolResultError("session_id and summary are required"), nil
	}
	sessID, err := uuid.Parse(rawSessID)
	if err != nil {
		return mcp.NewToolResultError("invalid session_id UUID"), nil
	}

	sess, err := s.workSession.Finish(ctx, worksession.FinishParams{
		SessionID: sessID,
		Summary:   summary,
	})
	if err != nil {
		if errors.Is(err, worksession.ErrNotFound) {
			return mcp.NewToolResultError("session not found or already completed/cancelled"), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("finish_work failed: %v", err)), nil
	}

	return jsonText(map[string]any{
		"session_id":   sess.ID,
		"status":       sess.Status,
		"completed_at": sess.CompletedAt,
		"final_report": sess.FinalSummary,
	})
}

// workspaceUUIDVal returns the workspace UUID, or uuid.Nil if not configured.
func (s *Server) workspaceUUIDVal() uuid.UUID {
	if s.workspaceID == nil {
		return uuid.Nil
	}
	return *s.workspaceID
}
