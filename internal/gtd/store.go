package gtd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/pgconv"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// jsonMarshal / jsonUnmarshal are thin aliases for encoding/json so that the
// json package appears referenced from this file (avoiding blank-import hacks).
var (
	jsonMarshal   = json.Marshal
	jsonUnmarshal = json.Unmarshal
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
	return &Store{q: db.New(dbtx), dbtx: dbtx, workspaceID: pgconv.ToUUID(workspaceID)}
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
//
// Hand-rolled INSERT (instead of sqlc CreateProject) so that the repo_name
// column added in migration 000037 is included without requiring a sqlc
// regen on every contributor's machine.
func (s *Store) CreateProject(ctx context.Context, p CreateProjectParams) (*db.Project, error) {
	area := p.Area
	if area == "" {
		area = "projects"
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	const q = `INSERT INTO projects
		(goal_id, name, title, description, status, area, priority, repo_name, workspace_id)
		VALUES ($1, $2, $3, $4, 'active', $5, $6, $7, $8)
		RETURNING id, goal_id, name, title, description, status, area, priority,
		          created_at, updated_at, workspace_id`
	rows, err := s.dbtx.Query(
		ctx, q,
		pgconv.ToUUID(p.GoalID), p.Name, p.Title, pgconv.ToText(p.Description),
		area, priority, pgconv.ToText(p.RepoName), s.workspaceID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("creating project %q: %w", p.Name, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if rErr := rows.Err(); rErr != nil {
			return nil, fmt.Errorf("iterating create project %q: %w", p.Name, rErr)
		}
		return nil, fmt.Errorf("creating project %q: no row returned", p.Name)
	}
	var row db.Project
	if err := rows.Scan(
		&row.ID, &row.GoalID, &row.Name, &row.Title, &row.Description, &row.Status, &row.Area, &row.Priority,
		&row.CreatedAt, &row.UpdatedAt, &row.WorkspaceID,
	); err != nil {
		return nil, fmt.Errorf("scanning created project %q: %w", p.Name, err)
	}
	return &row, nil
}

// Tasks returns pending/in-progress tasks, optionally filtered by project.
func (s *Store) Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error) {
	if projectID != nil {
		rows, err := s.q.GetTasksByProject(ctx, db.GetTasksByProjectParams{
			ProjectID:   pgconv.ToUUID(projectID),
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
		ProjectID:   pgconv.ToUUID(&projectID),
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
		due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		branch_name, pr_url, commit_shas
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
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
			&t.BranchName, &t.PRUrl, &t.CommitSHAs,
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

// TasksFiltered returns tasks matching the given TaskFilter with pagination.
// It is the query backing the list_tasks MCP tool. The existing Tasks method
// is left untouched so its non-test callers keep active-only semantics.
//
// Status "" or "active" → pending+in_progress; "all" → every task status; any
// other value → exact match. Callers pass Limit+1 to detect has_more without a
// COUNT query.
//
// Hand-rolled (not sqlc) to keep this feature self-contained without
// churning the queries.sql codegen surface.
func (s *Store) TasksFiltered(ctx context.Context, f TaskFilter) ([]db.Task, error) {
	const selectCols = `id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		branch_name, pr_url, commit_shas`
	return s.queryFilteredTasks(ctx, selectCols, f)
}

// queryFilteredTasks executes the status-conditional query and scans the results.
func (s *Store) queryFilteredTasks(ctx context.Context, selectCols string, f TaskFilter) ([]db.Task, error) {
	var rows pgx.Rows
	var err error
	var updatedSinceArg any
	if f.UpdatedSince != nil {
		updatedSinceArg = *f.UpdatedSince
	}
	switch f.Status {
	case "", "active":
		q := `SELECT ` + selectCols + `
			FROM tasks
			WHERE status IN ('pending','in_progress')
			  AND ($1::uuid IS NULL OR project_id = $1)
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			  AND ($5::timestamptz IS NULL OR updated_at >= $5)
			ORDER BY priority ASC, created_at ASC
			LIMIT $3 OFFSET $4`
		rows, err = s.dbtx.Query(ctx, q, pgconv.ToUUID(f.ProjectID), s.workspaceID, f.Limit, f.Offset, updatedSinceArg)
	case "all":
		q := `SELECT ` + selectCols + `
			FROM tasks
			WHERE ($1::uuid IS NULL OR project_id = $1)
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			  AND ($5::timestamptz IS NULL OR updated_at >= $5)
			ORDER BY priority ASC, created_at ASC
			LIMIT $3 OFFSET $4`
		rows, err = s.dbtx.Query(ctx, q, pgconv.ToUUID(f.ProjectID), s.workspaceID, f.Limit, f.Offset, updatedSinceArg)
	default:
		q := `SELECT ` + selectCols + `
			FROM tasks
			WHERE status = $1
			  AND ($2::uuid IS NULL OR project_id = $2)
			  AND ($3::uuid IS NULL OR workspace_id = $3)
			  AND ($6::timestamptz IS NULL OR updated_at >= $6)
			ORDER BY priority ASC, created_at ASC
			LIMIT $4 OFFSET $5`
		rows, err = s.dbtx.Query(ctx, q, f.Status, pgconv.ToUUID(f.ProjectID), s.workspaceID, f.Limit, f.Offset, updatedSinceArg)
	}
	if err != nil {
		return nil, fmt.Errorf("listing filtered tasks: %w", err)
	}
	defer rows.Close()
	var out []db.Task
	for rows.Next() {
		var t db.Task
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
			&t.BranchName, &t.PRUrl, &t.CommitSHAs,
		); err != nil {
			return nil, fmt.Errorf("scanning filtered task: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating filtered tasks: %w", err)
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
		due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		branch_name, pr_url, commit_shas
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
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
			&t.BranchName, &t.PRUrl, &t.CommitSHAs,
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
//   - All pending/in_progress tasks with no due_date, regardless of importance
//     (for the unscheduled bucket; priority-ordered so high-importance surfaces first)
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
		due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		branch_name, pr_url, commit_shas
		FROM tasks
		WHERE status IN ('pending','in_progress')
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND (
		      (due_date IS NOT NULL AND due_date <= $2)
		      OR (due_date IS NULL)
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
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
			&t.BranchName, &t.PRUrl, &t.CommitSHAs,
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

// PullForwardTasks returns up to PullForwardCap pending/in_progress tasks
// with importance=1 (high) whose due_date is NULL or falls on/after
// "tomorrow" (midnight, Asia/Taipei, relative to refDate — see
// PullForwardTomorrowStart). Tasks due today or overdue are excluded — those
// already surface as "today" work via UpcomingTasks / GroupUpcomingTasks;
// this query surfaces important work that hasn't yet entered the due-date
// radar. Ordered due_date ASC NULLS LAST, priority ASC. Workspace scoped,
// read-only, evaluated fresh on every call.
//
// Hand-rolled (not sqlc) to match the sibling UpcomingTasks query style.
func (s *Store) PullForwardTasks(ctx context.Context, refDate time.Time) ([]db.Task, error) {
	tomorrowStart, err := PullForwardTomorrowStart(refDate)
	if err != nil {
		return nil, fmt.Errorf("listing pull-forward tasks: %w", err)
	}
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		branch_name, pr_url, commit_shas
		FROM tasks
		WHERE status IN ('pending','in_progress')
		  AND importance = 1
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND (due_date IS NULL OR due_date >= $2)
		ORDER BY due_date ASC NULLS LAST, priority ASC
		LIMIT $3`
	rows, err := s.dbtx.Query(ctx, q, s.workspaceID, tomorrowStart, PullForwardCap)
	if err != nil {
		return nil, fmt.Errorf("listing pull-forward tasks: %w", err)
	}
	defer rows.Close()
	var out []db.Task
	for rows.Next() {
		var t db.Task
		if err := rows.Scan(
			&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
			&t.BranchName, &t.PRUrl, &t.CommitSHAs,
		); err != nil {
			return nil, fmt.Errorf("scanning pull-forward task: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating pull-forward tasks: %w", err)
	}
	return out, nil
}

// CreateTask inserts a new task.
// Hand-rolled INSERT so that all columns (including branch_name/pr_url from
// migration 000047 and vision_item_id from migration 000050) are included
// without requiring a sqlc regen on every contributor's machine.
func (s *Store) CreateTask(ctx context.Context, p CreateTaskParams) (*db.Task, error) {
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	kind := p.Kind
	if kind == "" {
		kind = "general"
	}

	// Domain-layer gate (P6.7, sunk from the MCP-only guard in P6.6): any
	// non-empty assignee MUST resolve through the canonical actor allowlist
	// before it reaches storage, regardless of caller (MCP, HTTP, or a
	// WithTx transactional materialiser). Empty stays empty (unowned task —
	// new tasks always insert as 'pending' below, so no in_progress guard
	// applies here).
	assignee := p.Assignee
	if strings.TrimSpace(assignee) != "" {
		normalized, err := NormalizeActor(assignee)
		if err != nil {
			return nil, fmt.Errorf("creating task %q: %w", p.Title, err)
		}
		assignee = normalized
	}

	const q = `INSERT INTO tasks
		(project_id, title, description, status, priority, assignee, due_date, importance, context, kind,
		 branch_name, pr_url, commit_shas, workspace_id, vision_item_id)
		VALUES ($1, $2, $3, 'pending', $4, $5, $6, $7, $8, $9, $10, $11, '{}', $12, $13)
		RETURNING id, project_id, title, description, status, priority, assignee,
		          due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		          branch_name, pr_url, commit_shas, vision_item_id`
	rows, err := s.dbtx.Query(
		ctx, q,
		pgconv.ToUUID(p.ProjectID), p.Title, pgconv.ToText(p.Description), priority, pgconv.ToText(assignee),
		pgconv.ToTimestamptz(p.DueDate), toInt2(p.Importance), pgconv.ToText(p.Context), kind,
		pgconv.ToText(coalesceStringPtr(p.BranchName)), pgconv.ToText(coalesceStringPtr(p.PRUrl)),
		s.workspaceID, pgconv.ToUUID(p.VisionItemID),
	)
	if err != nil {
		return nil, fmt.Errorf("creating task %q: %w", p.Title, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if rErr := rows.Err(); rErr != nil {
			return nil, fmt.Errorf("iterating create task %q: %w", p.Title, rErr)
		}
		return nil, fmt.Errorf("creating task %q: no row returned", p.Title)
	}
	var t db.Task
	if err := rows.Scan(
		&t.ID, &t.ProjectID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.Assignee,
		&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
		&t.BranchName, &t.PRUrl, &t.CommitSHAs, &t.VisionItemID,
	); err != nil {
		return nil, fmt.Errorf("scanning created task %q: %w", p.Title, err)
	}
	return &t, nil
}

// coalesceStringPtr returns the dereferenced value when non-nil, else empty string.
func coalesceStringPtr(ptr *string) string {
	if ptr != nil {
		return *ptr
	}
	return ""
}

func toInt2(v *int16) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: *v, Valid: true}
}

// BatchCompleteTasksByPRMatch implements StoreIface.BatchCompleteTasksByPRMatch
// for the Postgres backend. Runs inside a single transaction so a partial
// auto-close cannot leave half the matches done. See iface.go for contract.
func (s *Store) BatchCompleteTasksByPRMatch(ctx context.Context, matches []Match) (map[uuid.UUID]bool, error) {
	if len(matches) == 0 {
		return map[uuid.UUID]bool{}, nil
	}
	beginner, ok := s.dbtx.(txBeginner)
	if !ok {
		return nil, fmt.Errorf("BatchCompleteTasksByPRMatch: dbtx does not support Begin")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("BatchCompleteTasksByPRMatch: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	qtx := s.q.WithTx(tx)
	applied := make(map[uuid.UUID]bool, len(matches))
	for _, m := range matches {
		ok, perr := s.applyOnePRMatch(ctx, qtx, m)
		if perr != nil {
			return nil, fmt.Errorf("BatchCompleteTasksByPRMatch task %s: %w", m.TaskID, perr)
		}
		if ok {
			applied[m.TaskID] = true
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("BatchCompleteTasksByPRMatch: commit: %w", err)
	}
	committed = true
	return applied, nil
}

// applyOnePRMatch performs the per-task work for BatchCompleteTasksByPRMatch:
// guarded UPDATE (skip if already completed) + activity_log INSERT. Returns
// true when the row actually transitioned, false when it was already done.
func (s *Store) applyOnePRMatch(ctx context.Context, qtx *db.Queries, m Match) (bool, error) {
	// Guarded UPDATE: only flip tasks that are STILL pending/in_progress at
	// apply time. Setting status, artifact, and pr_url in one statement keeps
	// the row consistent. status IN ('pending','in_progress') mirrors the
	// exact precondition gtd.MatchMergedPRs used to select this task as a
	// candidate during preview (see reconcile.go) — this closes a CWE-367
	// TOCTOU gap where the confirm-gate could apply a match computed up to
	// reconcileTokenTTL earlier: previously the guard was merely
	// "status != 'completed'", so if a task was cancelled between preview and
	// confirm (e.g. via set_task_status), a still-valid reconcile_token would
	// silently re-complete it, overwriting the user's cancellation with a
	// fabricated pr_url/artifact. Tightening the guard makes the UPDATE a
	// no-op (RowsAffected=0) for any task whose status drifted away from
	// pending/in_progress in the confirm window, in addition to keeping the
	// original idempotency for already-completed tasks.
	const upd = `UPDATE tasks
		SET status     = 'completed',
		    artifact   = $1,
		    pr_url     = $1,
		    updated_at = NOW()
		WHERE id = $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		  AND status IN ('pending', 'in_progress')`
	res, err := s.dbtx.Exec(ctx, upd, m.PRUrl, m.TaskID, s.workspaceID)
	if err != nil {
		return false, fmt.Errorf("update task: %w", err)
	}
	if res.RowsAffected() == 0 {
		// Already completed or wrong workspace — idempotent no-op.
		return false, nil
	}

	// audit: record what closed the task and why.
	notes := fmt.Sprintf("auto-closed by reconcile_merged_prs reason=%s pr_url=%s head_ref=%s",
		m.Reason, m.PRUrl, m.PRHeadRef)
	if m.BodyExcerpt != "" {
		notes += " body=" + m.BodyExcerpt
	}
	if _, err := qtx.CreateActivityLog(ctx, db.CreateActivityLogParams{
		Actor:       "system",
		Action:      "pr_auto_close",
		Notes:       pgconv.ToText(sanitize.Notes(notes)),
		WorkspaceID: s.workspaceID,
	}); err != nil {
		return false, fmt.Errorf("activity log: %w", err)
	}
	return true, nil
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

// BeginTask atomically sets a task to in_progress and records a
// work_session_started activity log entry.
//
// Idempotency: if the task is already in_progress, the task row is returned
// as-is without writing a duplicate activity_log row.
// Returns ErrNotFound when no task matches id inside the configured workspace
// (scoped via the Store's own workspaceID, not a caller-supplied argument —
// see AddChecklistItem for the alternative pattern where the caller does pass
// an explicit workspace scope).
func (s *Store) BeginTask(ctx context.Context, id uuid.UUID) (*db.Task, error) {
	// Idempotency: read current status before opening a transaction.
	existing, err := s.getTaskByID(ctx, id)
	if err != nil {
		return nil, err // ErrNotFound already wrapped
	}
	if existing.Status == "in_progress" {
		return existing, nil
	}

	// Domain-layer gate (P6.7): BeginTask takes no assignee argument, so the
	// only source of truth is the existing row — MCP begin_task persists a
	// caller-supplied assignee via UpdateTask BEFORE calling BeginTask (see
	// handleBeginTask) precisely so it shows up here.
	existingAssignee := ""
	if existing.Assignee.Valid {
		existingAssignee = existing.Assignee.String
	}
	if err := RequireAssigneeForInProgress(existingAssignee, TaskStatusInProgress); err != nil {
		return nil, fmt.Errorf("beginning task %s: %w", id, err)
	}

	beginner, ok := s.dbtx.(txBeginner)
	if !ok {
		return nil, fmt.Errorf("begin task %s: dbtx does not support Begin", id)
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin task %s: begin tx: %w", id, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	qtx := s.q.WithTx(tx)
	// Use BeginTaskStatus (AND status != 'in_progress' guard) to prevent
	// duplicate activity_log rows when two concurrent calls race.
	// ErrNoRows here means either not found OR already in_progress.
	task, err := qtx.BeginTaskStatus(ctx, db.BeginTaskStatusParams{
		ID:          id,
		WorkspaceID: uuid.UUID(s.workspaceID.Bytes),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Distinguish "already in_progress" (idempotent) from "not found".
			reread, re := s.getTaskByID(ctx, id)
			if re != nil {
				return nil, re // ErrNotFound
			}
			if reread.Status == "in_progress" {
				// Idempotent: commit empty tx, return existing row without logging.
				if ce := tx.Commit(ctx); ce != nil {
					return nil, fmt.Errorf("begin task %s: commit: %w", id, ce)
				}
				committed = true
				return reread, nil
			}
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("begin task %s: update status: %w", id, err)
	}

	_, err = qtx.CreateActivityLog(ctx, db.CreateActivityLogParams{
		Actor:       "system",
		Action:      "work_session_started",
		Notes:       pgconv.ToText(sanitize.Notes("task: " + task.Title)),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("begin task %s: log activity: %w", id, err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("begin task %s: commit: %w", id, err)
	}
	committed = true
	return &task, nil
}

// LogActivity records an activity entry.
func (s *Store) LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error {
	_, err := s.q.CreateActivityLog(ctx, db.CreateActivityLogParams{
		Actor:       actor,
		ProjectID:   pgconv.ToUUID(projectID),
		Action:      action,
		Notes:       pgconv.ToText(sanitize.Notes(notes)),
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
		Description: pgconv.ToText(p.Description),
		Area:        pgconv.ToText(p.Area),
		DueDate:     pgconv.ToTimestamptz(p.DueDate),
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, fmt.Errorf("creating goal %q: %w", p.Title, err)
	}
	return &row, nil
}

// UpdateTaskStatus sets the status of a task by ID.
func (s *Store) UpdateTaskStatus(ctx context.Context, id uuid.UUID, status TaskStatus) (*db.Task, error) {
	// Domain-layer gate (P6.7): UpdateTaskStatus takes no assignee argument,
	// so an existing-row read is the only way to check "does this task have
	// an owner" before allowing the in_progress transition. Skipped entirely
	// for any other target status — no extra read on the hot pending/
	// completed/cancelled paths.
	if status == TaskStatusInProgress {
		existing, err := s.getTaskByID(ctx, id)
		if err != nil {
			return nil, err // ErrNotFound already wrapped
		}
		existingAssignee := ""
		if existing.Assignee.Valid {
			existingAssignee = existing.Assignee.String
		}
		if err := RequireAssigneeForInProgress(existingAssignee, status); err != nil {
			return nil, fmt.Errorf("updating task %s status: %w", id, err)
		}
	}

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

// GetTaskByID returns a single task by UUID, scoped to the configured workspace.
// Returns ErrNotFound when no matching row exists. Satisfies StoreIface.
func (s *Store) GetTaskByID(ctx context.Context, id uuid.UUID) (*db.Task, error) {
	return s.getTaskByID(ctx, id)
}

// getTaskByID fetches a single task by ID, scoped to the configured workspace.
// Returns ErrNotFound when no matching row exists. Hand-rolled query (not sqlc)
// to avoid churning the codegen surface for a single read.
func (s *Store) getTaskByID(ctx context.Context, id uuid.UUID) (*db.Task, error) {
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		branch_name, pr_url, commit_shas
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
		&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
		&t.BranchName, &t.PRUrl, &t.CommitSHAs,
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

// resolveAssigneeForUpdate merges and validates the assignee column for an
// UpdateTask write in one pass: p.Assignee == nil preserves existing; a
// non-empty NEW value must resolve through NormalizeActor (P6.7 domain-layer
// gate) before merging in; empty string is the explicit-clear case. The
// merged result is then checked against RequireAssigneeForInProgress using
// status (already merged by the caller), rejecting a write that would leave
// the row in_progress with no resolved assignee. Extracted from UpdateTask —
// which pre-reads `existing` and merges every other field inline — to keep
// its cyclomatic complexity within the gocyclo budget.
func resolveAssigneeForUpdate(p *UpdateTaskParams, existingAssignee pgtype.Text, status string) (pgtype.Text, error) {
	assignee := existingAssignee
	if p.Assignee != nil {
		newAssignee := *p.Assignee
		if strings.TrimSpace(newAssignee) != "" {
			normalized, err := NormalizeActor(newAssignee)
			if err != nil {
				return pgtype.Text{}, err
			}
			newAssignee = normalized
		}
		assignee = pgconv.ToText(newAssignee)
	}
	mergedAssignee := ""
	if assignee.Valid {
		mergedAssignee = assignee.String
	}
	if err := RequireAssigneeForInProgress(mergedAssignee, TaskStatus(status)); err != nil {
		return pgtype.Text{}, err
	}
	return assignee, nil
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
		description = pgconv.ToText(*p.Description)
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

	var dueDate pgtype.Timestamptz
	if p.DueDate != nil {
		dueDate = pgconv.ToTimestamptz(p.DueDate)
	} else {
		dueDate = existing.DueDate
	}

	var taskContext pgtype.Text
	if p.Context != nil {
		taskContext = pgconv.ToText(*p.Context)
	} else {
		taskContext = existing.Context
	}

	var status string
	if p.Status != nil {
		status = *p.Status
	} else {
		status = existing.Status
	}

	assignee, err := resolveAssigneeForUpdate(&p, existing.Assignee, status)
	if err != nil {
		return nil, fmt.Errorf("updating task %s: %w", id, err)
	}

	var branchName pgtype.Text
	if p.BranchName != nil {
		branchName = pgconv.ToText(*p.BranchName)
	} else {
		branchName = existing.BranchName
	}

	var prURL pgtype.Text
	if p.PRUrl != nil {
		prURL = pgconv.ToText(*p.PRUrl)
	} else {
		prURL = existing.PRUrl
	}

	commitSHAs := existing.CommitSHAs
	if p.CommitSHAs != nil {
		commitSHAs = p.CommitSHAs
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
		    branch_name = $9,
		    pr_url      = $10,
		    commit_shas = $11,
		    updated_at  = NOW()
		WHERE id = $12
		  AND ($13::uuid IS NULL OR workspace_id = $13)
		RETURNING id, project_id, title, description, status, priority, assignee,
		          due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		          branch_name, pr_url, commit_shas`
	rows, err := s.dbtx.Query(
		ctx, q,
		title, description, priority, importance, assignee, dueDate, taskContext, status,
		branchName, prURL, commitSHAs,
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
		&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
		&t.BranchName, &t.PRUrl, &t.CommitSHAs,
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
		Description: pgconv.ToText(p.Description),
		Area:        pgconv.ToText(p.Area),
		Status:      string(p.Status),
		DueDate:     pgconv.ToTimestamptz(p.DueDate),
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
//
// Hand-rolled UPDATE (instead of sqlc UpdateProject) so that the repo_name
// column added in migration 000037 is handled without requiring a sqlc regen.
// RepoName semantics: nil → preserve existing DB value; non-nil → overwrite
// (empty string clears to NULL).
func (s *Store) UpdateProject(ctx context.Context, id uuid.UUID, p UpdateProjectParams) (*db.Project, error) {
	area := p.Area
	if area == "" {
		area = "projects"
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}

	var (
		rows pgx.Rows
		err  error
	)
	// Two query branches: include repo_name in the SET only when the caller
	// explicitly provides it; otherwise omit it so the existing value is kept.
	if p.RepoName != nil {
		const q = `UPDATE projects
			SET title       = $1,
			    description = $2,
			    area        = $3,
			    priority    = $4,
			    status      = $5,
			    goal_id     = $6,
			    repo_name   = $7,
			    updated_at  = NOW()
			WHERE id = $8
			  AND ($9::uuid IS NULL OR workspace_id = $9)
			RETURNING id, goal_id, name, title, description, status, area, priority,
			          created_at, updated_at, workspace_id`
		rows, err = s.dbtx.Query(
			ctx, q,
			p.Title, pgconv.ToText(p.Description), area, priority, string(p.Status), pgconv.ToUUID(p.GoalID),
			pgconv.ToText(*p.RepoName),
			id, s.workspaceID,
		)
	} else {
		const q = `UPDATE projects
			SET title       = $1,
			    description = $2,
			    area        = $3,
			    priority    = $4,
			    status      = $5,
			    goal_id     = $6,
			    updated_at  = NOW()
			WHERE id = $7
			  AND ($8::uuid IS NULL OR workspace_id = $8)
			RETURNING id, goal_id, name, title, description, status, area, priority,
			          created_at, updated_at, workspace_id`
		rows, err = s.dbtx.Query(
			ctx, q,
			p.Title, pgconv.ToText(p.Description), area, priority, string(p.Status), pgconv.ToUUID(p.GoalID),
			id, s.workspaceID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("updating project %s: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if rErr := rows.Err(); rErr != nil {
			return nil, fmt.Errorf("iterating update result for project %s: %w", id, rErr)
		}
		return nil, ErrNotFound
	}
	var row db.Project
	if err := rows.Scan(
		&row.ID, &row.GoalID, &row.Name, &row.Title, &row.Description, &row.Status, &row.Area, &row.Priority,
		&row.CreatedAt, &row.UpdatedAt, &row.WorkspaceID,
	); err != nil {
		return nil, fmt.Errorf("scanning updated project %s: %w", id, err)
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
	if err = tx.QueryRow(
		ctx,
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
	if _, err = tx.Exec(
		ctx,
		`DELETE FROM work_session_tasks WHERE task_id = $1`, id,
	); err != nil {
		return fmt.Errorf("deleting task %s: cleanup work_session_tasks: %w", id, err)
	}

	// 2. NULL out work_sessions.current_task_id (was ON DELETE SET NULL).
	if _, err = tx.Exec(
		ctx,
		`UPDATE work_sessions
		   SET current_task_id = NULL,
		       updated_at      = NOW()
		 WHERE current_task_id = $1`, id,
	); err != nil {
		return fmt.Errorf("deleting task %s: nullify work_sessions.current_task_id: %w", id, err)
	}

	// 3a. Remove completion_candidates rows referencing this task.
	if _, err = tx.Exec(
		ctx,
		`DELETE FROM completion_candidates WHERE task_id = $1`, id,
	); err != nil {
		return fmt.Errorf("deleting task %s: cleanup completion_candidates: %w", id, err)
	}

	// 3b. Reset vision_items that were promoted from this task.
	if _, err = tx.Exec(
		ctx,
		`UPDATE vision_items
		    SET promoted_task_id = NULL,
		        status           = 'open'
		  WHERE promoted_task_id = $1`, id,
	); err != nil {
		return fmt.Errorf("deleting task %s: reset vision_items.promoted_task_id: %w", id, err)
	}

	// 5. Delete the task itself, scoped to the configured workspace.
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

// LatestActivityAt returns the created_at of the most-recent activity_log row,
// workspace-scoped, or nil if the table is empty.
func (s *Store) LatestActivityAt(ctx context.Context) (*time.Time, error) {
	const q = `SELECT created_at FROM activity_log
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC LIMIT 1`
	var ts pgtype.Timestamptz
	err := s.dbtx.QueryRow(ctx, q, s.workspaceID).Scan(&ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // sentinel: empty table is not an error; callers render null timestamp
	}
	if err != nil {
		return nil, fmt.Errorf("LatestActivityAt: %w", err)
	}
	if !ts.Valid {
		return nil, nil //nolint:nilnil // sentinel: NULL timestamp in DB is not an error
	}
	t := ts.Time.UTC()
	return &t, nil
}

// automationActions is the set of action strings considered "automation"
// for ListRecentAutomation. Filtering is done Go-side for SQLite compat.
var automationActions = map[string]bool{
	"task:begin":               true,
	"task:completed":           true,
	"task:updated":             true,
	"task:added":               true,
	"decision:logged":          true,
	"plan:confirmed":           true,
	"session:handoff":          true,
	"worksession:started":      true,
	"worksession:finished":     true,
	"worksession:checkpointed": true,
	"dashboard:reconciled":     true,
}

// ListRecentAutomation returns recent automation activity_log rows, filtered
// Go-side by automationActions, workspace-scoped.
// Query: WHERE workspace_id=? AND created_at >= NOW()-7d ORDER BY created_at DESC LIMIT 200,
// then filter in Go, then slice to limit.
func (s *Store) ListRecentAutomation(ctx context.Context, limit int32) ([]db.ActivityLog, error) {
	since := time.Now().UTC().Add(-7 * 24 * time.Hour)
	const q = `SELECT id, actor, project_id, action, notes, created_at, workspace_id
		FROM activity_log
		WHERE created_at >= $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY created_at DESC
		LIMIT 200`
	rows, err := s.dbtx.Query(ctx, q, since, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("ListRecentAutomation: %w", err)
	}
	defer rows.Close()
	var out []db.ActivityLog
	for rows.Next() {
		var a db.ActivityLog
		if err := rows.Scan(
			&a.ID, &a.Actor, &a.ProjectID, &a.Action, &a.Notes, &a.CreatedAt, &a.WorkspaceID,
		); err != nil {
			return nil, fmt.Errorf("ListRecentAutomation scan: %w", err)
		}
		if automationActions[a.Action] {
			out = append(out, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListRecentAutomation iter: %w", err)
	}
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

// LatestActionAt returns the created_at of the most-recent activity_log row
// matching the given action, workspace-scoped, or nil if none found.
func (s *Store) LatestActionAt(ctx context.Context, action string) (*time.Time, error) {
	const q = `SELECT created_at FROM activity_log
		WHERE action = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY created_at DESC LIMIT 1`
	var ts pgtype.Timestamptz
	err := s.dbtx.QueryRow(ctx, q, action, s.workspaceID).Scan(&ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // sentinel: no matching action row is not an error; callers render null timestamp
	}
	if err != nil {
		return nil, fmt.Errorf("LatestActionAt: %w", err)
	}
	if !ts.Valid {
		return nil, nil //nolint:nilnil // sentinel: NULL timestamp in DB is not an error
	}
	t := ts.Time.UTC()
	return &t, nil
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

// PruneOlderThan hard-deletes activity_log rows created before cutoff.
// Global cleanup (no workspace filter) — activity_log is an observability
// table, not user-authored data, and the retention job runs once for the
// whole deployment regardless of how many workspaces exist. cutoff is
// computed server-side by the caller (scheduler) and passed as a
// parameterised argument — never built from user input.
func (s *Store) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM activity_log WHERE created_at < $1`
	tag, err := s.dbtx.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning activity_log: %w", err)
	}
	return tag.RowsAffected(), nil
}

// TopPendingTask returns the single highest-priority pending task in the
// configured workspace, ordered by priority ASC NULLS LAST, importance ASC
// NULLS LAST, created_at ASC. Returns nil, nil when no pending task exists.
func (s *Store) TopPendingTask(ctx context.Context) (*db.Task, error) {
	const q = `SELECT id, project_id, title, description, status, priority, assignee,
		due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		branch_name, pr_url, commit_shas
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
		&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
		&t.BranchName, &t.PRUrl, &t.CommitSHAs,
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
		due_date, artifact, created_at, updated_at, workspace_id, importance, context, checklist, kind,
		branch_name, pr_url, commit_shas
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
			&t.DueDate, &t.Artifact, &t.CreatedAt, &t.UpdatedAt, &t.WorkspaceID, &t.Importance, &t.Context, &t.Checklist, &t.Kind,
			&t.BranchName, &t.PRUrl, &t.CommitSHAs,
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

// WeeklyProgress returns completed task count this week and total week-relevant task count.
// total = tasks completed this week + pending/in_progress due this week or created this week.
func (s *Store) WeeklyProgress(ctx context.Context) (completed, total int64, err error) {
	completed, err = s.q.CountCompletedTasksThisWeek(ctx, s.workspaceID)
	if err != nil {
		return 0, 0, fmt.Errorf("counting completed tasks: %w", err)
	}
	total, err = s.q.CountWeeklyRelevantTasks(ctx, s.workspaceID)
	if err != nil {
		return 0, 0, fmt.Errorf("counting week-relevant tasks: %w", err)
	}
	return completed, total, nil
}

// loadChecklistTx reads the checklist column for taskID scoped to workspaceID
// inside an existing transaction (using FOR UPDATE to acquire a row lock).
// Returns ErrNotFound when the task row does not exist in the workspace.
func loadChecklistTx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, wsFilter pgtype.UUID) ([]ChecklistItem, error) {
	const q = `SELECT checklist FROM tasks WHERE id = $1 AND ($2::uuid IS NULL OR workspace_id = $2) LIMIT 1 FOR UPDATE`
	rows, err := tx.Query(ctx, q, taskID, wsFilter)
	if err != nil {
		return nil, fmt.Errorf("loading checklist for task %s: %w", taskID, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if rErr := rows.Err(); rErr != nil {
			return nil, fmt.Errorf("iterating checklist for task %s: %w", taskID, rErr)
		}
		return nil, ErrNotFound
	}
	var raw []byte
	if err := rows.Scan(&raw); err != nil {
		return nil, fmt.Errorf("scanning checklist for task %s: %w", taskID, err)
	}
	var items []ChecklistItem
	if len(raw) > 0 {
		if err := jsonUnmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("parsing checklist for task %s: %w", taskID, err)
		}
	}
	if items == nil {
		items = []ChecklistItem{}
	}
	return items, nil
}

// saveChecklistTx writes the serialised checklist back to the task row inside
// an existing transaction.
func saveChecklistTx(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, wsFilter pgtype.UUID, items []ChecklistItem) error {
	data, err := jsonMarshal(items)
	if err != nil {
		return fmt.Errorf("marshalling checklist for task %s: %w", taskID, err)
	}
	const q = `UPDATE tasks SET checklist = $1, updated_at = NOW() WHERE id = $2 AND ($3::uuid IS NULL OR workspace_id = $3)`
	if _, err := tx.Exec(ctx, q, data, taskID, wsFilter); err != nil {
		return fmt.Errorf("saving checklist for task %s: %w", taskID, err)
	}
	return nil
}

// AddChecklistItem appends a new ChecklistItem (with a server-generated ID) to
// the task's checklist and returns the full updated slice.
// Returns ErrNotFound when no task matches taskID + workspaceID.
//
// The load+save pair runs inside a BEGIN/FOR UPDATE/COMMIT transaction to
// prevent lost updates when concurrent callers modify the same task's checklist.
func (s *Store) AddChecklistItem(
	ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID, item ChecklistItem,
) ([]ChecklistItem, error) {
	beginner, ok := s.dbtx.(txBeginner)
	if !ok {
		return nil, fmt.Errorf("AddChecklistItem task %s: dbtx does not support Begin", taskID)
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("AddChecklistItem task %s: begin tx: %w", taskID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	wsFilter := pgconv.ToUUID(&workspaceID)
	items, err := loadChecklistTx(ctx, tx, taskID, wsFilter)
	if err != nil {
		return nil, err
	}
	items = append(items, item)
	if err := saveChecklistTx(ctx, tx, taskID, wsFilter, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("AddChecklistItem task %s: commit: %w", taskID, err)
	}
	committed = true
	return items, nil
}

// ApplyChecklistItemPatch patches a single item in items identified by itemID
// using the fields set in update. It mutates items in place and returns
// ErrNotFound if itemID is not present. Extracted to keep UpdateChecklistItem
// below the gocyclo limit, and exported for use by the SQLite store.
func ApplyChecklistItemPatch(items []ChecklistItem, itemID uuid.UUID, update UpdateChecklistItemParams) ([]ChecklistItem, error) {
	now := time.Now().UTC()
	for i := range items {
		if items[i].ID != itemID {
			continue
		}
		if update.Title != nil {
			items[i].Title = *update.Title
		}
		if update.Notes != nil {
			items[i].Notes = *update.Notes
		}
		if update.EvidenceURL != nil {
			items[i].EvidenceURL = *update.EvidenceURL
		}
		if update.Done != nil {
			items[i].Done = *update.Done
			if *update.Done && items[i].CompletedAt == nil {
				items[i].CompletedAt = &now
			} else if !*update.Done {
				items[i].CompletedAt = nil
			}
		}
		return items, nil
	}
	return nil, ErrNotFound
}

// UpdateChecklistItem applies a partial patch to the checklist item identified
// by itemID inside the given task. Returns the full updated checklist.
// Returns ErrNotFound when task or item is not found.
//
// The load+save pair runs inside a BEGIN/FOR UPDATE/COMMIT transaction to
// prevent lost updates when concurrent callers modify the same task's checklist.
func (s *Store) UpdateChecklistItem(
	ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID,
	itemID uuid.UUID, update UpdateChecklistItemParams,
) ([]ChecklistItem, error) {
	beginner, ok := s.dbtx.(txBeginner)
	if !ok {
		return nil, fmt.Errorf("UpdateChecklistItem task %s: dbtx does not support Begin", taskID)
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("UpdateChecklistItem task %s: begin tx: %w", taskID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	wsFilter := pgconv.ToUUID(&workspaceID)
	items, err := loadChecklistTx(ctx, tx, taskID, wsFilter)
	if err != nil {
		return nil, err
	}
	items, err = ApplyChecklistItemPatch(items, itemID, update)
	if err != nil {
		return nil, err
	}
	if err := saveChecklistTx(ctx, tx, taskID, wsFilter, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("UpdateChecklistItem task %s: commit: %w", taskID, err)
	}
	committed = true
	return items, nil
}

// DeleteChecklistItem removes the item identified by itemID from the task's
// checklist. Returns ErrNotFound when task or item is not found.
//
// The load+save pair runs inside a BEGIN/FOR UPDATE/COMMIT transaction to
// prevent lost updates when concurrent callers modify the same task's checklist.
func (s *Store) DeleteChecklistItem(ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID, itemID uuid.UUID) error {
	beginner, ok := s.dbtx.(txBeginner)
	if !ok {
		return fmt.Errorf("DeleteChecklistItem task %s: dbtx does not support Begin", taskID)
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("DeleteChecklistItem task %s: begin tx: %w", taskID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	wsFilter := pgconv.ToUUID(&workspaceID)
	items, err := loadChecklistTx(ctx, tx, taskID, wsFilter)
	if err != nil {
		return err
	}
	next := items[:0]
	found := false
	for _, it := range items {
		if it.ID == itemID {
			found = true
			continue
		}
		next = append(next, it)
	}
	if !found {
		return ErrNotFound
	}
	if err := saveChecklistTx(ctx, tx, taskID, wsFilter, next); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("DeleteChecklistItem task %s: commit: %w", taskID, err)
	}
	committed = true
	return nil
}
