package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/outcome"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// strictVagueness reports whether WBT_STRICT_VAGUENESS is set to a truthy
// value in the server environment. Accepts the canonical strconv.ParseBool
// set ("1", "t", "T", "true", "True", "TRUE"). Never sourced from tool
// arguments (user-controlled).
func (s *Server) strictVagueness() bool {
	b, _ := strconv.ParseBool(os.Getenv("WBT_STRICT_VAGUENESS"))
	return b
}

// repoNameRe enforces a safe slug format for project repo_name values passed
// through MCP tools. Keeps the column semantically queryable and prevents
// control characters or path-traversal sequences from entering the DB.
var repoNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.\-]{1,100}$`)

// githubPRURLRe and commitSHARe are canonical definitions in the shared
// internal/validator and internal/gtd packages respectively. Package-level
// aliases keep call-sites in this file unchanged while eliminating duplication.
var (
	githubPRURLRe = validator.GitHubPRURLRe
	commitSHARe   = gtd.CommitSHARe
)

// branchNameHasControlChars returns true if s contains characters invalid in
// git branch names: ASCII control chars (< 0x20), DEL (0x7F), or Unicode
// "Other" characters (Cc control, Cf format like U+200B zero-width space /
// U+FEFF BOM, Co private-use, Cs surrogates).
func branchNameHasControlChars(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7F || unicode.Is(unicode.C, r) {
			return true
		}
	}
	return false
}

// resolveAssigneeArg reads the "assignee" arg and, when non-empty, resolves
// it to a canonical actor via gtd.NormalizeActor. Empty input is allowed
// (many tasks start unowned) and resolves to "". Returns a non-empty error
// message on validation failure — whitelist, not blacklist
// (backend-security-design.md §2.1: LLM tool input is adversarial; an
// unvalidated assignee corrupts the "who is working on what" audit trail).
func resolveAssigneeArg(args map[string]any) (string, string) {
	raw := stringArg(args, "assignee")
	if raw == "" {
		return "", ""
	}
	normalized, err := gtd.NormalizeActor(raw)
	if err != nil {
		return "", err.Error()
	}
	return normalized, ""
}

// applyProjectIDArg parses the optional "project_id" arg and sets
// p.ProjectID when present. Returns a non-empty error message on an invalid
// UUID. Extracted from handleAddTask to keep its cyclomatic complexity
// within the gocyclo budget (mirrors the applyBranchAndPR pattern below).
func applyProjectIDArg(args map[string]any, p *gtd.CreateTaskParams) string {
	raw := stringArg(args, "project_id")
	if raw == "" {
		return ""
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return errMsgInvalidProjectIDUUID
	}
	p.ProjectID = &id
	return ""
}

// parseImportanceArg extracts and validates the optional "importance" arg
// (1=high, 2=med, 3=low), shared by add_task and update_task so the range
// check isn't duplicated. Returns (nil, "") when the arg is absent, or a
// non-empty error message on an out-of-range value.
func parseImportanceArg(args map[string]any) (*int16, string) {
	imp := numberArg(args, "importance")
	if imp <= 0 {
		return nil, ""
	}
	if imp > 3 {
		return nil, "importance must be 1, 2, or 3"
	}
	v := int16(imp)
	return &v, ""
}

// errMsgBranchNameTooLong, errMsgBranchNameControlChars and errMsgInvalidPRURL
// are the shared validation messages for branch_name/pr_url, hoisted to
// package-level constants because add_task (applyBranchAndPR), update_task
// (applyBranchAndPRUpdate), and begin_task (validateBeginTaskLinkageArgs)
// all enforce the identical rule (goconst).
const (
	errMsgBranchNameTooLong      = "branch_name must not exceed 255 characters"
	errMsgBranchNameControlChars = "branch_name must not contain control characters"
	errMsgInvalidPRURL           = "pr_url must be a valid GitHub PR URL (https://github.com/owner/repo/pull/N)"
)

// applyBranchAndPR validates and sets branch_name and pr_url on a CreateTaskParams.
// Returns a non-empty error message on validation failure.
func applyBranchAndPR(args map[string]any, p *gtd.CreateTaskParams) string {
	if bn := stringArg(args, "branch_name"); bn != "" {
		if len(bn) > 255 {
			return errMsgBranchNameTooLong
		}
		if branchNameHasControlChars(bn) {
			return errMsgBranchNameControlChars
		}
		p.BranchName = &bn
	}
	if pu := stringArg(args, "pr_url"); pu != "" {
		if !githubPRURLRe.MatchString(pu) {
			return errMsgInvalidPRURL
		}
		p.PRUrl = &pu
	}
	return ""
}

// applyBranchAndPRUpdate validates and sets branch_name and pr_url on an UpdateTaskParams.
// Explicit empty string clears the field; absent key leaves it unchanged.
// Returns a non-empty error message on validation failure.
func applyBranchAndPRUpdate(args map[string]any, p *gtd.UpdateTaskParams) string {
	if _, ok := args["branch_name"]; ok {
		bn := stringArg(args, "branch_name")
		if bn != "" {
			if len(bn) > 255 {
				return errMsgBranchNameTooLong
			}
			if branchNameHasControlChars(bn) {
				return errMsgBranchNameControlChars
			}
		}
		p.BranchName = &bn
	}
	if _, ok := args["pr_url"]; ok {
		pu := stringArg(args, "pr_url")
		if pu != "" && !githubPRURLRe.MatchString(pu) {
			return errMsgInvalidPRURL
		}
		p.PRUrl = &pu
	}
	return ""
}

// maxPendingDeletions caps the number of valid (non-expired) delete tokens
// held simultaneously. This prevents a loop caller from growing the sync.Map
// without bound if tokens are issued faster than they expire.
const maxPendingDeletions = 256

func (s *Server) registerGTDTools(ms *server.MCPServer) {
	ms.AddTool(mcp.NewTool(
		"list_projects",
		mcp.WithDescription("Returns all active projects."),
	), s.handleListProjects)

	ms.AddTool(mcp.NewTool(
		"create_project",
		mcp.WithDescription("Creates a new project."),
		mcp.WithString("name", mcp.Description("Short slug identifier"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Display title"), mcp.Required()),
		mcp.WithString("area", mcp.Description("Work area (e.g. engineering, personal)"), mcp.Required()),
		mcp.WithString("description", mcp.Description("Detailed description")),
		mcp.WithString("goal_id", mcp.Description("Parent goal UUID")),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5, lower is higher")),
		mcp.WithString("repo_name", mcp.Description("VCS repository slug to link this project (e.g. wayneblacktea)")),
	), s.handleCreateProject)

	ms.AddTool(mcp.NewTool(
		"update_project",
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

	ms.AddTool(mcp.NewTool(
		"list_tasks",
		mcp.WithDescription("Lists tasks, optionally filtered by project and status. "+
			"Supports pagination via limit and offset. "+
			"Set summary=false to receive full task objects instead of compact summaries."),
		mcp.WithString("project_id", mcp.Description("Filter by project UUID")),
		mcp.WithBoolean("summary", mcp.Description("Return compact task summaries (default true); false → full task objects")),
		mcp.WithString("status",
			mcp.Description("Filter by status (default: active = pending + in_progress; all = every status)"),
			mcp.Enum("active", "all", "pending", "in_progress", "completed", "cancelled")),
		mcp.WithNumber("limit", mcp.Description("Max results per page (default 50, max 200)")),
		mcp.WithNumber("offset", mcp.Description("Pagination offset (default 0)")),
	), s.handleListTasks)

	ms.AddTool(mcp.NewTool(
		"add_task",
		mcp.WithDescription(
			"CALL immediately when follow-up work is identified during discussion. "+
				"Creates a task optionally under a project.",
		),
		mcp.WithString("title", mcp.Description("Task title"), mcp.Required()),
		mcp.WithString("project_id", mcp.Description("Parent project UUID")),
		mcp.WithString("description", mcp.Description("Task details")),
		// mcp.MaxLength(100) is a defensive client-side upper bound
		// (backend-security-design.md §2: LLM tool input is hostile) against
		// an unbounded assignee string reaching this handler. It is NOT the
		// validation authority: gtd.NormalizeActor's canonical-actor
		// allowlist (handleAddTask below) still rejects any raw value that
		// isn't a known actor/alias regardless of length, so an
		// mcp-go-runtime bypass of this hint (it's advisory-only, per
		// tools_worksession.go's established pattern) still fails closed.
		mcp.WithString("assignee", mcp.Description("Who owns this task"), mcp.MaxLength(100)),
		mcp.WithNumber("priority", mcp.Description("Priority 1-5 (execution order, lower runs first)")),
		mcp.WithNumber("importance", mcp.Description("Importance 1-3 (1=high, 2=med, 3=low) — distinct from priority")),
		mcp.WithString("context", mcp.Description("Free-form discussion background — why this task came up")),
		mcp.WithString("kind",
			mcp.Description("Task kind: general, fix-pr, feature, refactor, research, or chore. Defaults to 'general'."),
			mcp.Enum("general", "fix-pr", "feature", "refactor", "research", "chore")),
		mcp.WithString("branch_name", mcp.Description("Git branch name associated with this task (e.g. feature/my-feature)")),
		mcp.WithString("pr_url", mcp.Description("GitHub PR URL for this task (e.g. https://github.com/org/repo/pull/123)")),
		mcp.WithString("due_date", mcp.Description("Due date in RFC3339 format (required, e.g. 2026-12-31T00:00:00Z)")),
	), s.handleAddTask)

	ms.AddTool(mcp.NewTool(
		"complete_task",
		mcp.WithDescription(
			"CALL after task is verified done (build pass, tests pass). Marks task completed and records artifact. "+
				"If artifact is a GitHub PR URL (https://github.com/.../pull/N) it is also stored as pr_url. "+
				"If artifact is a 40-character hex SHA it is appended to commit_shas.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("artifact", mcp.Description("Link or note for the output (PR URL or commit SHA auto-detected)")),
	), s.handleCompleteTask)

	ms.AddTool(mcp.NewTool(
		"list_goals",
		mcp.WithDescription("Returns all active goals ordered by due date."),
	), s.handleListGoals)

	ms.AddTool(mcp.NewTool(
		"create_goal",
		mcp.WithDescription("Creates a new goal."),
		mcp.WithString("title", mcp.Description("Goal title"), mcp.Required()),
		mcp.WithString("area", mcp.Description("Life area (e.g. career, health, personal)"), mcp.Required()),
		mcp.WithString("description", mcp.Description("Detailed description")),
		mcp.WithString("due_date", mcp.Description("Target date in RFC3339 format (e.g. 2026-12-31T00:00:00Z)")),
	), s.handleCreateGoal)

	ms.AddTool(mcp.NewTool(
		"update_task",
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
		mcp.WithString("branch_name", mcp.Description("Git branch name (empty string clears the field)")),
		mcp.WithString("pr_url", mcp.Description("GitHub PR URL (empty string clears the field)")),
	), s.handleUpdateTask)

	ms.AddTool(mcp.NewTool(
		"update_project_status",
		mcp.WithDescription("Updates the status of a project."),
		mcp.WithString("project_id", mcp.Description("Project UUID"), mcp.Required()),
		mcp.WithString("status", mcp.Description("New status: active, completed, archived, or on_hold"), mcp.Required()),
	), s.handleUpdateProjectStatus)

	ms.AddTool(mcp.NewTool(
		"get_project",
		mcp.WithDescription("Returns a project by name with its recent decisions."),
		mcp.WithString("name", mcp.Description("Project slug name"), mcp.Required()),
	), s.handleGetProject)

	ms.AddTool(mcp.NewTool(
		"log_activity",
		mcp.WithDescription("Records an activity log entry for a project."),
		mcp.WithString("actor", mcp.Description("Who did the action (e.g. claude-code, human)"), mcp.Required()),
		mcp.WithString("action", mcp.Description("What was done"), mcp.Required()),
		mcp.WithString("project_id", mcp.Description("Project UUID (optional)")),
		mcp.WithString("notes", mcp.Description("Additional notes")),
	), s.handleLogActivity)

	ms.AddTool(mcp.NewTool(
		"get_upcoming_work",
		mcp.WithDescription(
			"Returns pending/in-progress tasks grouped into today, tomorrow, day_after, "+
				"upcoming, and unscheduled_important buckets. Use to plan the next work "+
				"session or surface high-importance tasks that have no due date.",
		),
		mcp.WithNumber("days", mcp.Description("How many days ahead to include (1-14, default 7)")),
	), s.handleGetUpcomingWork)

	ms.AddTool(mcp.NewTool(
		"delete_task",
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

	ms.AddTool(mcp.NewTool(
		"task_checklist_add_item",
		mcp.WithDescription(
			"Appends a new checklist item to a task. Returns the full updated checklist. "+
				"Use to track sub-steps or acceptance criteria for a task.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("title", mcp.Description("Item title (max 500 chars)"), mcp.Required(), mcp.MaxLength(500)),
		mcp.WithString("file_ref", mcp.Description("Optional file path reference (max 2000 chars)"), mcp.MaxLength(2000)),
		mcp.WithString("notes", mcp.Description("Optional notes (max 2000 chars)"), mcp.MaxLength(2000)),
	), s.handleChecklistAddItem)

	ms.AddTool(mcp.NewTool(
		"task_checklist_toggle",
		mcp.WithDescription(
			"Partially updates a checklist item (done flag, title, notes, evidence_url). "+
				"Returns the full updated checklist.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("item_id", mcp.Description("Checklist item UUID"), mcp.Required()),
		mcp.WithBoolean("done", mcp.Description("Mark item done (true) or undone (false)"), mcp.Required()),
		mcp.WithString("evidence_url", mcp.Description("Optional URL or note proving the item is done"), mcp.MaxLength(2000)),
	), s.handleChecklistToggle)

	ms.AddTool(mcp.NewTool(
		"task_checklist_complete",
		mcp.WithDescription(
			"Shorthand for marking a checklist item done=true and recording completed_at=now. "+
				"Returns the full updated checklist.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("item_id", mcp.Description("Checklist item UUID"), mcp.Required()),
	), s.handleChecklistComplete)

	ms.AddTool(mcp.NewTool(
		"get_task",
		mcp.WithDescription(
			"Returns a single task by UUID. Status-agnostic — retrieves pending, "+
				"in_progress, completed, and cancelled tasks. Use list_tasks for "+
				"filtered bulk retrieval.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
	), s.handleGetTask)

	ms.AddTool(mcp.NewTool(
		"set_task_status",
		mcp.WithDescription(
			"Transitions a task to a new status, including reopen (completed/cancelled → pending/in_progress). "+
				"Same-to-same status is an idempotent no-op. Allowed transitions: "+
				"pending↔in_progress, pending→completed/cancelled, in_progress→completed/cancelled, "+
				"completed/cancelled→pending/in_progress/completed/cancelled. "+
				"IMPORTANT: This tool MUST NOT call record_outcome or evaluate_outcome — "+
				"outcome recording stays exclusively in those tools. Reopen→re-complete records no outcome.",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("status",
			mcp.Description("New status"),
			mcp.Required(),
			mcp.Enum("pending", "in_progress", "completed", "cancelled")),
	), s.handleSetTaskStatus)

	ms.AddTool(mcp.NewTool(
		"begin_task",
		mcp.WithDescription(
			"Atomically marks a task in_progress, logs a work_session_started activity, "+
				"and returns the task with a branch_name_suggestion and work_session_id. "+
				"Call this INSTEAD of update_task when starting work on a task. "+
				"Optionally pass branch_name and/or pr_url to persist the linkage so "+
				"reconcile_merged_prs can auto-close the task on PR merge "+
				"(sprint feature/gtd-enforce-server-side GTD-fix 8/12).",
		),
		mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		mcp.WithString("branch_name",
			mcp.Description("Optional git branch name to persist on the task (e.g. feature/my-feature). "+
				"Pass this when you already know the branch so the task can be linked for auto-close.")),
		mcp.WithString("pr_url",
			mcp.Description("Optional GitHub PR URL to persist on the task (e.g. https://github.com/org/repo/pull/123). "+
				"Pass this once the PR is open so reconcile_merged_prs can match on merge.")),
		mcp.WithString("assignee",
			mcp.Description("Who is starting this task. Required unless the task already has an assignee — "+
				"a task cannot enter in_progress without a known owner (p6-7).")),
	), s.handleBeginTask)
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

	repoName := stringArg(args, "repo_name")
	if repoName != "" && !repoNameRe.MatchString(repoName) {
		return mcp.NewToolResultError("repo_name must match [a-zA-Z0-9_.-]{1,100}"), nil
	}
	p := gtd.CreateProjectParams{
		Name:        name,
		Title:       title,
		Area:        area,
		Description: stringArg(args, "description"),
		Priority:    numberArg(args, "priority"),
		RepoName:    repoName,
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
	id, errResult := requireUUIDArg(args, "project_id", errMsgInvalidProjectIDUUID)
	if errResult != nil {
		return errResult, nil
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
		if rn != "" && !repoNameRe.MatchString(rn) {
			return gtd.UpdateProjectParams{}, "repo_name must match [a-zA-Z0-9_.-]{1,100}"
		}
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

// taskSummary is the compact representation returned by list_tasks when
// summary=true (the default). Exactly 9 fields match the spec.
type taskSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Priority   int32  `json:"priority"`
	Importance *int16 `json:"importance,omitempty"`
	DueDate    string `json:"due_date,omitempty"`
	Assignee   string `json:"assignee,omitempty"`
	Kind       string `json:"kind"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// toTaskSummary converts a db.Task to the compact taskSummary wire format.
