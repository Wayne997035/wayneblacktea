package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// maxPendingDeletions caps the number of valid (non-expired) delete tokens
// held simultaneously. This prevents a loop caller from growing the sync.Map
// without bound if tokens are issued faster than they expire.
const maxPendingDeletions = 256

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
		mcp.WithString("repo_name", mcp.Description("VCS repository slug to link this project (e.g. wayneblacktea)")),
	), s.handleCreateProject)

	ms.AddTool(mcp.NewTool("update_project",
		mcp.WithDescription("Updates mutable fields of a project. All params except project_id are optional. "+
			"Omitted params preserve the existing value. Use update_project_status to change status only."),
		mcp.WithString("project_id", mcp.Description("Project UUID"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Updated display title"), mcp.MaxLength(500)),
		mcp.WithString("description", mcp.Description("Updated description"), mcp.MaxLength(5000)),
		mcp.WithString("area", mcp.Description("Work area (e.g. engineering, personal)")),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5, lower is higher")),
		mcp.WithString("status",
			mcp.Description("New status: active, completed, archived, or on_hold"),
			mcp.Enum("active", "completed", "archived", "on_hold")),
		mcp.WithString("goal_id", mcp.Description("Parent goal UUID (empty string clears the link)")),
		mcp.WithString("repo_name", mcp.Description("VCS repository slug (empty string clears the link)")),
	), s.handleUpdateProject)

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
		mcp.WithDescription("Updates one or more mutable fields of a task. All params except task_id are optional. "+
			"Use complete_task to mark a task completed."),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("status",
			mcp.Description("New status: pending, in_progress, or cancelled"),
			mcp.Enum("pending", "in_progress", "cancelled")),
		mcp.WithString("title", mcp.Description("Updated task title"), mcp.MaxLength(2000)),
		mcp.WithString("description", mcp.Description("Updated task details"), mcp.MaxLength(10000)),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5 (execution order, lower runs first)")),
		mcp.WithNumber("importance", mcp.Description("Importance 1-3 (1=high, 2=med, 3=low)")),
		mcp.WithString("assignee", mcp.Description("Who owns this task"), mcp.MaxLength(200)),
		mcp.WithString("due_date", mcp.Description("Due date in RFC3339 format (e.g. 2026-12-31T00:00:00Z)")),
		mcp.WithString("context", mcp.Description("Free-form discussion background"), mcp.MaxLength(10000)),
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

	ms.AddTool(mcp.NewTool("get_upcoming_work",
		mcp.WithDescription(
			"Returns pending/in-progress tasks grouped into today, tomorrow, day_after, "+
				"upcoming, and unscheduled_important buckets. Use to plan the next work "+
				"session or surface high-importance tasks that have no due date.",
		),
		mcp.WithNumber("days", mcp.Description("How many days ahead to include (1-14, default 7)")),
	), s.handleGetUpcomingWork)

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

	ms.AddTool(mcp.NewTool("task_checklist_add_item",
		mcp.WithDescription(
			"Appends a new checklist item to a task. Returns the full updated checklist. "+
				"Use to track sub-steps or acceptance criteria for a task.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Item title (max 500 chars)"), mcp.Required(), mcp.MaxLength(500)),
		mcp.WithString("file_ref", mcp.Description("Optional file path reference (max 2000 chars)"), mcp.MaxLength(2000)),
		mcp.WithString("notes", mcp.Description("Optional notes (max 2000 chars)"), mcp.MaxLength(2000)),
	), s.handleChecklistAddItem)

	ms.AddTool(mcp.NewTool("task_checklist_toggle",
		mcp.WithDescription(
			"Partially updates a checklist item (done flag, title, notes, evidence_url). "+
				"Returns the full updated checklist.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("item_id", mcp.Description("Checklist item UUID"), mcp.Required()),
		mcp.WithBoolean("done", mcp.Description("Mark item done (true) or undone (false)"), mcp.Required()),
		mcp.WithString("evidence_url", mcp.Description("Optional URL or note proving the item is done"), mcp.MaxLength(2000)),
	), s.handleChecklistToggle)

	ms.AddTool(mcp.NewTool("task_checklist_complete",
		mcp.WithDescription(
			"Shorthand for marking a checklist item done=true and recording completed_at=now. "+
				"Returns the full updated checklist.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("item_id", mcp.Description("Checklist item UUID"), mcp.Required()),
	), s.handleChecklistComplete)
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
		RepoName:    stringArg(args, "repo_name"),
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

func (s *Server) handleUpdateProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawID := stringArg(args, "project_id")
	if rawID == "" {
		return mcp.NewToolResultError("project_id is required"), nil
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return mcp.NewToolResultError("invalid project_id UUID"), nil
	}

	// Load the existing project to fill omitted fields (preserve-on-omit semantics).
	existing, err := s.gtd.GetProjectByID(ctx, id)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("project not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading project: %v", err)), nil
	}

	p, toolErr := buildUpdateProjectParams(args, existing)
	if toolErr != "" {
		return mcp.NewToolResultError(toolErr), nil
	}

	project, err := s.gtd.UpdateProject(ctx, id, p)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("project not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("updating project: %v", err)), nil
	}
	return jsonText(project)
}

