package mcp

import "github.com/google/uuid"

// Typed argument structs for the 20 tools_gtd.go MCP handlers (pilot of the
// handler seam — see docs/adr/0001-mcp-tool-handler-seam.md). Each field is
// tagged `mcp:"<arg name>"` and populated by decodeToolArgs after the seam's
// validate() pass confirms required/enum/maxLength/uuid constraints.
//
// Pointer vs. plain-value fields are a deliberate distinction, not a style
// choice:
//   - Plain value types (string/int32/bool/uuid.UUID) mirror the zero-value
//     "absent == unset" semantics of the legacy stringArg/numberArg/boolArg
//     getters — correct wherever the handler's own business logic already
//     treats an empty/zero value as "not provided" (e.g. numeric ranges
//     gated by `raw > 0`).
//   - Pointer fields (*string/*uuid.UUID/*bool) preserve presence
//     information — required wherever business logic distinguishes "key
//     absent" (preserve existing value) from "key present with an empty/zero
//     value" (explicitly clear/set). This is the seam's stated limit per ADR
//     0001: preserve-on-omit domain defaulting stays visible to the handler,
//     never absorbed into generic validation.

// ListProjectsArgs — list_projects. [F170-04] Limit/Offset are plain int32
// like ListTasksArgs': absent decodes to 0, and listPageBounds turns 0 into
// the default page size, so presence never needs to be distinguished here.
type ListProjectsArgs struct {
	Limit  int32 `mcp:"limit"`
	Offset int32 `mcp:"offset"`
}

// CreateProjectArgs — create_project.
type CreateProjectArgs struct {
	Name        string     `mcp:"name"`
	Title       string     `mcp:"title"`
	Area        string     `mcp:"area"`
	Description string     `mcp:"description"`
	GoalID      *uuid.UUID `mcp:"goal_id"`
	Priority    int32      `mcp:"priority"`
	RepoName    string     `mcp:"repo_name"`
}

// UpdateProjectArgs — update_project. Title/Description/Area/Status are
// plain strings: buildUpdateProjectParams treats an empty value as "not
// provided, fall back to the existing row" (non-presence-based — mirrors the
// legacy stringArg-then-empty-check pattern; there is no way to explicitly
// clear these fields via this API). GoalID/RepoName are *string
// (preserve-on-omit: nil = absent = keep existing row value, non-nil =
// explicitly passed, including empty-string-clears-the-link). GoalID is
// *string rather than *uuid.UUID because it has three-way semantics
// (absent=preserve, empty=clear, non-empty=parse+set) that the seam's
// generic UUID validation can't express — handleUpdateProject parses it
// itself (resolveGoalID).
type UpdateProjectArgs struct {
	ProjectID   uuid.UUID `mcp:"project_id"`
	Title       string    `mcp:"title"`
	Description string    `mcp:"description"`
	Area        string    `mcp:"area"`
	Priority    int32     `mcp:"priority"`
	Status      string    `mcp:"status"`
	GoalID      *string   `mcp:"goal_id"`
	RepoName    *string   `mcp:"repo_name"`
}

// ListTasksArgs — list_tasks. Summary is *bool so the handler can tell
// "summary key absent → default true" apart from "summary: false".
type ListTasksArgs struct {
	ProjectID *uuid.UUID `mcp:"project_id"`
	Summary   *bool      `mcp:"summary"`
	Status    string     `mcp:"status"`
	Limit     int32      `mcp:"limit"`
	Offset    int32      `mcp:"offset"`
}

// AddTaskArgs — add_task. Kind, BranchName and PRUrl stay plain strings: kind
// has default-then-validate business logic (validator.IsValidKind) distinct
// from the schema's advisory enum, and branch_name/pr_url go through
// hand-written regex/control-char validation (applyBranchAndPR) — neither is
// a seam-absorbable required/enum/maxLength/uuid check.
type AddTaskArgs struct {
	Title       string     `mcp:"title"`
	ProjectID   *uuid.UUID `mcp:"project_id"`
	Description string     `mcp:"description"`
	Assignee    string     `mcp:"assignee"`
	Priority    int32      `mcp:"priority"`
	Importance  int32      `mcp:"importance"`
	Context     string     `mcp:"context"`
	Kind        string     `mcp:"kind"`
	BranchName  string     `mcp:"branch_name"`
	PRUrl       string     `mcp:"pr_url"`
	DueDate     string     `mcp:"due_date"`
}

// CompleteTaskArgs — complete_task.
type CompleteTaskArgs struct {
	TaskID   uuid.UUID `mcp:"task_id"`
	Artifact string    `mcp:"artifact"`
}

// ListGoalsArgs — list_goals. [F170-05] Same Limit/Offset contract as
// ListProjectsArgs.
type ListGoalsArgs struct {
	Limit  int32 `mcp:"limit"`
	Offset int32 `mcp:"offset"`
}

// CreateGoalArgs — create_goal.
type CreateGoalArgs struct {
	Title       string `mcp:"title"`
	Area        string `mcp:"area"`
	Description string `mcp:"description"`
	DueDate     string `mcp:"due_date"`
}

