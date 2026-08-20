package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// githubPRURLRe and commitSHARe are canonical definitions in the shared
// internal/validator and internal/gtd packages respectively. Package-level
// aliases keep call-sites in this file unchanged while eliminating duplication.
var (
	githubPRURLRe = validator.GitHubPRURLRe
	commitSHARe   = gtd.CommitSHARe
)

// resolveAssignee resolves a raw "assignee" value to a canonical actor via
// gtd.NormalizeActor. Empty input is allowed (many tasks start unowned) and
// resolves to "". Returns a non-empty error message on validation failure —
// whitelist, not blacklist (backend-security-design.md §2.1: LLM tool input
// is adversarial; an unvalidated assignee corrupts the "who is working on
// what" audit trail).
func resolveAssignee(raw string) (string, string) {
	if raw == "" {
		return "", ""
	}
	normalized, err := gtd.NormalizeActor(raw)
	if err != nil {
		return "", err.Error()
	}
	return normalized, ""
}

// resolveAssigneeArg is the map[string]any-args variant of resolveAssignee,
// kept for the other tools_*.go files (tools_plan.go, tools_worksession.go —
// out of scope for this seam pilot) that still read args via stringArg.
func resolveAssigneeArg(args map[string]any) (string, string) {
	return resolveAssignee(stringArg(args, "assignee"))
}

// parseImportance validates the optional "importance" value (1=high, 2=med,
// 3=low), shared by add_task and update_task so the range check isn't
// duplicated. Returns (nil, "") when raw <= 0 (not provided), or a non-empty
// error message on an out-of-range value.
func parseImportance(raw int32) (*int16, string) {
	if raw <= 0 {
		return nil, ""
	}
	if raw > 3 {
		return nil, "importance must be 1, 2, or 3"
	}
	v := int16(raw)
	return &v, ""
}

// errMsgInvalidPRURL is the shared validation message for pr_url, hoisted to
// a package-level constant because add_task (applyBranchAndPR), update_task
// (applyBranchAndPRUpdate), and begin_task (validateBeginTaskLinkageArgs)
// all enforce the identical rule (goconst). branch_name's own error messages
// come straight from validator.ValidateBranchName — the single shared
// implementation with the HTTP path (gtd_handler.go) — rather than being
// hand-duplicated as constants here (sprint 8-7 gap E).
const errMsgInvalidPRURL = "pr_url must be a valid GitHub PR URL (https://github.com/owner/repo/pull/N)"

// applyBranchAndPR validates and sets branch_name and pr_url on a CreateTaskParams.
// Returns a non-empty error message on validation failure.
func applyBranchAndPR(branchName, prURL string, p *gtd.CreateTaskParams) string {
	if branchName != "" {
		if msg := validator.ValidateBranchName(branchName); msg != "" {
			return msg
		}
		p.BranchName = &branchName
	}
	if prURL != "" {
		if !githubPRURLRe.MatchString(prURL) {
			return errMsgInvalidPRURL
		}
		p.PRUrl = &prURL
	}
	return ""
}

// applyBranchAndPRUpdate validates and sets branch_name and pr_url on an
// UpdateTaskParams. nil means the caller didn't pass the arg at all (leave
// unchanged); a non-nil pointer to an explicit empty string clears the field.
// Returns a non-empty error message on validation failure.
func applyBranchAndPRUpdate(branchName, prURL *string, p *gtd.UpdateTaskParams) string {
	if branchName != nil {
		bn := *branchName
		if bn != "" {
			if msg := validator.ValidateBranchName(bn); msg != "" {
				return msg
			}
		}
		p.BranchName = branchName
	}
	if prURL != nil {
		pu := *prURL
		if pu != "" && !githubPRURLRe.MatchString(pu) {
			return errMsgInvalidPRURL
		}
		p.PRUrl = prURL
	}
	return ""
}

// maxPendingDeletions caps the number of valid (non-expired) delete tokens
// held simultaneously. This prevents a loop caller from growing the sync.Map
// without bound if tokens are issued faster than they expire.
const maxPendingDeletions = 256