// buildUpdateProjectParams builds a gtd.UpdateProjectParams from MCP args,
// filling omitted fields from the existing project row. Returns a non-empty
// toolErr string on validation failure.
func buildUpdateProjectParams(args map[string]any, existing *db.Project) (gtd.UpdateProjectParams, string) {
	title := stringArg(args, "title")
	if title == "" {
		title = existing.Title
	}
	description := stringArg(args, "description")
	if description == "" && existing.Description.Valid {
		description = existing.Description.String
	}
	area := stringArg(args, "area")
	if area == "" {
		area = existing.Area
	}
	priority := int32(numberArg(args, "priority"))
	if priority == 0 {
		priority = existing.Priority
	}

	rawStatus := stringArg(args, "status")
	status := gtd.ProjectStatus(rawStatus)
	if rawStatus == "" {
		status = gtd.ProjectStatus(existing.Status)
	} else {
		switch status {
		case gtd.ProjectStatusActive, gtd.ProjectStatusCompleted, gtd.ProjectStatusArchived, gtd.ProjectStatusOnHold:
		default:
			return gtd.UpdateProjectParams{}, "status must be one of: active, completed, archived, on_hold"
		}
	}

	p := gtd.UpdateProjectParams{
		Title:       title,
		Description: description,
		Area:        area,
		Priority:    priority,
		Status:      status,
	}

	// goal_id: explicitly passed (even empty) → overwrite; absent → preserve.
	if goalErr := resolveGoalID(args, existing, &p); goalErr != "" {
		return gtd.UpdateProjectParams{}, goalErr
	}

	// repo_name: explicitly passed → overwrite (nil pointer = preserve existing).
	if _, ok := args["repo_name"]; ok {
		rn := stringArg(args, "repo_name")
		p.RepoName = &rn
	}

	return p, ""
}