func toTaskSummary(t db.Task) taskSummary {
	ts := taskSummary{
		ID:       t.ID.String(),
		Title:    t.Title,
		Status:   t.Status,
		Priority: t.Priority,
		Kind:     t.Kind,
	}
	if t.Importance.Valid {
		v := t.Importance.Int16
		ts.Importance = &v
	}
	if t.DueDate.Valid {
		ts.DueDate = t.DueDate.Time.UTC().Format(time.RFC3339)
	}
	if t.Assignee.Valid && t.Assignee.String != "" {
		ts.Assignee = t.Assignee.String
	}
	if t.UpdatedAt.Valid {
		ts.UpdatedAt = t.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return ts
}

func (s *Server) handleListTasks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()

	// status: validate against allowed enum; default "active"
	rawStatus := stringArg(args, "status")
	if rawStatus == "" {
		rawStatus = "active"
	}
	switch rawStatus {
	case "active", "all", taskStatusPending, taskStatusInProgress, taskStatusCompleted, "cancelled":
	default:
		return mcp.NewToolResultError(
			"status must be one of: active, all, pending, in_progress, completed, cancelled",
		), nil
	}

	// limit: <=0 → 50; clamp to 200
	limit := int(numberArg(args, "limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// offset: <0 → 0
	offset := int(numberArg(args, "offset"))
	if offset < 0 {
		offset = 0
	}

	// summary: absent → true (default)
	summaryMode := true
	if _, ok := args["summary"]; ok {
		summaryMode = boolArg(args, "summary")
	}

	var projectID *uuid.UUID
	if raw := stringArg(args, "project_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
		}
		projectID = &id
	}

	// Fetch limit+1 rows to detect has_more without a COUNT query.
	effLimit := limit + 1
	f := gtd.TaskFilter{
		ProjectID: projectID,
		Status:    rawStatus,
		Limit:     effLimit,
		Offset:    offset,
	}
	rows, err := s.gtd.TasksFiltered(ctx, f)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading tasks: %v", err)), nil
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	var tasks any
	if summaryMode {
		summaries := make([]taskSummary, 0, len(rows))
		for _, t := range rows {
			summaries = append(summaries, toTaskSummary(t))
		}
		tasks = summaries
	} else {
		tasks = rows
	}

	return jsonText(map[string]any{
		"tasks":    tasks,
		"returned": len(rows),
		"limit":    limit,
		"offset":   offset,
		"has_more": hasMore,
		"status":   rawStatus,
		"summary":  summaryMode,
	})
}

// handleGetTask returns a single task by UUID. Status-agnostic — retrieves
// pending, in_progress, completed, and cancelled tasks alike.
func (s *Server) handleGetTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	id, errResult := requireUUIDArg(args, "task_id", errMsgInvalidTaskIDUUID)
	if errResult != nil {
		return errResult, nil
	}
	task, err := s.gtd.GetTaskByID(ctx, id)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading task: %v", err)), nil
	}
	return jsonText(task)
}

