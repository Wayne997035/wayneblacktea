package gtd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Store handles all database operations for the GTD bounded context.
//
// Every method automatically applies the configured workspace scope: NULL →
// no filter (legacy mode); set → strict per-workspace reads and writes.
type Store struct {
	q           *db.Queries
	dbtx        db.DBTX // retained for hand-written queries outside of sqlc
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given DBTX (pool or transaction)
// scoped to the optional workspaceID. nil workspaceID = legacy unscoped mode.
func NewStore(dbtx db.DBTX, workspaceID *uuid.UUID) *Store {
	return &Store{q: db.New(dbtx), dbtx: dbtx, workspaceID: toUUID(workspaceID)}
}

// WithTx returns a Store bound to tx, preserving the workspace scope, for use
// in multi-store transactions.
func (s *Store) WithTx(tx pgx.Tx) *Store {
	return &Store{q: s.q.WithTx(tx), dbtx: tx, workspaceID: s.workspaceID}
}

// WorkspaceID exposes the configured workspace UUID (or zero pgtype.UUID).
func (s *Store) WorkspaceID() pgtype.UUID {
	return s.workspaceID
}

func toText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

func toTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// ListActiveProjects returns all active projects ordered by priority.
func (s *Store) ListActiveProjects(ctx context.Context) ([]db.Project, error) {
	rows, err := s.q.ListActiveProjects(ctx, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing active projects: %w", err)
	}
	return rows, nil
}

// GetProjectByID returns a single project by UUID, regardless of status.
func (s *Store) GetProjectByID(ctx context.Context, id uuid.UUID) (*db.Project, error) {
	row, err := s.q.GetProjectByID(ctx, db.GetProjectByIDParams{
		ID:          id,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying project %s: %w", id, err)
	}
	return &row, nil
}

// ProjectByName returns a single project by unique name.
func (s *Store) ProjectByName(ctx context.Context, name string) (*db.Project, error) {
	row, err := s.q.GetProjectByName(ctx, db.GetProjectByNameParams{
		Name:        name,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying project %q: %w", name, err)
	}
	return &row, nil
}

// ProjectsByRepoName returns every project whose `repo_name` column matches
// the given repo, scoped to the configured workspace. Empty result is not an
// error — repos without paired projects are normal.
//
// Hand-rolled (not sqlc) so adding the new column doesn't require a sqlc
// regen on every contributor's machine. Returns []db.Project (existing struct
// fields only) so we don't have to bump db.Project schema either.
//
// Empty input → empty result fast-path (avoids a wildcard scan on the
// composite index).
func (s *Store) ProjectsByRepoName(ctx context.Context, repoName string) ([]db.Project, error) {
	if repoName == "" {
		return nil, nil
	}
	const q = `SELECT id, goal_id, name, title, description, status, area, priority,
		created_at, updated_at, workspace_id
		FROM projects
		WHERE repo_name = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY priority ASC, created_at ASC`
	rows, err := s.dbtx.Query(ctx, q, repoName, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing projects for repo %q: %w", repoName, err)
	}
	defer rows.Close()
	var out []db.Project
	for rows.Next() {
		var p db.Project
		if err := rows.Scan(
			&p.ID, &p.GoalID, &p.Name, &p.Title, &p.Description, &p.Status, &p.Area, &p.Priority,
			&p.CreatedAt, &p.UpdatedAt, &p.WorkspaceID,
		); err != nil {
			return nil, fmt.Errorf("scanning project for repo %q: %w", repoName, err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating projects for repo %q: %w", repoName, err)
	}
	return out, nil
}

// CreateProject inserts a new project.
func (s *Store) CreateProject(ctx context.Context, p CreateProjectParams) (*db.Project, error) {
	area := p.Area
	if area == "" {
		area = "projects"
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	row, err := s.q.CreateProject(ctx, db.CreateProjectParams{
		GoalID:      toUUID(p.GoalID),
		Name:        p.Name,
		Title:       p.Title,
		Description: toText(p.Description),
		Area:        area,
		Priority:    priority,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("creating project %q: %w", p.Name, err)
	}
	return &row, nil
}

// Tasks returns pending/in-progress tasks, optionally filtered by project.
func (s *Store) Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error) {
	if projectID != nil {
		rows, err := s.q.GetTasksByProject(ctx, db.GetTasksByProjectParams{
			ProjectID:   toUUID(projectID),
			WorkspaceID: s.workspaceID,
		})
		if err != nil {
			return nil, fmt.Errorf("listing tasks for project %s: %w", *projectID, err)
		}
		return rows, nil
	}
	rows, err := s.q.GetAllPendingTasks(ctx, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing all pending tasks: %w", err)
	}
	return rows, nil
}

// TasksByProjectAllStatuses returns every task belonging to projectID
// regardless of status, ordered by COALESCE(updated_at, created_at) DESC.
// The active-only Tasks(...) is the right call for GTD list pages; this
// variant exists so the project-detail UI can render a "completed" section
// without resurrecting completed rows in the global pending lists.
func (s *Store) TasksByProjectAllStatuses(ctx context.Context, projectID uuid.UUID) ([]db.Task, error) {
	rows, err := s.q.ListProjectTasksAllStatuses(ctx, db.ListProjectTasksAllStatusesParams{
		ProjectID:   toUUID(&projectID),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("listing all-status tasks for project %s: %w", projectID, err)
	}
	return rows, nil
}

// TasksByDueDateRange returns pending / in_progress tasks whose due_date
// falls inside [from, to] (inclusive on both ends), scoped to the
// configured workspace. The status filter intentionally excludes
// 'completed' so the calendar's planning view shows only work that still
// needs to happen.
//
// Hand-rolled query (rather than sqlc-generated) keeps the timeline
// feature self-contained without churning the queries.sql codegen surface.
func (s *Store) TasksByDueDateRange(ctx context.Context, from, to time.Time) ([]db.Task, error) {
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context
		FROM tasks
		WHERE status IN ('pending','in_progress')
		  AND due_date IS NOT NULL
		  AND due_date >= $1
		  AND due_date <= $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY due_date ASC, created_at ASC`
	rows, err := s.dbtx.Query(ctx, q, from, to, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks by due date range: %w", err)
	}
	defer rows.Close()
	var out []db.Task
	for rows.Next() {
		var t db.Task
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context,
		); err != nil {
			return nil, fmt.Errorf("scanning task by due date: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tasks by due date: %w", err)
	}
	return out, nil
}

// TasksForTimeline returns all tasks (any status) where created_at OR
// (status='completed' AND updated_at) falls inside [from, to] (inclusive),
// scoped to the configured workspace. Used by the timeline aggregator to
// build historical task_created / task_completed events without scanning
// every active task in the workspace.
//
// Hand-rolled query (not sqlc) to keep the timeline feature self-contained
// without churning the queries.sql codegen surface.
func (s *Store) TasksForTimeline(ctx context.Context, from, to time.Time) ([]db.Task, error) {
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context
		FROM tasks
		WHERE (
		    (created_at >= $1 AND created_at <= $2)
		    OR (status = 'completed' AND updated_at >= $1 AND updated_at <= $2)
		)
		AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY COALESCE(updated_at, created_at) DESC
		LIMIT 10000`
	rows, err := s.dbtx.Query(ctx, q, from, to, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing tasks for timeline [%s, %s]: %w", from.Format(time.RFC3339), to.Format(time.RFC3339), err)
	}
	defer rows.Close()
	var out []db.Task
	for rows.Next() {
		var t db.Task
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context,
		); err != nil {
			return nil, fmt.Errorf("scanning task for timeline: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating tasks for timeline: %w", err)
	}
	return out, nil
}

// UpcomingTasks returns pending and in_progress tasks relevant to the upcoming
// window anchored at refDate (in the given tz). The days parameter controls how
// far ahead the "upcoming" bucket extends beyond today+2; limit caps the total
// row count returned before Go-side grouping so callers can apply limit after
// bucket assignment.
//
// The query fetches:
//   - All pending/in_progress tasks with a due_date up to refDate+days (in UTC)
//   - All pending/in_progress tasks with importance=1 and no due_date
//     (for the unscheduled_important bucket)
//
// Hand-rolled (not sqlc) to keep this feature self-contained.
func (s *Store) UpcomingTasks(ctx context.Context, refDate time.Time, days, limit int) ([]db.Task, error) {
	// Extend the window to end of day on refDate + days in UTC.
	windowEnd := refDate.UTC().AddDate(0, 0, days).Truncate(24 * time.Hour).Add(24*time.Hour - time.Nanosecond)
	// We fetch a bit more than limit to allow Go-side grouping to fill all
	// buckets; multiply by 2 as a practical over-fetch factor, floor at limit.
	fetchLimit := limit * 2
	if fetchLimit < limit {
		fetchLimit = limit
	}
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context
		FROM tasks
		WHERE status IN ('pending','in_progress')
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND (
		      (due_date IS NOT NULL AND due_date <= $2)
		      OR (due_date IS NULL AND importance = 1)
		  )
		ORDER BY due_date ASC NULLS LAST, priority ASC, created_at ASC
		LIMIT $3`
	rows, err := s.dbtx.Query(ctx, q, s.workspaceID, windowEnd, fetchLimit)
	if err != nil {
		return nil, fmt.Errorf("listing upcoming tasks: %w", err)
	}
	defer rows.Close()
	var out []db.Task
	for rows.Next() {
		var t db.Task
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context,
		); err != nil {
			return nil, fmt.Errorf("scanning upcoming task: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating upcoming tasks: %w", err)
	}
	return out, nil
}

// CreateTask inserts a new task.
func (s *Store) CreateTask(ctx context.Context, p CreateTaskParams) (*db.Task, error) {
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	row, err := s.q.CreateTask(ctx, db.CreateTaskParams{
		ProjectID:   toUUID(p.ProjectID),
		Title:       p.Title,
		Description: toText(p.Description),
		Priority:    priority,
		Assignee:    toText(p.Assignee),
		DueDate:     toTimestamptz(p.DueDate),
		Importance:  toInt2(p.Importance),
		Context:     toText(p.Context),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("creating task %q: %w", p.Title, err)
	}
	return &row, nil
}

func toInt2(v *int16) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: *v, Valid: true}
}

// CompleteTask marks a task as completed with an optional artifact URL.
func (s *Store) CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (*db.Task, error) {
	var art pgtype.Text
	if artifact != nil {
		art = pgtype.Text{String: *artifact, Valid: true}
	}
	row, err := s.q.CompleteTask(ctx, db.CompleteTaskParams{
		ID:          id,
		Artifact:    art,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("completing task %s: %w", id, err)
	}
	return &row, nil
}

// LogActivity records an activity entry.
func (s *Store) LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error {
	_, err := s.q.CreateActivityLog(ctx, db.CreateActivityLogParams{
		Actor:       actor,
		ProjectID:   toUUID(projectID),
		Action:      action,
		Notes:       toText(notes),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return fmt.Errorf("logging activity: %w", err)
	}
	return nil
}

// ActiveGoals returns all active goals ordered by due date.
func (s *Store) ActiveGoals(ctx context.Context) ([]db.Goal, error) {
	rows, err := s.q.ListActiveGoals(ctx, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing active goals: %w", err)
	}
	return rows, nil
}

// CreateGoal inserts a new goal.
func (s *Store) CreateGoal(ctx context.Context, p CreateGoalParams) (*db.Goal, error) {
	row, err := s.q.CreateGoal(ctx, db.CreateGoalParams{
		Title:       p.Title,
		Description: toText(p.Description),
		Area:        toText(p.Area),
		DueDate:     toTimestamptz(p.DueDate),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("creating goal %q: %w", p.Title, err)
	}
	return &row, nil
}

// UpdateTaskStatus sets the status of a task by ID.
func (s *Store) UpdateTaskStatus(ctx context.Context, id uuid.UUID, status TaskStatus) (*db.Task, error) {
	row, err := s.q.UpdateTaskStatus(ctx, db.UpdateTaskStatusParams{
		ID:          id,
		Status:      string(status),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating task %s status: %w", id, err)
	}
	return &row, nil
}

// getTaskByID fetches a single task by ID, scoped to the configured workspace.
// Returns ErrNotFound when no matching row exists. Hand-rolled query (not sqlc)
// to avoid churning the codegen surface for a single read.
func (s *Store) getTaskByID(ctx context.Context, id uuid.UUID) (*db.Task, error) {
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context
		FROM tasks
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		LIMIT 1`
	rows, err := s.dbtx.Query(ctx, q, id, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("querying task %s: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterating task %s: %w", id, err)
		}
		return nil, ErrNotFound
	}
	var t db.Task
	if err := rows.Scan(
		&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
		&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context,
	); err != nil {
		return nil, fmt.Errorf("scanning task %s: %w", id, err)
	}
	return &t, nil
}

// coalesceString returns ptr if non-nil, otherwise fallback.
func coalesceString(ptr *string, fallback string) string {
	if ptr != nil {
		return *ptr
	}
	return fallback
}

// coalesceInt32 returns ptr if non-nil, otherwise fallback.
func coalesceInt32(ptr *int32, fallback int32) int32 {
	if ptr != nil {
		return *ptr
	}
	return fallback
}

// UpdateTask performs a partial update of a task by ID. nil fields in p are
// preserved from the existing row (no null-clear support). Pre-reads the existing
// task to fill nil params, then executes a single UPDATE RETURNING.
// Returns ErrNotFound when no row matching id exists in the configured workspace.
func (s *Store) UpdateTask(ctx context.Context, id uuid.UUID, p UpdateTaskParams) (*db.Task, error) {
	existing, err := s.getTaskByID(ctx, id)
	if err != nil {
		return nil, err // ErrNotFound propagated as-is
	}

	// Merge: keep existing values for unspecified fields.
	title := coalesceString(p.Title, existing.Title)

	var description pgtype.Text
	if p.Description != nil {
		description = toText(*p.Description)
	} else {
		description = existing.Description
	}

	priority := coalesceInt32(p.Priority, existing.Priority)

	var importance pgtype.Int2
	if p.Importance != nil {
		importance = pgtype.Int2{Int16: *p.Importance, Valid: true}
	} else {
		importance = existing.Importance
	}

	var assignee pgtype.Text
	if p.Assignee != nil {
		assignee = toText(*p.Assignee)
	} else {
		assignee = existing.Assignee
	}

	var dueDate pgtype.Timestamptz
	if p.DueDate != nil {
		dueDate = toTimestamptz(p.DueDate)
	} else {
		dueDate = existing.DueDate
	}

	var taskContext pgtype.Text
	if p.Context != nil {
		taskContext = toText(*p.Context)
	} else {
		taskContext = existing.Context
	}

	var status string
	if p.Status != nil {
		status = *p.Status
	} else {
		status = existing.Status
	}

	const q = `UPDATE tasks
		SET title       = $1,
		    description = $2,
		    priority    = $3,
		    importance  = $4,
		    assignee    = $5,
		    due_date    = $6,
		    context     = $7,
		    status      = $8,
		    updated_at  = NOW()
		WHERE id = $9
		  AND ($10::uuid IS NULL OR workspace_id = $10)
		RETURNING id, project_id, title, description, status, priority, assignee,
		          due_date, artifact, created_at, updated_at, workspace_id, importance, context`
	rows, err := s.dbtx.Query(ctx, q,
		title, description, priority, importance, assignee, dueDate, taskContext, status,
		id, s.workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("updating task %s: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if rErr := rows.Err(); rErr != nil {
			return nil, fmt.Errorf("iterating update result for task %s: %w", id, rErr)
		}
		return nil, ErrNotFound
	}
	var t db.Task
	if err := rows.Scan(
		&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
		&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context,
	); err != nil {
		return nil, fmt.Errorf("scanning updated task %s: %w", id, err)
	}
	return &t, nil
}

// UpdateGoal performs a full update of a goal by ID, replacing all mutable fields.
func (s *Store) UpdateGoal(ctx context.Context, id uuid.UUID, p UpdateGoalParams) (*db.Goal, error) {
	row, err := s.q.UpdateGoal(ctx, db.UpdateGoalParams{
		ID:          id,
		Title:       p.Title,
		Description: toText(p.Description),
		Area:        toText(p.Area),
		Status:      string(p.Status),
		DueDate:     toTimestamptz(p.DueDate),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating goal %s: %w", id, err)
	}
	return &row, nil
}

// UpdateProject performs a full update of a project by ID, replacing all mutable fields.
func (s *Store) UpdateProject(ctx context.Context, id uuid.UUID, p UpdateProjectParams) (*db.Project, error) {
	area := p.Area
	if area == "" {
		area = "projects"
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	row, err := s.q.UpdateProject(ctx, db.UpdateProjectParams{
		ID:          id,
		Title:       p.Title,
		Description: toText(p.Description),
		Area:        area,
		Priority:    priority,
		Status:      string(p.Status),
		GoalID:      toUUID(p.GoalID),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating project %s: %w", id, err)
	}
	return &row, nil
}

// UpdateProjectStatus sets the status of a project by ID.
func (s *Store) UpdateProjectStatus(ctx context.Context, id uuid.UUID, status ProjectStatus) (*db.Project, error) {
	row, err := s.q.UpdateProjectStatus(ctx, db.UpdateProjectStatusParams{
		ID:          id,
		Status:      string(status),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("updating project %s status: %w", id, err)
	}
	return &row, nil
}

// txBeginner abstracts the Begin method shared by *pgxpool.Pool and pgx.Tx
// (a tx Begin starts a savepoint, which preserves atomicity for nested calls).
// Implemented by both so DeleteTask works whether the Store was constructed
// from a pool or already inside a transaction via WithTx.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// DeleteTask permanently removes a task by ID and replicates the cascade
// behaviour previously enforced by foreign keys (red line #9; see migration
// 000026):
//
//   - work_session_tasks rows referencing the deleted task are removed
//     (was ON DELETE CASCADE)
//   - work_sessions.current_task_id pointing at the deleted task is set NULL
//     (was ON DELETE SET NULL)
//
// All statements run inside a single transaction so a partial state is
// impossible. Workspace authorisation is enforced by an explicit pre-check
// inside the tx BEFORE any cleanup runs: if the task does not exist in the
// configured workspace the tx is rolled back and the call is a silent no-op
// (matching the pre-fix behaviour where a workspace-mismatched DELETE simply
// affected 0 rows). The pre-check ensures cleanup never touches another
// workspace's join rows or work_sessions; the parent DELETE's workspace
// filter is now redundant defence-in-depth.
func (s *Store) DeleteTask(ctx context.Context, id uuid.UUID) error {
	beginner, ok := s.dbtx.(txBeginner)
	if !ok {
		return fmt.Errorf("deleting task %s: dbtx does not support Begin (cannot run cascade cleanup atomically)", id)
	}

	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("deleting task %s: begin tx: %w", id, err)
	}
	committed := false
	defer func() {
		if !committed {
			// Best-effort rollback; ignore error because tx may already have
			// closed if Commit succeeded between the check and this defer.
			_ = tx.Rollback(ctx)
		}
	}()

	// 0. Workspace authorisation pre-check. The cleanup statements below are
	// keyed only by task_id, so without this guard a cross-workspace caller
	// could delete another workspace's join rows / NULL its current_task_id
	// pointer (the parent DELETE's workspace filter would 0-row but the
	// damage to neighbouring tables would already be done).
	var exists bool
	if err = tx.QueryRow(ctx,
		`SELECT EXISTS(
		    SELECT 1 FROM tasks
		     WHERE id = $1
		       AND ($2::uuid IS NULL OR workspace_id = $2)
		 )`, id, s.workspaceID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("deleting task %s: workspace pre-check: %w", id, err)
	}
	if !exists {
		// Roll back the empty tx and silently no-op, matching the pre-fix
		// behaviour where a missing / workspace-mismatched task simply
		// affected 0 rows on the parent DELETE.
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("deleting task %s: rollback after workspace miss: %w", id, rbErr)
		}
		committed = true // suppress the deferred Rollback (already done).
		return nil
	}

	// 1. Remove join-table rows (was ON DELETE CASCADE on work_session_tasks.task_id).
	if _, err = tx.Exec(ctx,
		`DELETE FROM work_session_tasks WHERE task_id = $1`, id,
	); err != nil {
		return fmt.Errorf("deleting task %s: cleanup work_session_tasks: %w", id, err)
	}

	// 2. NULL out work_sessions.current_task_id (was ON DELETE SET NULL).
	if _, err = tx.Exec(ctx,
		`UPDATE work_sessions
		   SET current_task_id = NULL,
		       updated_at      = NOW()
		 WHERE current_task_id = $1`, id,
	); err != nil {
		return fmt.Errorf("deleting task %s: nullify work_sessions.current_task_id: %w", id, err)
	}

	// 3. Delete the task itself, scoped to the configured workspace.
	if err = s.q.WithTx(tx).DeleteTask(ctx, db.DeleteTaskParams{
		ID:          id,
		WorkspaceID: s.workspaceID,
	}); err != nil {
		return fmt.Errorf("deleting task %s: %w", id, err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("deleting task %s: commit: %w", id, err)
	}
	committed = true
	return nil
}

// ListActivityLogsSince returns activity_log rows created on or after since,
// scoped to the configured workspace. Results are ordered created_at ASC.
// maxRows caps the result set; callers should use a sensible bound (e.g. 500).
func (s *Store) ListActivityLogsSince(ctx context.Context, since time.Time, maxRows int32) ([]db.ActivityLog, error) {
	const q = `SELECT id, actor, project_id, action, notes, created_at, workspace_id
		FROM activity_log
		WHERE created_at >= $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY created_at ASC
		LIMIT $3`
	rows, err := s.dbtx.Query(ctx, q, since, s.workspaceID, maxRows)
	if err != nil {
		return nil, fmt.Errorf("listing activity logs since %s: %w", since.Format(time.RFC3339), err)
	}
	defer rows.Close()
	var out []db.ActivityLog
	for rows.Next() {
		var a db.ActivityLog
		if err := rows.Scan(
			&a.ID, &a.Actor, &a.ProjectID, &a.Action, &a.Notes, &a.CreatedAt, &a.WorkspaceID,
		); err != nil {
			return nil, fmt.Errorf("scanning activity log: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating activity logs: %w", err)
	}
	return out, nil
}

// TopPendingTask returns the single highest-priority pending task in the
// configured workspace, ordered by priority ASC NULLS LAST, importance ASC
// NULLS LAST, created_at ASC. Returns nil, nil when no pending task exists.
func (s *Store) TopPendingTask(ctx context.Context) (*db.Task, error) {
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context
		FROM tasks
		WHERE status = 'pending'
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY priority ASC NULLS LAST, importance ASC NULLS LAST, created_at ASC
		LIMIT 1`
	rows, err := s.dbtx.Query(ctx, q, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("querying top pending task: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterating top pending task: %w", err)
		}
		return nil, nil //nolint:nilnil // sentinel: no pending task is not an error; callers render {"task":null}
	}
	var t db.Task
	if err := rows.Scan(
		&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
		&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context,
	); err != nil {
		return nil, fmt.Errorf("scanning top pending task: %w", err)
	}
	return &t, nil
}

// RecentCompletedTasks returns recently-completed tasks for a project,
// scoped to the configured workspace, ordered by updated_at DESC. Hand-written
// query (not sqlc) to avoid touching codegen for a single new read.
func (s *Store) RecentCompletedTasks(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Task, error) {
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context
		FROM tasks
		WHERE status = 'completed'
		  AND project_id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY updated_at DESC, id DESC
		LIMIT $3`
	rows, err := s.dbtx.Query(ctx, q, projectID, s.workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing recent completed tasks for project %s: %w", projectID, err)
	}
	defer rows.Close()
	out := make([]db.Task, 0, limit)
	for rows.Next() {
		var t db.Task
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context,
		); err != nil {
			return nil, fmt.Errorf("scanning recent completed task: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating recent completed tasks: %w", err)
	}
	return out, nil
}

// RecentActivityByProject returns activity_log rows for a project since the
// given timestamp, scoped to the configured workspace, newest first. maxRows
// caps the result set so a hot project can't blow out memory.
func (s *Store) RecentActivityByProject(
	ctx context.Context, projectID uuid.UUID, since time.Time, maxRows int32,
) ([]db.ActivityLog, error) {
	const q = `SELECT id, actor, project_id, action, notes, created_at, workspace_id
		FROM activity_log
		WHERE project_id = $1
		  AND created_at >= $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY created_at DESC, id DESC
		LIMIT $4`
	rows, err := s.dbtx.Query(ctx, q, projectID, since, s.workspaceID, maxRows)
	if err != nil {
		return nil, fmt.Errorf("listing recent activity for project %s: %w", projectID, err)
	}
	defer rows.Close()
	out := make([]db.ActivityLog, 0, maxRows)
	for rows.Next() {
		var a db.ActivityLog
		if err := rows.Scan(
			&a.ID, &a.Actor, &a.ProjectID, &a.Action, &a.Notes, &a.CreatedAt, &a.WorkspaceID,
		); err != nil {
			return nil, fmt.Errorf("scanning recent activity: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating recent activity: %w", err)
	}
	return out, nil
}

// WeeklyProgress returns completed task count this week and total active task count.
func (s *Store) WeeklyProgress(ctx context.Context) (completed, total int64, err error) {
	completed, err = s.q.CountCompletedTasksThisWeek(ctx, s.workspaceID)
	if err != nil {
		return 0, 0, fmt.Errorf("counting completed tasks: %w", err)
	}
	total, err = s.q.CountTotalActiveTasks(ctx, s.workspaceID)
	if err != nil {
		return 0, 0, fmt.Errorf("counting active tasks: %w", err)
	}
	return completed, total, nil
}