func (s *Server) registerGTDTools(ms *server.MCPServer) {
	s.addTool(ms, mcp.NewTool(
		"list_projects",
		mcp.WithDescription("Returns all active projects."),
	), seam("list_projects", s.handleListProjects))

	s.addTool(
		ms, mcp.NewTool(
			"create_project",
			mcp.WithDescription("Creates a new project."),
			mcp.WithString("name", mcp.Description("Short slug identifier"), mcp.Required()),
			mcp.WithString("title", mcp.Description("Display title"), mcp.Required()),
			mcp.WithString("area", mcp.Description("Work area (e.g. engineering, personal)"), mcp.Required()),
			mcp.WithString("description", mcp.Description("Detailed description")),
			mcp.WithString("goal_id", mcp.Description("Parent goal UUID")),
			mcp.WithNumber("priority", mcp.Description("Priority 1-5, lower is higher")),
			mcp.WithString("repo_name", mcp.Description("VCS repository slug to link this project (e.g. wayneblacktea)")),
		), seam("create_project", s.handleCreateProject),
		requiredMsg("name", "name, title and area are required"),
		requiredMsg("title", "name, title and area are required"),
		requiredMsg("area", "name, title and area are required"),
		uuidArgs("goal_id"),
	)

	s.addTool(
		ms, mcp.NewTool(
			"update_project",
			mcp.WithDescription("Updates mutable fields of a project. All params except project_id are optional. "+
				"Omitted params preserve the existing value. Use update_project_status to change status only."),
			mcp.WithString("project_id", mcp.Description("Project UUID"), mcp.Required()),
			mcp.WithString("title", mcp.Description("Updated display title"), mcp.MaxLength(500)),
			mcp.WithString("description", mcp.Description(
				"Updated description. REPLACES the stored value entirely — no append/merge; "+
					"omit to leave unchanged.",
			), mcp.MaxLength(5000)),
			mcp.WithString("area", mcp.Description("Work area (e.g. engineering, personal)")),
			mcp.WithNumber("priority", mcp.Description("Priority 1-5, lower is higher")),
			mcp.WithString("status",
				mcp.Description("New status: active, completed, archived, or on_hold"),
				mcp.Enum("active", "completed", "archived", "on_hold")),
			mcp.WithString("goal_id", mcp.Description("Parent goal UUID (empty string clears the link)")),
			mcp.WithString("repo_name", mcp.Description("VCS repository slug (empty string clears the link)")),
		), seam("update_project", s.handleUpdateProject),
		uuidArgs("project_id"),
	)

	s.addTool(
		ms, mcp.NewTool(
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
		), seam("list_tasks", s.handleListTasks),
		uuidArgs("project_id"),
	)

	// Description budget: add_task is in the always-visible core tool set
	// (toolgroups.go), so every param description here is paid by every client
	// on every tools/list. Field semantics (priority-vs-importance, kind
	// values, the assignee allowlist) moved to mcpProtocolAppendix ("Per-tool
	// detail", tools_onboarding.go) — keep only the imperative trigger and the
	// bare field meanings here.
	s.addTool(
		ms, mcp.NewTool(
			"add_task",
			mcp.WithDescription(
				"CALL immediately when follow-up work is identified during discussion. "+
					"Creates a task optionally under a project. See initial_instructions "+
					"(Per-tool detail) for kind values, the assignee allowlist, and "+
					"priority-vs-importance.",
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
			// noMaxLength("assignee") below keeps the seam from enforcing this
			// hint server-side, preserving that established pattern.
			mcp.WithString("assignee", mcp.Description("Owner: claude | codex | human"), mcp.MaxLength(100)),
			mcp.WithNumber("priority", mcp.Description("Priority 1-5, lower runs first")),
			mcp.WithNumber("importance", mcp.Description("Importance 1-3, 1=high")),
			mcp.WithString("context", mcp.Description("Why this task came up")),
			mcp.WithString("kind",
				mcp.Description("Task kind, default general"),
				mcp.Enum("general", "fix-pr", "feature", "refactor", "research", "chore")),
			mcp.WithString("branch_name", mcp.Description("Git branch name")),
			mcp.WithString("pr_url", mcp.Description("GitHub PR URL")),
			mcp.WithString("due_date", mcp.Description("Required. RFC3339, e.g. 2026-12-31T00:00:00Z")),
		), seam("add_task", s.handleAddTask),
		uuidArgs("project_id"),
		noMaxLength("assignee"),
	)

	s.addTool(
		ms, mcp.NewTool(
			"complete_task",
			mcp.WithDescription(
				"CALL after task is verified done (build pass, tests pass). Marks task completed and records artifact. "+
					"If artifact is a GitHub PR URL (https://github.com/.../pull/N) it is also stored as pr_url. "+
					"If artifact is a 40-character hex SHA it is appended to commit_shas.",
			),
			mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
			mcp.WithString("artifact", mcp.Description("Link or note for the output (PR URL or commit SHA auto-detected)")),
		), seam("complete_task", s.handleCompleteTask),
		uuidArgs("task_id"),
	)

	s.addTool(ms, mcp.NewTool(
		"list_goals",
		mcp.WithDescription("Returns all active goals ordered by due date."),
	), seam("list_goals", s.handleListGoals))

	s.addTool(
		ms, mcp.NewTool(
			"create_goal",
			mcp.WithDescription("Creates a new goal."),
			mcp.WithString("title", mcp.Description("Goal title"), mcp.Required()),
			mcp.WithString("area", mcp.Description("Life area (e.g. career, health, personal)"), mcp.Required()),
			mcp.WithString("description", mcp.Description("Detailed description")),
			mcp.WithString("due_date", mcp.Description("Target date in RFC3339 format (e.g. 2026-12-31T00:00:00Z)")),
		), seam("create_goal", s.handleCreateGoal),
		requiredMsg("title", "title and area are required"),
		requiredMsg("area", "title and area are required"),
	)

	// Description budget: update_task is in the always-visible core tool set
	// (toolgroups.go) — same rule as add_task above. Omitted-field semantics
	// and the empty-string-clears convention moved to mcpProtocolAppendix
	// ("Per-tool detail", tools_onboarding.go).
	s.addTool(
		ms, mcp.NewTool(
			"update_task",
			mcp.WithDescription("Updates mutable fields of a task; all params except task_id are optional "+
				"and omitted ones keep their value. MUST call with status=\"in_progress\" the moment work "+
				"starts. Use complete_task, NEVER update_task, to mark a task completed. See "+
				"initial_instructions (Per-tool detail) for the full omission/clear semantics per field."),
			mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
			mcp.WithString("status",
				mcp.Description("pending | in_progress | cancelled"),
				mcp.Enum("pending", "in_progress", "cancelled")),
			mcp.WithString("title", mcp.Description("Updated title"), mcp.MaxLength(2000)),
			mcp.WithString("description", mcp.Description(
				"Updated details. REPLACES the stored value entirely — no append/merge; "+
					"omit to leave unchanged.",
			), mcp.MaxLength(10000)),
			mcp.WithNumber("priority", mcp.Description("Priority 1-5, lower runs first")),
			mcp.WithNumber("importance", mcp.Description("Importance 1-3, 1=high")),
			mcp.WithString("assignee", mcp.Description("Owner: claude | codex | human"), mcp.MaxLength(200)),
			mcp.WithString("due_date", mcp.Description("RFC3339, e.g. 2026-12-31T00:00:00Z")),
			mcp.WithString("context", mcp.Description("Discussion background"), mcp.MaxLength(10000)),
			mcp.WithString("kind",
				mcp.Description("Task kind"),
				mcp.Enum("general", "fix-pr", "feature", "refactor", "research", "chore")),
			mcp.WithString("branch_name", mcp.Description("Git branch name, empty clears")),
			mcp.WithString("pr_url", mcp.Description("GitHub PR URL, empty clears")),
		), seam("update_task", s.handleUpdateTask),
		uuidArgs("task_id"),
		// assignee's MaxLength(200) is advisory-only (see add_task's
		// identical rationale above) — gtd.NormalizeActor's allowlist is the
		// real authority regardless of length.
		noMaxLength("assignee"),
	)

	s.addTool(
		ms, mcp.NewTool(
			"update_project_status",
			mcp.WithDescription("Updates the status of a project."),
			mcp.WithString("project_id", mcp.Description("Project UUID"), mcp.Required()),
			mcp.WithString("status", mcp.Description("New status: active, completed, archived, or on_hold"), mcp.Required()),
		), seam("update_project_status", s.handleUpdateProjectStatus),
		uuidArgs("project_id"),
	)

	s.addTool(ms, mcp.NewTool(
		"get_project",
		mcp.WithDescription("Returns a project by name with its recent decisions."),
		mcp.WithString("name", mcp.Description("Project slug name"), mcp.Required()),
	), seam("get_project", s.handleGetProject))

	s.addTool(
		ms, mcp.NewTool(
			"log_activity",
			mcp.WithDescription("Records an activity log entry for a project."),
			mcp.WithString("actor", mcp.Description("Who did the action (e.g. claude-code, human)"), mcp.Required()),
			mcp.WithString("action", mcp.Description("What was done"), mcp.Required()),
			mcp.WithString("project_id", mcp.Description("Project UUID (optional)")),
			mcp.WithString("notes", mcp.Description("Additional notes")),
		), seam("log_activity", s.handleLogActivity),
		requiredMsg("actor", "actor and action are required"),
		requiredMsg("action", "actor and action are required"),
		uuidArgs("project_id"),
	)

	s.addTool(ms, mcp.NewTool(
		"get_upcoming_work",
		mcp.WithDescription(
			"Returns pending/in-progress tasks grouped into today, tomorrow, day_after, "+
				"upcoming, and unscheduled_important buckets. Use to plan the next work "+
				"session or surface high-importance tasks that have no due date.",
		),
		mcp.WithNumber("days", mcp.Description("How many days ahead to include (1-14, default 7)")),
	), seam("get_upcoming_work", s.handleGetUpcomingWork))

	s.addTool(
		ms, mcp.NewTool(
			"delete_task",
			mcp.WithDescription(
				"Permanently deletes a task. TWO-STEP: first call with only task_id "+
					"returns {deletion_token, expires_at}; second call MUST include "+
					"confirm=true and deletion_token to perform the delete. Tokens "+
					"expire after 60s and MUST be confirmed from the same MCP session "+
					"that issued them — a token cannot be handed off to a different "+
					"session/connection to complete the delete.",
			),
			mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
			mcp.WithBoolean("confirm", mcp.Description("Set true on the second call to actually delete")),
			mcp.WithString("deletion_token", mcp.Description("Token returned by the first call; required when confirm=true")),
		), seam("delete_task", s.handleDeleteTask),
		uuidArgs("task_id"),
	)

	s.addTool(
		ms, mcp.NewTool(
			"task_checklist_add_item",
			mcp.WithDescription(
				"Appends a new checklist item to a task. Returns the full updated checklist. "+
					"Use to track sub-steps or acceptance criteria for a task.",
			),
			mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
			mcp.WithString("title", mcp.Description("Item title (max 500 chars)"), mcp.Required(), mcp.MaxLength(500)),
			mcp.WithString("file_ref", mcp.Description("Optional file path reference (max 2000 chars)"), mcp.MaxLength(2000)),
			mcp.WithString("notes", mcp.Description("Optional notes (max 2000 chars)"), mcp.MaxLength(2000)),
		), seam("task_checklist_add_item", s.handleChecklistAddItem),
		uuidArgs("task_id"),
	)

	s.addTool(
		ms, mcp.NewTool(
			"task_checklist_toggle",
			mcp.WithDescription(
				"Partially updates a checklist item (done flag, title, notes, evidence_url). "+
					"Returns the full updated checklist.",
			),
			mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
			mcp.WithString("item_id", mcp.Description("Checklist item UUID"), mcp.Required()),
			mcp.WithBoolean("done", mcp.Description("Mark item done (true) or undone (false)"), mcp.Required()),
			mcp.WithString("evidence_url", mcp.Description("Optional URL or note proving the item is done"), mcp.MaxLength(2000)),
		), seam("task_checklist_toggle", s.handleChecklistToggle),
		uuidArgs("task_id", "item_id"),
	)

	s.addTool(
		ms, mcp.NewTool(
			"task_checklist_complete",
			mcp.WithDescription(
				"Shorthand for marking a checklist item done=true and recording completed_at=now. "+
					"Returns the full updated checklist.",
			),
			mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
			mcp.WithString("item_id", mcp.Description("Checklist item UUID"), mcp.Required()),
		), seam("task_checklist_complete", s.handleChecklistComplete),
		uuidArgs("task_id", "item_id"),
	)

	s.addTool(
		ms, mcp.NewTool(
			"get_task",
			mcp.WithDescription(
				"Returns a single task by UUID. Status-agnostic — retrieves pending, "+
					"in_progress, completed, and cancelled tasks. Use list_tasks for "+
					"filtered bulk retrieval.",
			),
			mcp.WithString("task_id", mcp.Description("Task UUID"), mcp.Required()),
		), seam("get_task", s.handleGetTask),
		uuidArgs("task_id"),
	)

	s.addTool(
		ms, mcp.NewTool(
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
		), seam("set_task_status", s.handleSetTaskStatus),
		uuidArgs("task_id"),
	)

	s.addTool(ms, mcp.NewTool(
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
	// begin_task's task_id is intentionally NOT uuidArgs-marked: handleBeginTask
	// keeps its own inline uuid.Parse so it can embed the parse error verbatim
	// ("invalid task_id: <err>") — the documented exception on requireUUIDArg's
	// doc comment (server.go) for handlers whose message needs the underlying
	// parse error. task_id's pre-existing mcp.Required() still makes the seam
	// reject a wholly-missing/empty task_id with "task_id is required" before
	// the handler runs; only a malformed-but-non-empty task_id now reaches the
	// hand-written uuid.Parse and its dynamic message.
	), seam("begin_task", s.handleBeginTask))
}

// Read-time bounds for db.Task/db.Project's free-text fields, applied by
// wrapUntrustedTask/wrapUntrustedProject before jsonText — U13
// (2026-08-20-mcp-surface-spec.md). Write-time caps on these fields are
// inconsistent across tools today (update_task.title has mcp.MaxLength(500),
// add_task.title has none at all), so these read-time bounds intentionally
// sit ABOVE every existing write cap — they exist to stop marker-stuffing /
// pathological-growth content from reaching an unbounded read, not to
// replicate the session-start token-diet get_today_context enforces via
// taskTitleMaxRunes/projectTitleMaxRunes (tools_context.go, a DIFFERENT,
// much smaller cap for a DIFFERENT, budget-constrained payload). Legitimate
// content on a get_task/get_project-class full read should never hit these.
const (
	gtdTitleMaxRunes = 2000
	gtdBodyMaxRunes  = 20000
)

// wrapUntrustedTask returns a copy of t with every free-text field
// clipSafe'd (bounded + boundary-marker-neutralised) — U13. Mirrors
// wrapUntrustedArchSnapshot's (tools_arch.go) and wrapUntrustedDecision's
// (tools_decision.go) copy-not-mutate contract. nil in, nil out.
//
// Checklist is deliberately NOT touched here: it is raw JSON bytes ([]byte)
// encoding a []gtd.ChecklistItem, each of which carries its own free-text
// Title/FileRef/Notes — neutralising those requires unmarshal-walk-remarshal,
// not a plain clipSafe(string) call, and is out of this template's scope
// (Phase A dispatch note: flagged to Lead as a distinct implementation
// pattern, not silently skipped). Artifact/BranchName/PRUrl are left as-is:
// applyArtifactSideEffects/validateBeginTaskLinkageArgs already constrain
// them to URL/SHA/branch-name shapes at the write paths that set them, which
// forecloses embedding a multi-line forged marker. Assignee is left as-is:
// gtd.NormalizeActor is a whitelist, not free text.
func wrapUntrustedTask(t *db.Task) *db.Task {
	if t == nil {
		return nil
	}
	out := *t
	out.Title = clipSafe(t.Title, gtdTitleMaxRunes)
	if t.Description.Valid {
		out.Description.String = clipSafe(t.Description.String, gtdBodyMaxRunes)
	}
	if t.Context.Valid {
		out.Context.String = clipSafe(t.Context.String, gtdBodyMaxRunes)
	}
	return &out
}

// wrapUntrustedProject is wrapUntrustedTask's sibling for db.Project — see
// its doc comment for the shared rationale. Name is included alongside
// Title: unlike Task, a project's Name doubles as its lookup key
// (get_project(name)) but is still caller-supplied free text at
// create_project time, validated only by validator.IsValidRepoName's
// separate repo_name field, not Name itself.
func wrapUntrustedProject(p *db.Project) *db.Project {
	if p == nil {
		return nil
	}
	out := *p
	out.Name = clipSafe(p.Name, gtdTitleMaxRunes)
	out.Title = clipSafe(p.Title, gtdTitleMaxRunes)
	if p.Description.Valid {
		out.Description.String = clipSafe(p.Description.String, gtdBodyMaxRunes)
	}
	return &out
}

// wrapUntrustedGoal is wrapUntrustedTask's sibling for db.Goal — U13 Phase B
// (.specs/2026-08-20-u13-inventory.md, tools_gtd.go:1026/1047). Same
// copy-not-mutate contract, nil in/nil out. Area/Status are left as-is: both
// are short caller-supplied values but neither list_goals nor create_goal's
// PENDING inventory entries flag them as needing this treatment, unlike
// Title/Description.
func wrapUntrustedGoal(g *db.Goal) *db.Goal {
	if g == nil {
		return nil
	}
	out := *g
	out.Title = clipSafe(g.Title, gtdTitleMaxRunes)
	if g.Description.Valid {
		out.Description.String = clipSafe(g.Description.String, gtdBodyMaxRunes)
	}
	return &out
}

// wrapUntrustedGoals maps wrapUntrustedGoal over a slice, mirroring
// wrapUntrustedDecisions' (tools_decision.go) same pattern for a []T sibling.
func wrapUntrustedGoals(goals []db.Goal) []db.Goal {
	out := make([]db.Goal, len(goals))
	for i := range goals {
		out[i] = *wrapUntrustedGoal(&goals[i])
	}
	return out
}

func (s *Server) handleListProjects(ctx context.Context, _ ListProjectsArgs) (*mcp.CallToolResult, error) {
	projects, err := s.gtd.ListActiveProjects(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading projects: %v", err)), nil
	}
	if projects == nil {
		projects = []db.Project{} // list tools MUST return [] not null (a nil slice marshals to JSON null)
	}
	out := make([]db.Project, len(projects))
	for i := range projects {
		out[i] = *wrapUntrustedProject(&projects[i])
	}
	return jsonText(out)
}

func (s *Server) handleCreateProject(ctx context.Context, args CreateProjectArgs) (*mcp.CallToolResult, error) {
	if !validator.IsValidRepoName(args.RepoName) {
		return mcp.NewToolResultError("repo_name must match [a-zA-Z0-9_.-]{1,100}"), nil
	}
	p := gtd.CreateProjectParams{
		Name:        args.Name,
		Title:       args.Title,
		Area:        args.Area,
		Description: args.Description,
		Priority:    args.Priority,
		RepoName:    args.RepoName,
		GoalID:      args.GoalID,
	}

	project, err := s.gtd.CreateProject(ctx, p)
	if errors.Is(err, gtd.ErrConflict) {
		return mcp.NewToolResultError("project name already exists"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating project: %v", err)), nil
	}
	return jsonText(wrapUntrustedProject(project))
}

func (s *Server) handleUpdateProject(ctx context.Context, args UpdateProjectArgs) (*mcp.CallToolResult, error) {
	// Load the existing project to fill omitted fields (preserve-on-omit semantics).
	existing, err := s.gtd.GetProjectByID(ctx, args.ProjectID)
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

	project, err := s.gtd.UpdateProject(ctx, args.ProjectID, p)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("project not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("updating project: %v", err)), nil
	}
	return jsonText(wrapUntrustedProject(project))
}

// buildUpdateProjectParams builds a gtd.UpdateProjectParams from typed
// UpdateProjectArgs, filling omitted fields from the existing project row.
// Returns a non-empty toolErr string on validation failure. status's
// enum-membership (when present+non-empty) is already enforced by the seam
// (update_project's status arg declares mcp.Enum(...) at registration) — no
// hand-written switch-case needed here.
func buildUpdateProjectParams(args UpdateProjectArgs, existing *db.Project) (gtd.UpdateProjectParams, string) {
	title := args.Title
	if title == "" {
		title = existing.Title
	}
	description := args.Description
	if description == "" && existing.Description.Valid {
		description = existing.Description.String
	}
	area := args.Area
	if area == "" {
		area = existing.Area
	}
	priority := args.Priority
	if priority == 0 {
		priority = existing.Priority
	}

	status := gtd.ProjectStatus(args.Status)
	if args.Status == "" {
		status = gtd.ProjectStatus(existing.Status)
	}

	p := gtd.UpdateProjectParams{
		Title:       title,
		Description: description,
		Area:        area,
		Priority:    priority,
		Status:      status,
	}

	// goal_id: explicitly passed (even empty) → overwrite; absent → preserve.
	if goalErr := resolveGoalID(args.GoalID, existing, &p); goalErr != "" {
		return gtd.UpdateProjectParams{}, goalErr
	}

	// repo_name: explicitly passed → overwrite (nil pointer = preserve existing).
	if args.RepoName != nil {
		if !validator.IsValidRepoName(*args.RepoName) {
			return gtd.UpdateProjectParams{}, "repo_name must match [a-zA-Z0-9_.-]{1,100}"
		}
		p.RepoName = args.RepoName
	}

	return p, ""
}

// resolveGoalID applies goal_id to p, preserving the existing goal when
// goalID is nil (absent from the call). Returns a non-empty string on parse
// failure. goalID != nil but pointing at "" means "explicitly clear".
func resolveGoalID(goalID *string, existing *db.Project, p *gtd.UpdateProjectParams) string {
	if goalID != nil {
		if *goalID == "" {
			p.GoalID = nil // clear the link
			return ""
		}
		gid, parseErr := uuid.Parse(*goalID)
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
// Title goes through clipSafe (U13 Phase B, tools_gtd.go:772) — same bound
// wrapUntrustedTask applies to the full-record shape below, since a stored
// title can carry a forged boundary marker regardless of which shape the
// caller asked for (summary=true is the list_tasks default).
func toTaskSummary(t db.Task) taskSummary {
	ts := taskSummary{
		ID:       t.ID.String(),
		Title:    clipSafe(t.Title, gtdTitleMaxRunes),
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

func (s *Server) handleListTasks(ctx context.Context, args ListTasksArgs) (*mcp.CallToolResult, error) {
	// status: enum-membership (when present) already enforced by the seam
	// (list_tasks' status arg declares mcp.Enum(...) at registration);
	// default "active" when absent.
	rawStatus := args.Status
	if rawStatus == "" {
		rawStatus = "active"
	}

	// limit: <=0 → 50; clamp to 200
	limit := int(args.Limit)
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	// offset: <0 → 0
	offset := int(args.Offset)
	if offset < 0 {
		offset = 0
	}

	// summary: absent → true (default)
	summaryMode := true
	if args.Summary != nil {
		summaryMode = *args.Summary
	}

	// Fetch limit+1 rows to detect has_more without a COUNT query.
	effLimit := limit + 1
	f := gtd.TaskFilter{
		ProjectID: args.ProjectID,
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
		if rows == nil {
			rows = []db.Task{} // list tools MUST return [] not null (a nil slice marshals to JSON null)
		}
		// U13 Phase B (tools_gtd.go:772): summary=false returns full db.Task
		// rows, so each one goes through wrapUntrustedTask the same as
		// get_task's single-record read (line ~793).
		wrapped := make([]db.Task, len(rows))
		for i := range rows {
			wrapped[i] = *wrapUntrustedTask(&rows[i])
		}
		tasks = wrapped
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
func (s *Server) handleGetTask(ctx context.Context, args GetTaskArgs) (*mcp.CallToolResult, error) {
	task, err := s.gtd.GetTaskByID(ctx, args.TaskID)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading task: %v", err)), nil
	}
	return jsonText(wrapUntrustedTask(task))
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
func (s *Server) handleSetTaskStatus(ctx context.Context, args SetTaskStatusArgs) (*mcp.CallToolResult, error) {
	id := args.TaskID
	rawStatus := args.Status

	cur, err := s.gtd.GetTaskByID(ctx, id)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading task: %v", err)), nil
	}

	// Idempotent no-op: same → same, no write. U13 Phase B
	// (tools_gtd.go:825): wrapUntrustedTask same as the write branch below —
	// this branch returns the raw existing row unchanged, which is a stored
	// (possibly untrusted) read just like get_task's.
	if cur.Status == rawStatus {
		return jsonText(wrapUntrustedTask(cur))
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
	return jsonText(wrapUntrustedTask(updated)) // U13 Phase B (tools_gtd.go:843)
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

// parseRequiredDueDate validates the RFC3339 due_date argument. Returns the
// parsed time and nil error result on success; returns nil time and a
// non-nil CallToolResult (error) when validation fails.
func parseRequiredDueDate(raw string) (*time.Time, *mcp.CallToolResult) {
	if raw == "" {
		return nil, mcp.NewToolResultError("due_date is required (RFC3339, e.g. 2026-12-31T00:00:00Z)")
	}
	dueTime, parseErr := time.Parse(time.RFC3339, raw)
	if parseErr != nil {
		return nil, mcp.NewToolResultError("invalid due_date: must be RFC3339 (e.g. 2026-12-31T00:00:00Z)")
	}
	return &dueTime, nil
}

func (s *Server) handleAddTask(ctx context.Context, args AddTaskArgs) (*mcp.CallToolResult, error) {
	kind := args.Kind
	if kind == "" {
		kind = validator.KindGeneral
	}
	if !validator.IsValidKind(kind) {
		return mcp.NewToolResultError("kind must be one of: general, fix-pr, feature, refactor, research, chore"), nil
	}

	// Vagueness and kind-field checks. For MCP, warnings are embedded in the
	// result JSON body (no HTTP headers available). Strict mode → tool error.
	allWarnings := validator.CheckTaskInput(args.Description, kind)
	if len(allWarnings) > 0 && validator.StrictModeEnabled() {
		return mcp.NewToolResultError(fmt.Sprintf("vagueness check failed: %v", allWarnings)), nil
	}

	assignee, assigneeErr := resolveAssignee(args.Assignee)
	if assigneeErr != "" {
		return mcp.NewToolResultError(assigneeErr), nil
	}

	p := gtd.CreateTaskParams{
		Title:       args.Title,
		Description: args.Description,
		Assignee:    assignee,
		Priority:    args.Priority,
		Context:     args.Context,
		Kind:        kind,
		ProjectID:   args.ProjectID,
	}
	imp, impErr := parseImportance(args.Importance)
	if impErr != "" {
		return mcp.NewToolResultError(impErr), nil
	}
	p.Importance = imp
	if msg := applyBranchAndPR(args.BranchName, args.PRUrl, &p); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	// due_date is required in add_task (OVERRIDE 1: hard-require at tool layer only).
	// Other CreateTask callers (promote_vision_to_task, reconcile, HTTP, CLI, Discord)
	// call the store directly and are NOT affected by this check.
	dueTime, dueErr := parseRequiredDueDate(args.DueDate)
	if dueErr != nil {
		return dueErr, nil
	}
	p.DueDate = dueTime

	task, err := s.gtd.CreateTask(ctx, p)
	if err != nil {
		slog.Warn("add_task: CreateTask failed", "title", args.Title, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("creating task: %v", err)), nil
	}

	// Embed warnings in the result body when present. U13 Phase B
	// (tools_gtd.go:927/929): both return paths echo the just-created task
	// back to the caller — wrapUntrustedTask, not a same-turn-echo exemption,
	// because Description/Context/Title are free text the caller supplied
	// this same call, so the exemption would apply here too EXCEPT this same
	// task row is also read back later by list_tasks/get_task from a
	// different session (matches the sync_repo/list_active_repos reasoning
	// in the U13 inventory) — wire it here for consistency.
	if len(allWarnings) > 0 {
		return jsonText(map[string]any{"task": wrapUntrustedTask(task), "warnings": allWarnings})
	}
	return jsonText(wrapUntrustedTask(task))
}

func (s *Server) handleCompleteTask(ctx context.Context, args CompleteTaskArgs) (*mcp.CallToolResult, error) {
	var artifact *string
	if args.Artifact != "" {
		a := args.Artifact
		artifact = &a
	}

	task, err := s.gtd.CompleteTask(ctx, args.TaskID, artifact)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		slog.Warn("complete_task: CompleteTask failed", "task_id", args.TaskID, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("completing task: %v", err)), nil
	}

	// Auto-parse artifact: PR URL → set pr_url; 40-hex SHA → append to commit_shas.
	// SECURITY: we only store the strings, never fetch the URLs.
	if artifact != nil && *artifact != "" {
		if updated := s.applyArtifactSideEffects(ctx, args.TaskID, strings.TrimSpace(*artifact)); updated != nil {
			task = updated
		}
	}

	s.seedDraftOutcome(ctx, args.TaskID)

	return jsonText(wrapUntrustedTask(task)) // U13 Phase B (tools_gtd.go:958)
}

// seedDraftOutcome best-effort records a result:"unknown" outcome for a
// just-completed task so the outcome→evaluate_outcome→behavior_governance
// learning loop always has fuel, even when the agent never calls
// record_outcome itself. Idempotent and race-safe via
// outcome.StoreIface.SeedDraft (migration 000074's partial unique index
// idx_outcomes_one_open_draft) rather than the old
// ExistsForEntity-then-CreateOutcome check-then-insert, which had a TOCTOU
// window under concurrent complete_task calls on the same task. Failures are
// logged and swallowed — complete_task has already succeeded by the time
// this runs and must never fail because of it.
func (s *Server) seedDraftOutcome(ctx context.Context, taskID uuid.UUID) {
	if s.outcome == nil {
		return
	}
	wsID := s.workspaceUUID()
	if _, _, err := s.outcome.SeedDraft(ctx, wsID, "task", taskID); err != nil {
		slog.Warn("complete_task: seeding draft outcome failed (non-fatal)", "task_id", taskID, "err", err)
	}
}

// applyArtifactSideEffects detects whether artifact is a GitHub PR URL or a
// 40-hex commit SHA and applies the corresponding side-effect update on the
// task. Returns the updated task on success, nil if no side-effect applied or
// the update failed (caller uses the original completed task in that case).
// SECURITY: only stores the URL string, never makes an HTTP fetch.
//
// commit_shas is appended atomically at the SQL layer via
// gtd.UpdateTaskParams.AppendCommitSHA (P7 fix) — this function no longer
// reads the caller's already-loaded task to compute a merged array in Go,
// which was a TOCTOU race under concurrent complete_task calls on the same
// task (the exact duplicate of gtd/artifact_effects.go's HTTP-path bug this
// PR also fixes).
func (s *Server) applyArtifactSideEffects(ctx context.Context, id uuid.UUID, artifact string) *db.Task {
	var up gtd.UpdateTaskParams
	var artifactKind string
	if githubPRURLRe.MatchString(artifact) {
		up.PRUrl = &artifact
		artifactKind = "pr_url"
	} else if commitSHARe.MatchString(artifact) {
		up.AppendCommitSHA = &artifact
		artifactKind = "commit_sha"
	}
	if up.PRUrl == nil && up.AppendCommitSHA == nil {
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

func (s *Server) handleListGoals(ctx context.Context, _ ListGoalsArgs) (*mcp.CallToolResult, error) {
	goals, err := s.gtd.ActiveGoals(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading goals: %v", err)), nil
	}
	if goals == nil {
		goals = []db.Goal{} // list tools MUST return [] not null (a nil slice marshals to JSON null)
	}
	return jsonText(wrapUntrustedGoals(goals)) // U13 Phase B (tools_gtd.go:1026)
}

func (s *Server) handleCreateGoal(ctx context.Context, args CreateGoalArgs) (*mcp.CallToolResult, error) {
	p := gtd.CreateGoalParams{
		Title:       args.Title,
		Area:        args.Area,
		Description: args.Description,
	}
	if args.DueDate != "" {
		t, err := time.Parse(time.RFC3339, args.DueDate)
		if err != nil {
			return mcp.NewToolResultError("invalid due_date: must be RFC3339 (e.g. 2026-12-31T00:00:00Z)"), nil
		}
		p.DueDate = &t
	}

	goal, err := s.gtd.CreateGoal(ctx, p)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("creating goal: %v", err)), nil
	}
	return jsonText(wrapUntrustedGoal(goal)) // U13 Phase B (tools_gtd.go:1047)
}

// parseUpdateTaskArgs translates a validated UpdateTaskArgs into
// gtd.UpdateTaskParams. status's enum-membership (when present) is already
// enforced by the seam (update_task's status arg declares mcp.Enum(...) at
// registration) — this function only handles genuine business logic
// (priority/importance ranges, assignee normalisation, due_date parsing,
// branch_name/pr_url validation) that has no schema-derivable equivalent.
// Returns (params, errMsg) — errMsg is non-empty on validation failure.
func parseUpdateTaskArgs(args UpdateTaskArgs) (gtd.UpdateTaskParams, string) {
	p := gtd.UpdateTaskParams{}

	if args.Status != "" {
		st := args.Status
		p.Status = &st
	}

	if args.Title != "" {
		title := args.Title
		p.Title = &title
	}
	if args.Description != "" {
		desc := args.Description
		p.Description = &desc
	}

	if args.Priority > 0 {
		if args.Priority > 5 {
			return p, "priority must be between 1 and 5"
		}
		v := args.Priority
		p.Priority = &v
	}
	imp, impErr := parseImportance(args.Importance)
	if impErr != "" {
		return p, impErr
	}
	p.Importance = imp

	if args.Assignee != "" {
		normalized, normErr := gtd.NormalizeActor(args.Assignee)
		if normErr != nil {
			return p, normErr.Error()
		}
		p.Assignee = &normalized
	}
	if args.DueDate != "" {
		t, parseErr := time.Parse(time.RFC3339, args.DueDate)
		if parseErr != nil {
			return p, "invalid due_date: must be RFC3339 (e.g. 2026-12-31T00:00:00Z)"
		}
		p.DueDate = &t
	}
	if args.Context != "" {
		taskCtx := args.Context
		p.Context = &taskCtx
	}
	if args.Kind != "" {
		if !validator.IsValidKind(args.Kind) {
			return p, "kind must be one of: general, fix-pr, feature, refactor, research, chore"
		}
		k := args.Kind
		p.Kind = &k
	}

	if msg := applyBranchAndPRUpdate(args.BranchName, args.PRUrl, &p); msg != "" {
		return p, msg
	}

	return p, ""
}

// updateTaskParamsIsEmpty returns true when no field is set in p — the caller
// must reject the request when all fields are nil/empty to avoid a no-op write.
func updateTaskParamsIsEmpty(p gtd.UpdateTaskParams) bool {
	return p.Status == nil && p.Title == nil && p.Description == nil &&
		p.Priority == nil && p.Importance == nil && p.Assignee == nil &&
		p.DueDate == nil && p.Context == nil && p.Kind == nil &&
		p.BranchName == nil && p.PRUrl == nil && p.AppendCommitSHA == nil
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

func (s *Server) handleUpdateTask(ctx context.Context, args UpdateTaskArgs) (*mcp.CallToolResult, error) {
	p, errMsg := parseUpdateTaskArgs(args)
	if errMsg != "" {
		return mcp.NewToolResultError(errMsg), nil
	}
	if updateTaskParamsIsEmpty(p) {
		return mcp.NewToolResultError("at least one field is required"), nil
	}
	if errResult := s.requireAssigneeForInProgress(ctx, args.TaskID, p); errResult != nil {
		return errResult, nil
	}

	task, err := s.gtd.UpdateTask(ctx, args.TaskID, p)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		slog.Warn("update_task: UpdateTask failed", "task_id", args.TaskID, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("updating task: %v", err)), nil
	}
	return jsonText(wrapUntrustedTask(task)) // U13 Phase B (tools_gtd.go:1178)
}

func (s *Server) handleUpdateProjectStatus(ctx context.Context, args UpdateProjectStatusArgs) (*mcp.CallToolResult, error) {
	status := gtd.ProjectStatus(args.Status)
	switch status {
	case gtd.ProjectStatusActive, gtd.ProjectStatusCompleted, gtd.ProjectStatusArchived, gtd.ProjectStatusOnHold:
	default:
		return mcp.NewToolResultError("status must be one of: active, completed, archived, on_hold"), nil
	}

	project, err := s.gtd.UpdateProjectStatus(ctx, args.ProjectID, status)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("project not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("updating project: %v", err)), nil
	}
	return jsonText(wrapUntrustedProject(project)) // U13 Phase B (tools_gtd.go:1196)
}

type projectWithDecisions struct {
	Project   any `json:"project"`
	Decisions any `json:"recent_decisions"`
}

func (s *Server) handleGetProject(ctx context.Context, args GetProjectArgs) (*mcp.CallToolResult, error) {
	project, err := s.gtd.ProjectByName(ctx, args.Name)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError(fmt.Sprintf("project %q not found", args.Name)), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading project: %v", err)), nil
	}

	decisions, err := s.decision.ByProject(ctx, project.ID, 5)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("loading decisions: %v", err)), nil
	}
	if decisions == nil {
		decisions = []db.Decision{} // embedded list MUST be [] not null (a nil slice marshals to JSON null)
	}

	// U13 Phase B (tools_gtd.go:1221): both nested structs are stored reads
	// unwired before this — Project via wrapUntrustedProject (same helper
	// used at list_projects/create_project/update_project), Decisions via
	// wrapUntrustedDecisions (tools_decision.go, same helper list_decisions
	// already uses).
	return jsonText(projectWithDecisions{
		Project:   wrapUntrustedProject(project),
		Decisions: wrapUntrustedDecisions(decisions),
	})
}

func (s *Server) handleLogActivity(ctx context.Context, args LogActivityArgs) (*mcp.CallToolResult, error) {
	if err := s.gtd.LogActivity(ctx, args.Actor, args.Action, args.ProjectID, args.Notes); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("logging activity: %v", err)), nil
	}
	return mcp.NewToolResultText("activity logged"), nil
}

// currentSessionID returns the calling MCP client session's ID, or "" when
// ctx carries no tracked session at all (e.g. a transport that doesn't do
// session tracking, or a direct handler call in tests that never went
// through the real MCP server dispatch). mcp-go's real session IDs
// (StreamableHTTP: "mcp-session-"+uuid) never contain ":", which
// issueDeletionToken/deletionTokenMatchesSession below rely on.
func currentSessionID(ctx context.Context) string {
	sess := server.ClientSessionFromContext(ctx)
	if sess == nil {
		return ""
	}
	return sess.SessionID()
}

// issueDeletionToken builds the token value returned to the caller in
// delete_task's step 1 (and stored server-side as deletionToken.token,
// server.go — that struct itself is unchanged; the binding lives entirely in
// how this string is constructed and later checked, both in this file).
//
// When ctx carries a tracked session, the token is prefixed
// "<sessionID>:<random>" — deletionTokenMatchesSession then requires step 2
// to run from that SAME live session, not just present the same string.
// This is U9's partial mitigation for Category S (self-confirmed
// irreversible deletion, 2026-08-20-mcp-surface-spec.md): the token was
// previously keyed by task_id alone with no binding to WHO can complete step
// 2 — anyone who obtains the exact token string (already unlikely, but not
// architecturally prevented) could confirm it. Binding to the live MCP
// session narrows that to "must be the same ongoing connection", without
// requiring F16/U15's authenticated-actor-identity system, which hasn't
// landed yet and is what the FULL fix (distinct human/agent identities, not
// just distinct transport sessions) actually needs.
//
// When ctx carries no session (non-tracked transport, or a direct test
// call), the token is the bare random UUID exactly as before — unchanged,
// task-id-only protection for that case.
func issueDeletionToken(ctx context.Context) string {
	random := uuid.NewString()
	if sessionID := currentSessionID(ctx); sessionID != "" {
		return sessionID + ":" + random
	}
	return random
}

// deletionTokenMatchesSession reports whether storedToken (as built by
// issueDeletionToken) may be confirmed from ctx's current session. A token
// issued with no session prefix is accepted from any caller (the original,
// unchanged task-id-only protection). A token issued WITH a session prefix
// may only be confirmed from that exact session — see issueDeletionToken's
// doc comment.
func deletionTokenMatchesSession(ctx context.Context, storedToken string) bool {
	if !strings.Contains(storedToken, ":") {
		return true
	}
	sessionID := currentSessionID(ctx)
	return sessionID != "" && strings.HasPrefix(storedToken, sessionID+":")
}

// handleDeleteTask implements a 2-step confirmation flow.
//
// First call (confirm absent / false): issue a one-time deletion_token tied
// to task_id, store it in-memory with a 60s TTL, and return
// {deletion_token, expires_at} to the caller. The task is NOT deleted.
//
// Second call (confirm=true + matching deletion_token): verify the token
// matches the stored value, has not expired, AND (when the issuing call had
// a tracked MCP session) comes from that same session — see
// issueDeletionToken/deletionTokenMatchesSession — then call
// store.DeleteTask. The token is consumed (single-use) regardless of
// success.
//
// Rationale: prevent accidental deletion from a hallucinated tool call, AND
// (partial mitigation, U9) narrow deliberate cross-session replay of a
// captured token. An LLM that emits delete_task once gets a token-only
// response back; deleting requires a second deliberate call from the SAME
// session. The token must come from us so a malicious upstream client can't
// synthesize one without first making a "read" call. The full fix for a
// deliberate cross-IDENTITY (not just cross-session) confirm needs
// authenticated actor identity — F16/U15, not yet landed; see Category S in
// 2026-08-20-mcp-surface-spec.md.
func (s *Server) handleDeleteTask(ctx context.Context, args DeleteTaskArgs) (*mcp.CallToolResult, error) {
	id := args.TaskID
	confirm := args.Confirm
	suppliedToken := args.DeletionToken

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

		token := issueDeletionToken(ctx)
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
	// U9 partial mitigation (Category S): requester must be the same MCP
	// session that issued the token, when one was tracked — see
	// issueDeletionToken/deletionTokenMatchesSession's doc comments.
	if !deletionTokenMatchesSession(ctx, rec.token) {
		return mcp.NewToolResultError(
			"deletion_token was issued to a different session; call delete_task without confirm " +
				"from that same session to obtain a new token",
		), nil
	}

	if err := s.gtd.DeleteTask(ctx, id); err != nil {
		slog.Warn("delete_task: DeleteTask failed", "task_id", id, "err", err)
		return mcp.NewToolResultError(fmt.Sprintf("deleting task: %v", err)), nil
	}
	return mcp.NewToolResultText("task deleted"), nil
}

func (s *Server) handleGetUpcomingWork(ctx context.Context, args GetUpcomingWorkArgs) (*mcp.CallToolResult, error) {
	days := int(args.Days)
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

// wrapUntrustedChecklistItems returns a copy of items with each item's
// free-text fields (Title, FileRef, Notes, EvidenceURL) clipSafe'd (bounded +
// boundary-marker-neutralised) — U13 Phase B
// (.specs/2026-08-20-u13-inventory.md, tools_gtd.go:1440/1460/1475).
// sanitiseMCPText (gtd.SanitiseChecklistText) only strips control
// chars/nulls at write time; it does not neutralise boundary-marker text, so
// a forged "=== END STORED CONTEXT ===" survives into the stored row and was
// read back verbatim by all three checklist handlers before this change.
//
// EvidenceURL is included even though the inventory's field list for these
// three sites names only Title/FileRef/Notes: it is set from
// task_checklist_toggle's caller-supplied evidence_url argument through the
// exact same sanitiseMCPText-only path (handleChecklistToggle below), so it
// is the same class of gap as handleGetUpcomingWork's raw-title render
// (flagged separately in the U13 inventory despite not being a jsonText
// call site) — caught while implementing this helper rather than left for a
// later pass.
func wrapUntrustedChecklistItems(items []gtd.ChecklistItem) []gtd.ChecklistItem {
	out := make([]gtd.ChecklistItem, len(items))
	for i, it := range items {
		out[i] = it
		out[i].Title = clipSafe(it.Title, gtdTitleMaxRunes)
		if it.FileRef != "" {
			out[i].FileRef = clipSafe(it.FileRef, gtdBodyMaxRunes)
		}
		if it.Notes != "" {
			out[i].Notes = clipSafe(it.Notes, gtdBodyMaxRunes)
		}
		if it.EvidenceURL != "" {
			out[i].EvidenceURL = clipSafe(it.EvidenceURL, gtdBodyMaxRunes)
		}
	}
	return out
}

func (s *Server) handleChecklistAddItem(ctx context.Context, args ChecklistAddItemArgs) (*mcp.CallToolResult, error) {
	// title's required-presence and maxLength(500) are already enforced by
	// the seam on the raw value; sanitisation here can only shrink the
	// string further (never lengthen it), so a post-sanitise length re-check
	// would be redundant. "must not be blank after sanitisation" below is
	// genuine business logic the seam can't express: a title made entirely
	// of control characters passes the seam's raw-value checks but must
	// still be rejected once sanitised down to empty.
	title := sanitiseMCPText(args.Title)
	if title == "" {
		return mcp.NewToolResultError("title must not be blank after sanitisation"), nil
	}

	fileRef := sanitiseMCPText(args.FileRef)
	notes := sanitiseMCPText(args.Notes)

	wsID := workspaceUUIDFromPgtype(s.gtd.WorkspaceID())
	item := gtd.ChecklistItem{
		ID:        uuid.New(),
		Title:     title,
		FileRef:   fileRef,
		Notes:     notes,
		CreatedAt: s.now().UTC(),
	}
	items, err := s.gtd.AddChecklistItem(ctx, args.TaskID, wsID, item)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("adding checklist item: %v", err)), nil
	}
	return jsonText(wrapUntrustedChecklistItems(items)) // U13 Phase B (tools_gtd.go:1440)
}

func (s *Server) handleChecklistToggle(ctx context.Context, args ChecklistToggleArgs) (*mcp.CallToolResult, error) {
	evidenceURL := sanitiseMCPText(args.EvidenceURL)

	done := args.Done
	update := gtd.UpdateChecklistItemParams{Done: &done}
	if evidenceURL != "" {
		update.EvidenceURL = &evidenceURL
	}

	wsID := workspaceUUIDFromPgtype(s.gtd.WorkspaceID())
	items, err := s.gtd.UpdateChecklistItem(ctx, args.TaskID, wsID, args.ItemID, update)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task or item not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("toggling checklist item: %v", err)), nil
	}
	return jsonText(wrapUntrustedChecklistItems(items)) // U13 Phase B (tools_gtd.go:1460)
}