// allowedTransitions maps each source status to the set of valid target statuses.
// Same-to-same is handled separately as an idempotent no-op.
//
// IMPORTANT: set_task_status MUST NOT call any outcome path. record_outcome and
// evaluate_outcome are the only outcome writers. Reopen→re-complete records no
// outcome — do NOT add an outcome side-effect here in the future.
var allowedTransitions = map[string]map[string]bool{
	"pending":     {"in_progress": true, "completed": true, "cancelled": true},
	"in_progress": {"pending": true, "completed": true, "cancelled": true},
	"completed":   {"pending": true, "in_progress": true, "cancelled": true},
	"cancelled":   {"pending": true, "in_progress": true, "completed": true},
}

// handleSetTaskStatus transitions a task to a new status. Idempotent for
// same-to-same. Covers reopen (completed/cancelled → pending/in_progress).
func (s *Server) handleSetTaskStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	id, errResult := requireUUIDArg(args, "task_id", errMsgInvalidTaskIDUUID)
	if errResult != nil {
		return errResult, nil
	}

	rawStatus := stringArg(args, "status")
	switch rawStatus {
	case taskStatusPending, taskStatusInProgress, taskStatusCompleted, "cancelled":
	default:
		return mcp.NewToolResultError(
			"status must be one of: pending, in_progress, completed, cancelled",
		), nil
	}

	cur, err := s.gtd.GetTaskByID(ctx, id)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading task: %v", err)), nil
	}

	// Idempotent no-op: same → same, no write.
	if cur.Status == rawStatus {
		return jsonText(cur)
	}

	// Guard invalid transitions.
	if !allowedTransitions[cur.Status][rawStatus] {
		return mcp.NewToolResultError(fmt.Sprintf(
			"invalid transition %s → %s: allowed targets are %v",
			cur.Status, rawStatus, allowedTargets(cur.Status),
		)), nil
	}

	updated, err := s.gtd.UpdateTaskStatus(ctx, id, gtd.TaskStatus(rawStatus))
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("updating task status: %v", err)), nil
	}
	return jsonText(updated)
}