// resolveGoalID applies goal_id from args to p, preserving the existing goal
// when goal_id is absent. Returns a non-empty string on parse failure.
func resolveGoalID(args map[string]any, existing *db.Project, p *gtd.UpdateProjectParams) string {
	if _, ok := args["goal_id"]; ok {
		rawGoal := stringArg(args, "goal_id")
		if rawGoal == "" {
			p.GoalID = nil // clear the link
			return ""
		}
		gid, parseErr := uuid.Parse(rawGoal)
		if parseErr != nil {
			return "invalid goal_id UUID"
		}
		p.GoalID = &gid
		return ""
	}
	if existing.GoalID.Valid {
		gid := uuid.UUID(existing.GoalID.Bytes)
		p.GoalID = &gid
	}
	return ""
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

// parseUpdateTaskArgs extracts and validates optional fields from MCP args into
// UpdateTaskParams. Returns (params, errMsg) — errMsg is non-empty on validation failure.
func parseUpdateTaskArgs(args map[string]any) (gtd.UpdateTaskParams, string) {
	p := gtd.UpdateTaskParams{}

	if rawStatus := stringArg(args, "status"); rawStatus != "" {
		status := gtd.TaskStatus(rawStatus)
		switch status {
		case gtd.TaskStatusPending, gtd.TaskStatusInProgress, gtd.TaskStatusCancelled:
		default:
			return p, "status must be one of: pending, in_progress, cancelled"
		}
		s := string(status)
		p.Status = &s
	}

	if title := stringArg(args, "title"); title != "" {
		p.Title = &title
	}
	if desc := stringArg(args, "description"); desc != "" {
		p.Description = &desc
	}

	if raw := numberArg(args, "priority"); raw > 0 {
		if raw > 5 {
			return p, "priority must be between 1 and 5"
		}
		v := int32(raw)
		p.Priority = &v
	}
	if raw := numberArg(args, "importance"); raw > 0 {
		if raw > 3 {
			return p, "importance must be 1, 2, or 3"
		}
		v := int16(raw)
		p.Importance = &v
	}

	if assignee := stringArg(args, "assignee"); assignee != "" {
		p.Assignee = &assignee
	}
	if rawDue := stringArg(args, "due_date"); rawDue != "" {
		t, parseErr := time.Parse(time.RFC3339, rawDue)
		if parseErr != nil {
			return p, "invalid due_date: must be RFC3339 (e.g. 2026-12-31T00:00:00Z)"
		}
		p.DueDate = &t
	}
	if taskCtx := stringArg(args, "context"); taskCtx != "" {
		p.Context = &taskCtx
	}

	return p, ""
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

	p, errMsg := parseUpdateTaskArgs(args)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}

	// At least one field must be provided.
	if p.Status == nil && p.Title == nil && p.Description == nil &&
		p.Priority == nil && p.Importance == nil && p.Assignee == nil &&
		p.DueDate == nil && p.Context == nil {
		return mcp.NewToolResultError("at least one field is required"), nil
	}

	task, err := s.gtd.UpdateTask(ctx, id, p)
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

		// Prune expired tokens and cap concurrent pending deletions.
		var count int
		s.deleteTokens.Range(func(k, v any) bool {
			rec := v.(deletionToken)
			if s.now().After(rec.expiresAt) {
				s.deleteTokens.Delete(k)
			} else {
				count++
			}
			return true
		})
		if count >= maxPendingDeletions {
			return mcp.NewToolResultError("too many pending deletions in flight; retry later"), nil
		}

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

func (s *Server) handleGetUpcomingWork(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	days := int(numberArg(args, "days"))
	if days <= 0 {
		days = 7
	}
	if days > 14 {
		days = 14
	}

	const limit = 50 // fetch max so caller sees the full picture
	now := time.Now()
	tasks, err := s.gtd.UpcomingTasks(ctx, now, days, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading upcoming tasks: %v", err)), nil
	}

	groups := gtd.GroupUpcomingTasks(tasks, now, time.UTC, days, limit)

	header := fmt.Sprintf("Upcoming tasks (next %d days)\n\n", days)
	body := renderUpcomingBuckets(groups)
	if body == "" {
		body = "No upcoming tasks found."
	}
	return mcp.NewToolResultText(header + body), nil
}

func (s *Server) handleChecklistAddItem(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	rawTaskID := stringArg(args, "task_id")
	if rawTaskID == "" {
		return mcp.NewToolResultError("task_id is required"), nil
	}
	taskID, err := uuid.Parse(rawTaskID)
	if err != nil {
		return mcp.NewToolResultError("invalid task_id UUID"), nil
	}

	title := stringArg(args, "title")
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}
	title = sanitiseMCPText(title)
	if title == "" {
		return mcp.NewToolResultError("title must not be blank after sanitisation"), nil
	}
	if len([]rune(title)) > gtd.ChecklistMaxTitle {
		return mcp.NewToolResultError("title exceeds 500 characters"), nil
	}

	fileRef := sanitiseMCPText(stringArg(args, "file_ref"))
	notes := sanitiseMCPText(stringArg(args, "notes"))
	if len([]rune(fileRef)) > gtd.ChecklistMaxText {
		return mcp.NewToolResultError("file_ref exceeds 2000 characters"), nil
	}
	if len([]rune(notes)) > gtd.ChecklistMaxText {
		return mcp.NewToolResultError("notes exceeds 2000 characters"), nil
	}

	wsID := workspaceUUIDFromPgtype(s.gtd.WorkspaceID())
	item := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     title,
		FileRef:   fileRef,
		Notes:     notes,
		CreatedAt: s.now().UTC(),
	}
	items, err := s.gtd.AddChecklistItem(ctx, taskID, wsID, item)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("adding checklist item: %v", err)), nil
	}
	return jsonText(items)
}