func (s *Server) handleChecklistComplete(ctx context.Context, args ChecklistCompleteArgs) (*mcp.CallToolResult, error) {
	done := true
	update := gtd.UpdateChecklistItemParams{Done: &done}

	wsID := workspaceUUIDFromPgtype(s.gtd.WorkspaceID())
	items, err := s.gtd.UpdateChecklistItem(ctx, args.TaskID, wsID, args.ItemID, update)
	if errors.Is(err, gtd.ErrNotFound) {
		return mcp.NewToolResultError("task or item not found"), nil
	}
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("completing checklist item: %v", err)), nil
	}
	return jsonText(wrapUntrustedChecklistItems(items)) // U13 Phase B (tools_gtd.go:1475)
}

// validateBeginTaskLinkageArgs checks the optional branch_name/pr_url args
// BEFORE handleBeginTask mutates anything, so a validation failure never
// leaves the task half-mutated (sprint feature/gtd-enforce-server-side
// GTD-fix 8/12). Extracted from handleBeginTask to keep its cyclomatic
// complexity within the gocyclo budget. Returns a non-empty error message on
// validation failure.
func validateBeginTaskLinkageArgs(branchName, prURL string) string {
	if branchName != "" {
		if msg := validator.ValidateBranchName(branchName); msg != "" {
			return msg
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

// resolveBeginTaskRepoName resolves the repo_name a work session created for
// task should be scoped to: the task's own project's repo_name when set,
// else primaryProjectSlug (tools_context.go) — the same single-tenant
// fallback get_project_arch's default slug already uses for "no specific
// project" contexts. Never returns "" (worksession.CreateParams.RepoName is
// required and rejects empty).
func (s *Server) resolveBeginTaskRepoName(ctx context.Context, task *db.Task) string {
	if task.ProjectID.Valid {
		pid := workspaceUUIDFromPgtype(task.ProjectID)
		if project, err := s.gtd.GetProjectByID(ctx, pid); err == nil &&
			project.RepoName.Valid && project.RepoName.String != "" {
			return project.RepoName.String
		}
	}
	return primaryProjectSlug
}

// attachBeginTaskWorkSession creates (or, if one is already active for this
// task's repo, reuses) a real worksession.Session and links id to it as the
// primary task — so the work_session_id begin_task returns is a real,
// persisted row checkpoint_work/finish_work can operate on, not a phantom
// UUID (F17, 2026-08-20-mcp-surface-spec.md U16). Best-effort: a failure here
// never fails begin_task's primary guarantee (the task is already in_progress
// by the time this runs) — on failure the caller gets no work_session_id
// rather than a fabricated one.
//
// Source="other" and Goal=task.Title reuse the narrowest existing
// conventions rather than inventing new ones: "other" is already a valid
// worksession source (tools_worksession.go's validWorkSessionSources) with no
// more specific value fitting "created as a side effect of begin_task"; Goal
// has no independent input on this call path, and "the goal of this session
// is to complete this task" is the literal, unambiguous reading for a
// single-task session.
func (s *Server) attachBeginTaskWorkSession(ctx context.Context, id uuid.UUID, task *db.Task, assignee string) uuid.UUID {
	if s.workSession == nil {
		return uuid.Nil
	}
	wsID := s.workspaceUUIDVal()
	repoName := s.resolveBeginTaskRepoName(ctx, task)

	var projectID *uuid.UUID
	if task.ProjectID.Valid {
		pid := workspaceUUIDFromPgtype(task.ProjectID)
		projectID = &pid
	}

	sess, err := s.workSession.Create(ctx, worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    repoName,
		ProjectID:   projectID,
		Title:       task.Title,
		Goal:        task.Title,
		Source:      "other",
		TaskIDs:     []uuid.UUID{id},
		Assignee:    assignee,
	})
	if err == nil {
		return sess.ID
	}
	if !errors.Is(err, worksession.ErrAlreadyActive) {
		slog.Warn("begin_task: worksession.Create failed (non-fatal, no work_session_id returned)",
			"task_id", id, "err", err)
		return uuid.Nil
	}

	// Another session is already in_progress for this repo — attach this
	// task to it rather than failing begin_task's primary guarantee.
	active, err := s.workSession.GetActive(ctx, wsID, repoName)
	if err != nil || active.Session == nil {
		slog.Warn("begin_task: GetActive after ErrAlreadyActive failed (non-fatal, no work_session_id returned)",
			"task_id", id, "err", err)
		return uuid.Nil
	}
	if err := s.workSession.LinkTask(ctx, active.Session.ID, id, "primary"); err != nil {
		slog.Warn("begin_task: LinkTask into active session failed (non-fatal, no work_session_id returned)",
			"task_id", id, "session_id", active.Session.ID, "err", err)
		return uuid.Nil
	}
	return active.Session.ID
}

func (s *Server) handleBeginTask(ctx context.Context, args BeginTaskArgs) (*mcp.CallToolResult, error) {
	idStr := args.TaskID
	id, err := uuid.Parse(idStr)
	if err != nil {
		return mcp.NewToolResultError("invalid task_id: " + err.Error()), nil
	}

	branchName := args.BranchName
	prURL := args.PRUrl
	if msg := validateBeginTaskLinkageArgs(branchName, prURL); msg != "" {
		return mcp.NewToolResultError(msg), nil
	}

	assignee, assigneeErr := resolveAssignee(args.Assignee)
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

	// U13 Phase B (tools_gtd.go:1668): wrapUntrustedTask ONLY for the
	// response map's "task" field — branch_name_suggestion and
	// attachBeginTaskWorkSession below still use the original unwrapped
	// task (its Title feeds gtd.TitleToBranchSlug and the new work
	// session's own Title/Goal), matching wrapUntrustedTask's
	// copy-not-mutate contract.
	resp := map[string]any{
		"task":                   wrapUntrustedTask(task),
		"branch_name_suggestion": gtd.TitleToBranchSlug(task.Title),
	}
	// work_session_id is a real, persisted worksession.Session row
	// (checkpoint_work/finish_work-compatible) when the side effect
	// succeeds — see attachBeginTaskWorkSession's doc comment (F17). Omitted
	// entirely on failure rather than falling back to a fabricated UUID.
	if sessID := s.attachBeginTaskWorkSession(ctx, id, task, assignee); sessID != uuid.Nil {
		resp["work_session_id"] = sessID.String()
	}
	return jsonText(resp)
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

// renderUpcomingBuckets formats the 5 task buckets as plain text for MCP
// responses. get_upcoming_work (handleGetUpcomingWork) is the one stored-data
// reader in this file the jsonText-based grep cannot find — it returns
// mcp.NewToolResultText, not JSON — so a raw t.Title here was interpolated
// straight into plain text with no boundary-marker neutralisation at all
// (U13 inventory §"Not caught by the jsonText grep at all"). Titles are
// neutralised only, not clipSafe-clipped: this render has never capped
// length (unlike jsonText-based callers that go through wrapUntrustedTask),
// so clipping here would be a new, unrelated behaviour change outside this
// dispatch's scope.
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
			sb = append(sb, fmt.Sprintf("- [%s] %s%s\n", t.Status, neutralizeBoundaryMarkers(t.Title), due)...)
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
