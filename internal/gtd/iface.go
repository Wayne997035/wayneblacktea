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
	CreateTask(ctx context.Context, p CreateTaskParams) (*db.Task, error)
	CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (*db.Task, error)
	// BeginTask atomically sets task status to in_progress and records a
	// work_session_started activity log entry. Returns ErrNotFound when no task
	// matches id + workspaceID.
	// Idempotent: if already in_progress, returns the task without error and
	// without writing a duplicate activity_log entry.
	BeginTask(ctx context.Context, id uuid.UUID, workspaceID uuid.UUID) (*db.Task, error)
	LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error
	// ListActivityLogsSince returns activity_log rows created on or after since,
	// scoped to the configured workspace. Results are ordered by created_at ASC.
	// maxRows caps the result set to prevent unbounded memory usage.
	ListActivityLogsSince(ctx context.Context, since time.Time, maxRows int32) ([]db.ActivityLog, error)
	ActiveGoals(ctx context.Context) ([]db.Goal, error)
	CreateGoal(ctx context.Context, p CreateGoalParams) (*db.Goal, error)
	UpdateTaskStatus(ctx context.Context, id uuid.UUID, status TaskStatus) (*db.Task, error)
	// UpdateTask performs a partial update of a task, replacing only the fields
	// that are non-nil in p. Nil fields are preserved from the existing row.
	// Returns ErrNotFound when no row matching id exists in the configured workspace.
	UpdateTask(ctx context.Context, id uuid.UUID, p UpdateTaskParams) (*db.Task, error)
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