// UpdateTaskArgs — update_task. Status/Title/Description/Priority/
// Importance/Assignee/DueDate/Context/Kind are plain values: parseUpdateTaskArgs
// already treats an empty/zero value as "not provided" for these fields
// (non-presence-based, matching the legacy stringArg/numberArg checks). Kind
// stays a plain string rather than being seam-absorbed for the same reason
// AddTaskArgs.Kind does (see that doc comment): kind has default-then-validate
// business logic (validator.IsValidKind) distinct from the schema's advisory
// enum. BranchName/PRUrl are *string: applyBranchAndPRUpdate distinguishes
// "key absent" (leave unchanged) from "key present as empty string" (clear
// the field) — genuine presence-based business logic.
type UpdateTaskArgs struct {
	TaskID      uuid.UUID `mcp:"task_id"`
	Status      string    `mcp:"status"`
	Title       string    `mcp:"title"`
	Description string    `mcp:"description"`
	Priority    int32     `mcp:"priority"`
	Importance  int32     `mcp:"importance"`
	Assignee    string    `mcp:"assignee"`
	DueDate     string    `mcp:"due_date"`
	Context     string    `mcp:"context"`
	Kind        string    `mcp:"kind"`
	BranchName  *string   `mcp:"branch_name"`
	PRUrl       *string   `mcp:"pr_url"`
}

// UpdateProjectStatusArgs — update_project_status. Status is required at the
// schema level but NOT declared with mcp.Enum() there (unlike
// update_project's status field) — the seam only derives what's declared, so
// enum-membership stays hand-checked in handleUpdateProjectStatus.
type UpdateProjectStatusArgs struct {
	ProjectID uuid.UUID `mcp:"project_id"`
	Status    string    `mcp:"status"`
}

// GetProjectArgs — get_project.
type GetProjectArgs struct {
	Name string `mcp:"name"`
}

// LogActivityArgs — log_activity.
type LogActivityArgs struct {
	Actor     string     `mcp:"actor"`
	Action    string     `mcp:"action"`
	ProjectID *uuid.UUID `mcp:"project_id"`
	Notes     string     `mcp:"notes"`
}

// GetUpcomingWorkArgs — get_upcoming_work.
type GetUpcomingWorkArgs struct {
	Days int32 `mcp:"days"`
}

// DeleteTaskArgs — delete_task (two-step confirmation flow).
type DeleteTaskArgs struct {
	TaskID        uuid.UUID `mcp:"task_id"`
	Confirm       bool      `mcp:"confirm"`
	DeletionToken string    `mcp:"deletion_token"`
}

// ChecklistAddItemArgs — task_checklist_add_item. Title/FileRef/Notes are
// plain strings: their maxLength enforcement runs on the seam's raw value
// (Pass B), but the handler still applies gtd.SanitiseChecklistText to the
// decoded value before use/persist — sanitisation strips control chars, so
// raw length is always >= sanitised length, meaning the seam's cap can only
// be stricter, never looser, than the legacy post-sanitise check it replaces.
type ChecklistAddItemArgs struct {
	TaskID  uuid.UUID `mcp:"task_id"`
	Title   string    `mcp:"title"`
	FileRef string    `mcp:"file_ref"`
	Notes   string    `mcp:"notes"`
}

// ChecklistToggleArgs — task_checklist_toggle. Done is required+boolean at
// the schema level, so the seam guarantees presence before decode runs — a
// plain bool (not *bool) is correct here.
type ChecklistToggleArgs struct {
	TaskID      uuid.UUID `mcp:"task_id"`
	ItemID      uuid.UUID `mcp:"item_id"`
	Done        bool      `mcp:"done"`
	EvidenceURL string    `mcp:"evidence_url"`
}

// ChecklistCompleteArgs — task_checklist_complete.
type ChecklistCompleteArgs struct {
	TaskID uuid.UUID `mcp:"task_id"`
	ItemID uuid.UUID `mcp:"item_id"`
}

// GetTaskArgs — get_task.
type GetTaskArgs struct {
	TaskID uuid.UUID `mcp:"task_id"`
}

// SetTaskStatusArgs — set_task_status.
type SetTaskStatusArgs struct {
	TaskID uuid.UUID `mcp:"task_id"`
	Status string    `mcp:"status"`
}

// BeginTaskArgs — begin_task. TaskID stays a raw string (not uuid.UUID):
// handleBeginTask keeps its own inline uuid.Parse so it can embed the parse
// error verbatim ("invalid task_id: <err>") — the one documented exception
// noted on requireUUIDArg's doc comment (server.go) for handlers whose
// message needs the underlying parse error, which a seam-level default/
// override message cannot reproduce.
type BeginTaskArgs struct {
	TaskID     string `mcp:"task_id"`
	BranchName string `mcp:"branch_name"`
	PRUrl      string `mcp:"pr_url"`
	Assignee   string `mcp:"assignee"`
}
