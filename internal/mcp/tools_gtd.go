package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func (s *Server) registerGTDTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool("list_projects",
		mcp.WithDescription("Returns all active projects."),
	), s.handleListProjects)

	ms.AddTool(mcp.NewTool("create_project",
		mcp.WithDescription("Creates a new project."),
		mcp.WithString("name", mcp.Description("Short slug identifier"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Display title"), mcp.Required()),
		mcp.WithString("area", mcp.Description("Work area (e.g. engineering, personal)"), mcp.Required()),
		mcp.WithString("description", mcp.Description("Detailed description")),
		mcp.WithString("goal_id", mcp.Description("Parent goal UUID")),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5, lower is higher")),
	), s.handleCreateProject)

	ms.AddTool(mcp.NewTool("list_tasks",
		mcp.WithDescription("Lists tasks, optionally filtered by project."),
		mcp.WithString("project_id", mcp.Description("Filter by project UUID")),
	), s.handleListTasks)

	ms.AddTool(mcp.NewTool("add_task",
		mcp.WithDescription(
			"CALL immediately when follow-up work is identified during discussion. "+
				"Creates a task optionally under a project.",
		),
		mcp.WithString("title", mcp.Description("Task title"), mcp.Required()),
		mcp.WithString("project_id", mcp.Description("Parent project UUID")),
		mcp.WithString("description", mcp.Description("Task details")),
		mcp.WithString("assignee", mcp.Description("Who owns this task")),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5 (execution order, lower runs first)")),
		mcp.WithNumber("importance", mcp.Description("Importance 1-3 (1=high, 2=med, 3=low) — distinct from priority")),
		mcp.WithString("context", mcp.Description("Free-form discussion background — why this task came up")),
	), s.handleAddTask)

	ms.AddTool(mcp.NewTool("complete_task",
		mcp.WithDescription(
			"CALL after task is verified done (build pass, tests pass). Marks task completed and records artifact.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("artifact", mcp.Description("Link or note for the output")),
	), s.handleCompleteTask)

	ms.AddTool(mcp.NewTool("list_goals",
		mcp.WithDescription("Returns all active goals ordered by due date."),
	), s.handleListGoals)

	ms.AddTool(mcp.NewTool("create_goal",
		mcp.WithDescription("Creates a new goal."),
		mcp.WithString("title", mcp.Description("Goal title"), mcp.Required()),
		mcp.WithString("area", mcp.Description("Life area (e.g. career, health, personal)"), mcp.Required()),
		mcp.WithString("description", mcp.Description("Detailed description")),
		mcp.WithString("due_date", mcp.Description("Target date in RFC3339 format (e.g. 2026-12-31T00:00:00Z)")),
	), s.handleCreateGoal)

	ms.AddTool(mcp.NewTool("update_task",
		mcp.WithDescription("Updates the status of a task."),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("status", mcp.Description("New status: pending, in_progress, or cancelled"), mcp.Required()),
	), s.handleUpdateTask)

	ms.AddTool(mcp.NewTool("update_project_status",
		mcp.WithDescription("Updates the status of a project."),
		mcp.WithString("project_id", mcp.Description("Project UUID"), mcp.Required()),
		mcp.WithString("status", mcp.Description("New status: active, completed, archived, or on_hold"), mcp.Required()),
	), s.handleUpdateProjectStatus)

	ms.AddTool(mcp.NewTool("get_project",
		mcp.WithDescription("Returns a project by name with its recent decisions."),
		mcp.WithString("name", mcp.Description("Project slug name"), mcp.Required()),
	), s.handleGetProject)

	ms.AddTool(mcp.NewTool("log_activity",
		mcp.WithDescription("Records an activity log entry for a project."),
		mcp.WithString("actor", mcp.Description("Who did the action (e.g. claude-code, human)"), mcp.Required()),
		mcp.WithString("action", mcp.Description("What was done"), mcp.Required()),
		mcp.WithString("project_id", mcp.Description("Project UUID (optional)")),
		mcp.WithString("notes", mcp.Description("Additional notes")),
	), s.handleLogActivity)

	ms.AddTool(mcp.NewTool("delete_task",
		mcp.WithDescription(
			"Permanently deletes a task. TWO-STEP: first call with only task_id "+
				"returns {deletion_token, expires_at}; second call MUST include "+
				"confirm=true and deletion_token to perform the delete. Tokens "+
				"expire after 60s.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithBoolean("confirm", mcp.Description("Set true on the second call to actually delete")),
		mcp.WithString("deletion_token", mcp.Description("Token returned by the first call; required when confirm=true")),
	), s.handleDeleteTask)
}

func (s *Server) handleListProjects(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	projects, err := s.gtd.ListActiveProjects(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading projects: %v", err)), nil
	}
	return jsonText(projects)
}

func (s *Server) handleCreateProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name, title, area := stringArg(args, "name"), stringArg(args, "title"), stringArg(args, "area")
	if name == "" || title == "" || area == "" {
		return mcp.NewToolResultError("name, title and area are required"), nil
	}

	p := gtd.CreateProjectParams{
		Name:        name,
		Title:       title,
		Area:        area,
		Description: stringArg(args, "description"),
		Priority:    numberArg(args, "priority"),
	}
	if raw := stringArg(args, "goal_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid goal_id UUID"), nil
		}
		p.GoalID = &id
	}

	project, err := s.gtd.CreateProject(ctx, p)
	if errors.Is(err, gtd.ErrConflict) {
		return mcp.NewToolResultError("project name already exists"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating project: %v", err)), nil
	}
	return jsonText(project)
}

func (s *Server) handleListTasks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	var projectID *uuid.UUID
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid project_id UUID"), nil
		}
		projectID = &id
	}
	tasks, err := s.gtd.Tasks(ctx, projectID)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading tasks: %v", err)), nil
	}
	return jsonText(tasks)
}