// allowedTargets returns a sorted list of allowed target statuses for errMsg.
func allowedTargets(fromStatus string) []string {
	targets := allowedTransitions[fromStatus]
	out := make([]string, 0, len(targets))
	for t := range targets {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// parseRequiredDueDate extracts and validates the RFC3339 due_date argument.
// Returns the parsed time and nil error result on success; returns nil time and
// a non-nil CallToolResult (error) when validation fails.
func parseRequiredDueDate(args map[string]any) (*time.Time, *mcp.CallToolResult) {
	rawDue := stringArg(args, "due_date")
	if rawDue == "" {
		return nil, mcp.NewToolResultError("due_date is required (RFC3339, e.g. 2026-12-31T00:00:00Z)")
	}
	dueTime, parseErr := time.Parse(time.RFC3339, rawDue)
	if parseErr != nil {
		return nil, mcp.NewToolResultError("invalid due_date: must be RFC3339 (e.g. 2026-12-31T00:00:00Z)")
	}
	return &dueTime, nil
}

func (s *Server) handleAddTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	title := stringArg(args, "title")
	if title == "" {
		return mcp.NewToolResultError("title is required"), nil
	}

	kind := stringArg(args, "kind")
	if kind == "" {
		kind = validator.KindGeneral
	}
	if !validator.IsValidKind(kind) {
		return mcp.NewToolResultError("kind must be one of: general, fix-pr, feature, refactor, research, chore"), nil
	}

	description := stringArg(args, "description")

	// Vagueness and kind-field checks. For MCP, warnings are embedded in the
	// result JSON body (no HTTP headers available). Strict mode → tool error.
	var allWarnings []string
	allWarnings = append(allWarnings, validator.CheckVagueness("description", description, kind)...)
	allWarnings = append(allWarnings, validator.CheckKindFields(kind, description)...)
	if len(allWarnings) > 0 && s.strictVagueness() {
		return mcp.NewToolResultError(fmt.Sprintf("vagueness check failed: %v", allWarnings)), nil
	}

	assignee, assigneeErr := resolveAssigneeArg(args)
	if assigneeErr != "" {
		return mcp.NewToolResultError(assigneeErr), nil
	}

	p := gtd.CreateTaskParams{
		Title:       title,
		Description: description,
		Assignee:    assignee,
		Priority:    numberArg(args, "priority"),
		Context:     stringArg(args, "context"),
		Kind:        kind,
	}
	if msg := applyProjectIDArg(args, &p); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}
	imp, impErr := parseImportanceArg(args)
	if impErr != "" {
		return mcp.NewToolResultError(impErr), nil
	}
	p.Importance = imp
	if msg := applyBranchAndPR(args, &p); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	// due_date is required in add_task (OVERRIDE 1: hard-require at tool layer only).
	// Other CreateTask callers (promote_vision_to_task, reconcile, HTTP, CLI, Discord)
	// call the store directly and are NOT affected by this check.
	dueTime, dueErr := parseRequiredDueDate(args)
	if dueErr != nil {
		return dueErr, nil
	}
	p.DueDate = dueTime

	task, err := s.gtd.CreateTask(ctx, p)
	if err != nil {
		slog.Warn("add_task: CreateTask failed", "title", title, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("creating task: %v", err)), nil
	}

	// Embed warnings in the result body when present.
	if len(allWarnings) > 0 {
		return jsonText(map[string]any{"task": task, "warnings": allWarnings})
	}
	return jsonText(task)
}

