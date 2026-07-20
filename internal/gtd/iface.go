package gtd

import (
	"context"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// StoreIface is the backend-agnostic contract for the GTD bounded context.
// Postgres-backed *Store satisfies this interface; an upcoming SQLite-backed
// store will satisfy the same surface to enable swapping without changing
// HTTP handlers or MCP tools.
//
// Transactional helpers (WithTx) are intentionally absent — atomic
// multi-store operations stay on concrete pg.Store until the cross-backend
// unit-of-work design is settled (Phase C+).
type StoreIface interface {
	ListActiveProjects(ctx context.Context) ([]db.Project, error)
	GetProjectByID(ctx context.Context, id uuid.UUID) (*db.Project, error)
	ProjectByName(ctx context.Context, name string) (*db.Project, error)
	// ProjectsByRepoName returns every project whose `repo_name` column matches
	// the given repo, scoped to the configured workspace. Empty input or no
	// matches → empty slice (not an error). Used by the workspace overview
	// drill-down so a repo can list multiple paired projects.
	ProjectsByRepoName(ctx context.Context, repoName string) ([]db.Project, error)
	CreateProject(ctx context.Context, p CreateProjectParams) (*db.Project, error)
	Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error)
	// TasksFiltered returns tasks matching f, with pagination. This is the
	// query backing the list_tasks MCP tool. The existing Tasks method is
	// left unchanged so its 16 non-test callers keep active-only semantics.
	// Status "" or "active" → pending+in_progress; "all" → every task status;
	// any other value → exact match. Callers pass Limit+1 to detect has_more
	// without a COUNT query.
	TasksFiltered(ctx context.Context, f TaskFilter) ([]db.Task, error)
	// TasksByProjectAllStatuses returns ALL tasks for the project regardless
	// of status (pending / in_progress / completed / cancelled), ordered by
	// COALESCE(updated_at, created_at) DESC. Used by the project-detail UI
	// to render the "completed" section alongside open tasks. The active-only
	// Tasks variant remains the default for GTD list pages.
	TasksByProjectAllStatuses(ctx context.Context, projectID uuid.UUID) ([]db.Task, error)
	// TasksByDueDateRange returns pending or in_progress tasks whose
	// due_date falls inside [from, to] (inclusive on both ends), scoped to
	// the configured workspace. Used by the calendar timeline to surface
	// forward-looking "task_due" planning events. Results are ordered by
	// due_date ASC, created_at ASC for stable pagination.
	TasksByDueDateRange(ctx context.Context, from, to time.Time) ([]db.Task, error)
	// UpcomingTasks returns pending/in_progress tasks relevant to the upcoming
	// window rooted at refDate. days controls how far ahead to look; limit caps
	// the pre-grouping row count. Tasks with importance=1 and no due_date are
	// also included for the unscheduled_important bucket. Workspace scoped.
	UpcomingTasks(ctx context.Context, refDate time.Time, days, limit int) ([]db.Task, error)
	// TasksForTimeline returns all tasks (any status) where created_at OR
	// (status='completed' AND updated_at) falls inside [from, to] (inclusive).
	// Used by the timeline aggregator to surface task_created and task_completed
	// historical events from a date-range query instead of scanning every active
	// task. Results are ordered by COALESCE(updated_at, created_at) DESC,
	// capped at 10000 rows. Workspace scoping is applied by the implementation.
	TasksForTimeline(ctx context.Context, from, to time.Time) ([]db.Task, error)
	// PullForwardTasks returns up to PullForwardCap pending/in_progress tasks
	// with importance=1 (high) whose due_date is NULL or falls on/after
	// "tomorrow" (the day after refDate, evaluated in UTC). Tasks due today or
	// overdue are excluded — those already surface via UpcomingTasks /
	// GroupUpcomingTasks as "today" work; this query is specifically for
	// important work that hasn't yet entered the due-date radar, so it isn't
	// silently missed until due_date or a manual list_tasks query surfaces it.
	// Ordered due_date ASC NULLS LAST, priority ASC. Workspace scoped,
	// read-only (no mutation), evaluated fresh on every call — no caching or
	// persisted "already pulled" state.
	PullForwardTasks(ctx context.Context, refDate time.Time) ([]db.Task, error)
	CreateTask(ctx context.Context, p CreateTaskParams) (*db.Task, error)
	CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (*db.Task, error)
	// BatchCompleteTasksByPRMatch marks every Match in matches as completed,
	// sets pr_url + artifact = PR URL, and writes a single activity_log entry per
	// task (action="pr_auto_close"). Used by the reconcile_merged_prs MCP tool /
	// HTTP handler (sprint feature/gtd-enforce-server-side GTD-fix 9/12).
	//
	// Idempotent: a task already in 'completed' status is left untouched and not
	// re-logged. The guarded UPDATE also requires status IN
	// ('pending','in_progress') at apply time, not merely "not completed" — a
	// task cancelled between preview and confirm is likewise skipped (CWE-367
	// TOCTOU guard).
	//
	// Returns a map keyed by every Match's TaskID whose guarded UPDATE actually
	// affected a row this call (i.e. genuinely transitioned to completed).
	// Task IDs skipped by the guard (already completed, or drifted away from
	// pending/in_progress since preview) are simply ABSENT from the map — they
	// are never present with a false value, so callers can use the plain index
	// expression `applied[id]` (zero-value false for an absent key) or the
	// comma-ok form interchangeably.
	//
	// Callers MUST consult this map (not just its length) before treating a
	// match as "really applied" — e.g. before writing an audit-trail row via
	// completioncandidate.WriteAutoApplied, so a match the guard skipped never
	// produces a false "auto_applied" record (round-3 Finding 2). len(applied)
	// gives the aggregate applied count previously returned as a bare int.
	//
	// Returns an empty (non-nil) map + nil error on empty input.
	BatchCompleteTasksByPRMatch(ctx context.Context, matches []Match) (map[uuid.UUID]bool, error)
	// BeginTask atomically sets task status to in_progress and records a
	// work_session_started activity log entry. Returns ErrNotFound when no task
	// matches id inside the Store's configured workspace.
	// Idempotent: if already in_progress, returns the task without error and
	// without writing a duplicate activity_log entry.
	BeginTask(ctx context.Context, id uuid.UUID) (*db.Task, error)
	LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error
	// ListActivityLogsSince returns activity_log rows created on or after since,
	// scoped to the configured workspace. Results are ordered by created_at ASC.
	// maxRows caps the result set to prevent unbounded memory usage.
	ListActivityLogsSince(ctx context.Context, since time.Time, maxRows int32) ([]db.ActivityLog, error)
	// PruneOlderThan hard-deletes activity_log rows created before cutoff.
	// Global cleanup (no workspace filter) — called daily by the scheduler to
	// enforce the 365-day TTL per backend-security-design.md §1.3.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
	ActiveGoals(ctx context.Context) ([]db.Goal, error)
	CreateGoal(ctx context.Context, p CreateGoalParams) (*db.Goal, error)
	UpdateTaskStatus(ctx context.Context, id uuid.UUID, status TaskStatus) (*db.Task, error)
	// UpdateTask performs a partial update of a task, replacing only the fields
	// that are non-nil in p. Nil fields are preserved from the existing row.
	// Returns ErrNotFound when no row matching id exists in the configured workspace.
	UpdateTask(ctx context.Context, id uuid.UUID, p UpdateTaskParams) (*db.Task, error)
	// GetTaskByID returns a single task by UUID, scoped to the configured workspace.
	// Returns ErrNotFound when no matching row exists.
	GetTaskByID(ctx context.Context, id uuid.UUID) (*db.Task, error)
	UpdateProjectStatus(ctx context.Context, id uuid.UUID, status ProjectStatus) (*db.Project, error)
	// UpdateGoal performs a full update of a goal, replacing all mutable fields.
	UpdateGoal(ctx context.Context, id uuid.UUID, p UpdateGoalParams) (*db.Goal, error)
	// UpdateProject performs a full update of a project, replacing all mutable fields.
	UpdateProject(ctx context.Context, id uuid.UUID, p UpdateProjectParams) (*db.Project, error)
	DeleteTask(ctx context.Context, id uuid.UUID) error
	WeeklyProgress(ctx context.Context) (completed, total int64, err error)
	// AddChecklistItem appends a new ChecklistItem to the task's checklist and
	// returns the full updated slice. The item's ID is generated server-side.
	// Returns ErrNotFound when no task matches taskID + workspaceID.
	AddChecklistItem(ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID, item ChecklistItem) ([]ChecklistItem, error)
	// UpdateChecklistItem applies a partial patch to the checklist item identified
	// by itemID inside the given task. Returns the full updated checklist.
	// Returns ErrNotFound when task or item is not found.
	UpdateChecklistItem(
		ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID,
		itemID uuid.UUID, update UpdateChecklistItemParams,
	) ([]ChecklistItem, error)
	// DeleteChecklistItem removes the item identified by itemID from the task's
	// checklist. Returns ErrNotFound when task or item is not found.
	DeleteChecklistItem(ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID, itemID uuid.UUID) error
	// TopPendingTask returns the single highest-priority pending task scoped to
	// the configured workspace. Returns nil, nil when none exist.
	TopPendingTask(ctx context.Context) (*db.Task, error)
	// RecentCompletedTasks returns recently-completed tasks for a project,
	// scoped to the configured workspace, ordered by updated_at DESC. Used by
	// the workspace repo overview to show "what got done lately".
	RecentCompletedTasks(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Task, error)
	// RecentActivityByProject returns activity_log rows for a project since
	// the given timestamp, scoped to the configured workspace, newest first.
	// maxRows caps the result set to bound memory.
	RecentActivityByProject(ctx context.Context, projectID uuid.UUID, since time.Time, maxRows int32) ([]db.ActivityLog, error)
	WorkspaceID() pgtype.UUID
}

// Compile-time assertion: pg-backed Store implements StoreIface.
var _ StoreIface = (*Store)(nil)