func (s *Server) handleAddTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title := stringArg(args, "title")
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}

	p := gtd.CreateTaskParams{
		Title:       title,
		Description: stringArg(args, "description"),
		Assignee:    stringArg(args, "assignee"),
		Priority:    numberArg(args, "priority"),
		Context:     stringArg(args, "context"),
	}
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid project_id UUID"), nil
		}
		p.ProjectID = &id
	}
	if imp := numberArg(args, "importance"); imp > 0 {
		if imp < 1 || imp > 3 {
			return mcp.NewToolResultError("importance must be 1, 2, or 3"), nil
		}
		v := int16(imp)
		p.Importance = &v
	}

	task, err := s.gtd.CreateTask(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating task: %v", err)), nil
	}
	return jsonText(task)
}

func (s *Server) handleCompleteTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	raw := stringArg(args, "task_id")
	if raw == "" {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return mcp.NewToolResultError("invalid task_id UUID"), nil
	}

	var artifact *string
	if a := stringArg(args, "artifact"); a != "" {
		artifact = &a
	}

	task, err := s.gtd.CompleteTask(ctx, id, artifact)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("completing task: %v", err)), nil
	}
	return jsonText(task)
}

func (s *Server) handleListGoals(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	goals, err := s.gtd.ActiveGoals(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading goals: %v", err)), nil
	}
	return jsonText(goals)
}

func (s *Server) handleCreateGoal(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title, area := stringArg(args, "title"), stringArg(args, "area")
	if title == "" || area == "" {
		return mcp.NewToolResultError("title and area are required"), nil
	}

	p := gtd.CreateGoalParams{
		Title:       title,
		Area:        area,
		Description: stringArg(args, "description"),
	}
	if raw := stringArg(args, "due_date"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return mcp.NewToolResultError("invalid due_date: must be RFC3339 (e.g. 2026-12-31T00:00:00Z)"), nil
		}
		p.DueDate = &t
	}

	goal, err := s.gtd.CreateGoal(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating goal: %v", err)), nil
	}
	return jsonText(goal)
}

func (s *Server) handleUpdateTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawID := stringArg(args, "task_id")
	if rawID == "" {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return mcp.NewToolResultError("invalid task_id UUID"), nil
	}
	rawStatus := stringArg(args, "status")
	if rawStatus == "" {
		return mcp.NewToolResultError("status is required"), nil
	}
	status := gtd.TaskStatus(rawStatus)
	switch status {
	case gtd.TaskStatusPending, gtd.TaskStatusInProgress, gtd.TaskStatusCancelled:
	default:
		return mcp.NewToolResultError("status must be one of: pending, in_progress, cancelled"), nil
	}

	task, err := s.gtd.UpdateTaskStatus(ctx, id, status)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("updating task: %v", err)), nil
	}
	return jsonText(task)
}