func (s *Server) handleCompleteTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	id, errResult := requireUUIDArg(args, "task_id", errMsgInvalidTaskIDUUID)
	if errResult != nil {
		return errResult, nil
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
		slog.Warn("complete_task: CompleteTask failed", "task_id", id, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("completing task: %v", err)), nil
	}

	// Auto-parse artifact: PR URL → set pr_url; 40-hex SHA → append to commit_shas.
	// SECURITY: we only store the strings, never fetch the URLs.
	if artifact != nil && *artifact != "" {
		if updated := s.applyArtifactSideEffects(ctx, id, task, strings.TrimSpace(*artifact)); updated != nil {
			task = updated
		}
	}

	s.seedDraftOutcome(ctx, id)

	return jsonText(task)
}

// seedDraftOutcome best-effort records a result:"unknown" outcome for a
// just-completed task so the outcome→evaluate_outcome→behavior_governance
// learning loop always has fuel, even when the agent never calls
// record_outcome itself. Idempotent: outcomes has no unique constraint on
// (entity_type, entity_id) (migration 000054), so a duplicate-check via
// ExistsForEntity guards against complete_task being called more than once
// for the same task. Failures are logged and swallowed — complete_task has
// already succeeded by the time this runs and must never fail because of it.
func (s *Server) seedDraftOutcome(ctx context.Context, taskID uuid.UUID) {
	if s.outcome == nil {
		return
	}
	wsID := s.workspaceUUID()
	exists, err := s.outcome.ExistsForEntity(ctx, wsID, "task", taskID)
	if err != nil {
		slog.Warn("complete_task: outcome.ExistsForEntity failed (non-fatal, skipping seed)", "task_id", taskID, "err", err)
		return
	}
	if exists {
		return
	}
	if _, err := s.outcome.CreateOutcome(ctx, outcome.CreateOutcomeParams{
		WorkspaceID: wsID,
		EntityType:  "task",
		EntityID:    taskID,
		Result:      "unknown",
	}); err != nil {
		slog.Warn("complete_task: seeding draft outcome failed (non-fatal)", "task_id", taskID, "err", err)
	}
}