func (s *Server) handleChecklistToggle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	taskID, itemID, errMsg := parseChecklistIDs(args)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}

	done := boolArg(args, "done")
	evidenceURL := sanitiseMCPText(stringArg(args, "evidence_url"))
	if len([]rune(evidenceURL)) > gtd.ChecklistMaxText {
		return mcp.NewToolResultError("evidence_url exceeds 2000 characters"), nil
	}

	update := gtd.UpdateChecklistItemParams{Done: &done}
	if evidenceURL != "" {
		update.EvidenceURL = &evidenceURL
	}

	wsID := workspaceUUIDFromPgtype(s.gtd.WorkspaceID())
	items, err := s.gtd.UpdateChecklistItem(ctx, taskID, wsID, itemID, update)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task or item not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("toggling checklist item: %v", err)), nil
	}
	return jsonText(items)
}

func (s *Server) handleChecklistComplete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	taskID, itemID, errMsg := parseChecklistIDs(args)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}

	done := true
	update := gtd.UpdateChecklistItemParams{Done: &done}

	wsID := workspaceUUIDFromPgtype(s.gtd.WorkspaceID())
	items, err := s.gtd.UpdateChecklistItem(ctx, taskID, wsID, itemID, update)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task or item not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("completing checklist item: %v", err)), nil
	}
	return jsonText(items)
}

// parseChecklistIDs extracts and validates task_id and item_id from MCP args.
// Returns zero UUIDs and a non-empty errMsg on validation failure.
func parseChecklistIDs(args map[string]any) (taskID, itemID uuid.UUID, errMsg string) {
	rawTask := stringArg(args, "task_id")
	if rawTask == "" {
		return uuid.UUID{}, uuid.UUID{}, "task_id is required"
	}
	var err error
	taskID, err = uuid.Parse(rawTask)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, "invalid task_id UUID"
	}
	rawItem := stringArg(args, "item_id")
	if rawItem == "" {
		return uuid.UUID{}, uuid.UUID{}, "item_id is required"
	}
	itemID, err = uuid.Parse(rawItem)
	if err != nil {
		return uuid.UUID{}, uuid.UUID{}, "invalid item_id UUID"
	}
	return taskID, itemID, ""
}

// sanitiseMCPText strips null bytes and control characters from LLM-emitted text.
// MCP inputs are treated as hostile (adversarial LLM output).
func sanitiseMCPText(s string) string {
	return gtd.SanitiseChecklistText(s)
}

// workspaceUUIDFromPgtype converts a pgtype.UUID into a plain uuid.UUID.
// Invalid (zero) pgtype.UUID → zero uuid.UUID (unscoped mode).
func workspaceUUIDFromPgtype(pg pgtype.UUID) uuid.UUID {
	if !pg.Valid {
		return uuid.UUID{}
	}
	return uuid.UUID(pg.Bytes)
}

// renderUpcomingBuckets formats the 5 task buckets as plain text for MCP responses.
func renderUpcomingBuckets(groups gtd.UpcomingGroups) string {
	appendBucket := func(sb []byte, label string, bucket []db.Task) []byte {
		if len(bucket) == 0 {
			return sb
		}
		sb = append(sb, "### "+label+"\n"...)
		for _, t := range bucket {
			due := ""
			if t.DueDate.Valid {
				due = " [due: " + t.DueDate.Time.UTC().Format("2006-01-02") + "]"
			}
			sb = append(sb, fmt.Sprintf("- [%s] %s%s\n", t.Status, t.Title, due)...)
		}
		return append(sb, '\n')
	}
	var sb []byte
	sb = appendBucket(sb, "Today (including past-due)", groups.Today)
	sb = appendBucket(sb, "Tomorrow", groups.Tomorrow)
	sb = appendBucket(sb, "Day after tomorrow", groups.DayAfter)
	sb = appendBucket(sb, "Upcoming", groups.Upcoming)
	sb = appendBucket(sb, "High-importance (no due date)", groups.UnscheduledImportant)
	return string(sb)
}