func (s *Server) handleUpdateProjectStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawID := stringArg(args, "project_id")
	if rawID == "" {
		return mcp.NewToolResultError("project_id is required"), nil
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return mcp.NewToolResultError("invalid project_id UUID"), nil
	}
	rawStatus := stringArg(args, "status")
	if rawStatus == "" {
		return mcp.NewToolResultError("status is required"), nil
	}
	status := gtd.ProjectStatus(rawStatus)
	switch status {
	case gtd.ProjectStatusActive, gtd.ProjectStatusCompleted, gtd.ProjectStatusArchived, gtd.ProjectStatusOnHold:
	default:
		return mcp.NewToolResultError("status must be one of: active, completed, archived, on_hold"), nil
	}

	project, err := s.gtd.UpdateProjectStatus(ctx, id, status)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("project not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("updating project: %v", err)), nil
	}
	return jsonText(project)
}

type projectWithDecisions struct {
	Project   any `json:"project"`
	Decisions any `json:"recent_decisions"`
}

func (s *Server) handleGetProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	name := stringArg(args, "name")
	if name == "" {
		return mcp.NewToolResultError("name is required"), nil
	}

	project, err := s.gtd.ProjectByName(ctx, name)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError(fmt.Sprintf("project %q not found", name)), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading project: %v", err)), nil
	}

	decisions, err := s.decision.ByProject(ctx, project.ID, 5)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading decisions: %v", err)), nil
	}

	return jsonText(projectWithDecisions{Project: project, Decisions: decisions})
}

func (s *Server) handleLogActivity(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	actor, action := stringArg(args, "actor"), stringArg(args, "action")
	if actor == "" || action == "" {
		return mcp.NewToolResultError("actor and action are required"), nil
	}

	var projectID *uuid.UUID
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError("invalid project_id UUID"), nil
		}
		projectID = &id
	}

	notes := stringArg(args, "notes")
	if err := s.gtd.LogActivity(ctx, actor, action, projectID, notes); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("logging activity: %v", err)), nil
	}
	return mcp.NewToolResultText("activity logged"), nil
}

// handleDeleteTask implements a 2-step confirmation flow.
//
// First call (confirm absent / false): issue a one-time deletion_token tied
// to task_id, store it in-memory with a 60s TTL, and return
// {deletion_token, expires_at} to the caller. The task is NOT deleted.
//
// Second call (confirm=true + matching deletion_token): verify the token
// matches the stored value AND has not expired, then call store.DeleteTask.
// The token is consumed (single-use) regardless of success.
//
// Rationale: prevent accidental deletion from a hallucinated tool call. An
// LLM that emits delete_task once gets a token-only response back; deleting
// requires a second deliberate call. The token must come from us so a malicious
// upstream client can't synthesize one without first making a "read" call.
func (s *Server) handleDeleteTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	raw := stringArg(args, "task_id")
	if raw == "" {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return mcp.NewToolResultError("invalid task_id UUID"), nil
	}

	confirm := boolArg(args, "confirm")
	suppliedToken := stringArg(args, "deletion_token")

	if !confirm {
		// Step 1 — issue token, do NOT delete.
		token := uuid.NewString()
		expires := s.now().Add(deleteTokenTTL)
		s.deleteTokens.Store(id.String(), deletionToken{token: token, expiresAt: expires})
		return jsonText(map[string]any{
			"status":         "confirmation_required",
			"task_id":        id.String(),
			"deletion_token": token,
			"expires_at":     expires.UTC().Format(time.RFC3339),
			"message":        "Call delete_task again with confirm=true and the deletion_token to delete this task. Token expires in 60s.",
		})
	}

	// Step 2 — confirm=true. Token must be present, match, and not expired.
	if suppliedToken == "" {
		return mcp.NewToolResultError("deletion_token is required when confirm=true"), nil
	}
	stored, ok := s.deleteTokens.LoadAndDelete(id.String())
	if !ok {
		return mcp.NewToolResultError("no pending deletion for this task_id; call without confirm first to obtain a token"), nil
	}
	rec, ok := stored.(deletionToken)
	if !ok {
		return mcp.NewToolResultError("internal: deletion token state corrupted"), nil
	}
	if s.now().After(rec.expiresAt) {
		return mcp.NewToolResultError("deletion_token expired; call without confirm to obtain a new token"), nil
	}
	// Constant-time string compare on equal-length inputs would be ideal, but
	// these tokens are generated server-side UUIDs and never exposed to
	// untrusted parties in the comparison window — plain equality is fine.
	if suppliedToken != rec.token {
		return mcp.NewToolResultError("deletion_token mismatch"), nil
	}

	if err := s.gtd.DeleteTask(ctx, id); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("deleting task: %v", err)), nil
	}
	return mcp.NewToolResultText("task deleted"), nil
}