// applyArtifactSideEffects detects whether artifact is a GitHub PR URL or a
// 40-hex commit SHA and applies the corresponding side-effect update on the
// task. Returns the updated task on success, nil if no side-effect applied or
// the update failed (caller uses the original completed task in that case).
// SECURITY: only stores the URL string, never makes an HTTP fetch.
func (s *Server) applyArtifactSideEffects(ctx context.Context, id uuid.UUID, task *db.Task, artifact string) *db.Task {
	var up gtd.UpdateTaskParams
	var artifactKind string
	if githubPRURLRe.MatchString(artifact) {
		up.PRUrl = &artifact
		artifactKind = "pr_url"
	} else if commitSHARe.MatchString(artifact) {
		newSHAs := make([]string, len(task.CommitSHAs)+1)
		copy(newSHAs, task.CommitSHAs)
		newSHAs[len(task.CommitSHAs)] = artifact
		up.CommitSHAs = newSHAs
		artifactKind = "commit_sha"
	}
	if up.PRUrl == nil && up.CommitSHAs == nil {
		return nil
	}
	updated, updateErr := s.gtd.UpdateTask(ctx, id, up)
	if updateErr != nil {
		// Not propagated to the caller — complete_task already succeeded and
		// returns the pre-side-effect task. Logged so operators can see the
		// artifact link silently failed to attach. Never log artifact itself
		// (may be a URL/SHA); artifactKind is enough to diagnose.
		slog.Warn("applyArtifactSideEffects: UpdateTask failed", "task_id", id, "artifact_kind", artifactKind, "err", updateErr)
		return nil
	}
	return updated
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
	imp, impErr := parseImportanceArg(args)
	if impErr != "" {
		return p, impErr
	}
	p.Importance = imp

	if assignee := stringArg(args, "assignee"); assignee != "" {
		normalized, normErr := gtd.NormalizeActor(assignee)
		if normErr != nil {
			return p, normErr.Error()
		}
		p.Assignee = &normalized
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

	if msg := applyBranchAndPRUpdate(args, &p); msg != "" {
		return p, msg
	}

	return p, ""
}

// updateTaskParamsIsEmpty returns true when no field is set in p — the caller
// must reject the request when all fields are nil/empty to avoid a no-op write.
func updateTaskParamsIsEmpty(p gtd.UpdateTaskParams) bool {
	return p.Status == nil && p.Title == nil && p.Description == nil &&
		p.Priority == nil && p.Importance == nil && p.Assignee == nil &&
		p.DueDate == nil && p.Context == nil &&
		p.BranchName == nil && p.PRUrl == nil && p.CommitSHAs == nil
}

// requireAssigneeForInProgress enforces that a task cannot transition to
// in_progress without a known owner (p6-6: multi-AI collaboration needs a
// reliable answer to "who is working on this"). A no-op unless this call is
// actually setting status=in_progress. When the call itself sets assignee,
// that's sufficient — no extra read needed. Otherwise the existing row is
// consulted: a task that already carries a valid assignee may re-enter
// in_progress without repeating it. Returns a non-nil tool-error result when
// neither this call nor the existing row have an assignee.
func (s *Server) requireAssigneeForInProgress(ctx context.Context, id uuid.UUID, p gtd.UpdateTaskParams) *mcp.CallToolResult {
	if p.Status == nil || *p.Status != string(gtd.TaskStatusInProgress) {
		return nil
	}
	if p.Assignee != nil && strings.TrimSpace(*p.Assignee) != "" {
		return nil
	}
	existing, err := s.gtd.GetTaskByID(ctx, id)
	if err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return mcp.NewToolResultError("task not found")
		}
		slog.Warn("update_task: GetTaskByID failed while checking assignee for in_progress", "task_id", id, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("checking task before in_progress transition: %v", err))
	}
	if existing.Assignee.Valid && strings.TrimSpace(existing.Assignee.String) != "" {
		return nil
	}
	return mcp.NewToolResultError("assignee is required when moving a task to in_progress; set assignee on this call or beforehand")
}

func (s *Server) handleUpdateTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	id, errResult := requireUUIDArg(args, "task_id", errMsgInvalidTaskIDUUID)
	if errResult != nil {
		return errResult, nil
	}

	p, errMsg := parseUpdateTaskArgs(args)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if updateTaskParamsIsEmpty(p) {
		return mcp.NewToolResultError("at least one field is required"), nil
	}
	if errResult := s.requireAssigneeForInProgress(ctx, id, p); errResult != nil {
		return errResult, nil
	}

	task, err := s.gtd.UpdateTask(ctx, id, p)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		slog.Warn("update_task: UpdateTask failed", "task_id", id, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("updating task: %v", err)), nil
	}
	return jsonText(task)
}

func (s *Server) handleUpdateProjectStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	id, errResult := requireUUIDArg(args, "project_id", errMsgInvalidProjectIDUUID)
	if errResult != nil {
		return errResult, nil
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
			return mcp.NewToolResultError(errMsgInvalidProjectIDUUID), nil
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
	id, errResult := requireUUIDArg(args, "task_id", errMsgInvalidTaskIDUUID)
	if errResult != nil {
		return errResult, nil
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
		slog.Warn("delete_task: DeleteTask failed", "task_id", id, "err", err)
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
	taskID, errResult := requireUUIDArg(args, "task_id", errMsgInvalidTaskIDUUID)
	if errResult != nil {
		return errResult, nil
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

	if _, ok := args["done"]; !ok {
		return mcp.NewToolResultError("done is required"), nil
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

// validateBeginTaskLinkageArgs checks the optional branch_name/pr_url args
// BEFORE handleBeginTask mutates anything, so a validation failure never
// leaves the task half-mutated (sprint feature/gtd-enforce-server-side
// GTD-fix 8/12). Extracted from handleBeginTask to keep its cyclomatic
// complexity within the gocyclo budget. Returns a non-empty error message on
// validation failure.
func validateBeginTaskLinkageArgs(branchName, prURL string) string {
	if branchName != "" {
		if len(branchName) > 255 {
			return errMsgBranchNameTooLong
		}
		if branchNameHasControlChars(branchName) {
			return errMsgBranchNameControlChars
		}
	}
	if prURL != "" && !githubPRURLRe.MatchString(prURL) {
		return errMsgInvalidPRURL
	}
	return ""
}

// persistAssigneeBeforeBegin saves a caller-supplied assignee BEFORE
// handleBeginTask calls BeginTask (not after, unlike branch_name/pr_url):
// BeginTask's domain-layer in_progress guard (gtd.RequireAssigneeForInProgress)
// reads the EXISTING row, so the assignee must already be there by the time
// BeginTask runs. A task with no assignee (neither on this call nor already
// on the row) is rejected by that guard — this early-fail check just gives a
// friendlier message (mirrors requireAssigneeForInProgress for update_task).
// A no-op when assignee is empty. Returns a non-nil tool-error result on
// failure.
func (s *Server) persistAssigneeBeforeBegin(ctx context.Context, id uuid.UUID, idStr, assignee string) *mcp.CallToolResult {
	if assignee == "" {
		return nil
	}
	if _, err := s.gtd.UpdateTask(ctx, id, gtd.UpdateTaskParams{Assignee: &assignee}); err != nil {
		if errors.Is(err, gtd.ErrNotFound) {
			return mcp.NewToolResultError("task not found: " + idStr)
		}
		return mcp.NewToolResultError(fmt.Sprintf("persisting assignee before begin: %v", err))
	}
	return nil
}

// persistBeginTaskLinkage saves branch_name/pr_url AFTER BeginTask has
// already flipped the status, so we don't disturb BeginTask's idempotency
// contract. A no-op (returns task unchanged) when neither arg is set.
// Returns a non-nil tool-error result on failure.
func (s *Server) persistBeginTaskLinkage(
	ctx context.Context, id uuid.UUID, task *db.Task, branchName, prURL string,
) (*db.Task, *mcp.CallToolResult) {
	if branchName == "" && prURL == "" {
		return task, nil
	}
	up := gtd.UpdateTaskParams{}
	if branchName != "" {
		up.BranchName = &branchName
	}
	if prURL != "" {
		up.PRUrl = &prURL
	}
	updated, err := s.gtd.UpdateTask(ctx, id, up)
	if err != nil {
		return task, mcp.NewToolResultError(fmt.Sprintf("beginning task linkage persist: %v", err))
	}
	return updated, nil
}

func (s *Server) handleBeginTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	idStr := stringArg(args, "task_id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		return mcp.NewToolResultError("invalid task_id: " + err.Error()), nil
	}

	branchName := stringArg(args, "branch_name")
	prURL := stringArg(args, "pr_url")
	if msg := validateBeginTaskLinkageArgs(branchName, prURL); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	assignee, assigneeErr := resolveAssigneeArg(args)
	if assigneeErr != "" {
		return mcp.NewToolResultError(assigneeErr), nil
	}
	if errResult := s.persistAssigneeBeforeBegin(ctx, id, idStr, assignee); errResult != nil {
		return errResult, nil
	}

	task, err := s.gtd.BeginTask(ctx, id)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found: " + idStr), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("beginning task: %v", err)), nil
	}

	task, errResult := s.persistBeginTaskLinkage(ctx, id, task, branchName, prURL)
	if errResult != nil {
		return errResult, nil
	}

	return jsonText(map[string]any{
		"task":                   task,
		"branch_name_suggestion": gtd.TitleToBranchSlug(task.Title),
		"work_session_id":        uuid.New().String(),
	})
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
		return uuid.UUID{}, uuid.UUID{}, errMsgInvalidTaskIDUUID
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
