package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// defaultProjectArea is the fallback area applied when an empty area is
// provided to CreateProject, CreateProjectTx, or UpdateProject.
const defaultProjectArea = "projects"

// GTDStore is the SQLite-backed implementation of gtd.StoreIface.
type GTDStore struct {
	db *DB
}

// NewGTDStore wraps an open DB into a GTDStore.
func NewGTDStore(d *DB) *GTDStore {
	return &GTDStore{db: d}
}

// WorkspaceID returns the configured workspace UUID for parity with
// gtd.Store.WorkspaceID(). Used by MCP system_health to surface the active
// scope. Empty configured workspace → zero pgtype.UUID (Valid=false).
func (s *GTDStore) WorkspaceID() pgtype.UUID {
	return pgtypeUUID(s.db.workspaceID)
}

// Compile-time guarantee against drift from gtd.StoreIface.
var _ gtd.StoreIface = (*GTDStore)(nil)

// ----- helpers -----

const tasksSelectCols = `id, workspace_id, project_id, title, description, status,
	priority, importance, context, assignee, due_date, artifact,
	created_at, updated_at`

// scanTask reads a row in tasksSelectCols order into db.Task, converting
// SQLite TEXT columns to the pgtype values the Postgres stores already use.
func scanTask(scan func(...any) error) (db.Task, error) {
	var (
		t                                                                      db.Task
		idStr                                                                  string
		workspaceIDNS, projectIDNS                                             sql.NullString
		descNS, contextNS, assigneeNS, dueDateNS, artifactNS, createdNS, updNS sql.NullString
		statusStr                                                              string
		importanceNI                                                           sql.NullInt32
	)

	err := scan(&idStr, &workspaceIDNS, &projectIDNS, &t.Title, &descNS, &statusStr,
		&t.Priority, &importanceNI, &contextNS, &assigneeNS, &dueDateNS, &artifactNS,
		&createdNS, &updNS)
	if err != nil {
		return db.Task{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		t.ID = id
	}
	t.WorkspaceID = pgtypeUUID(nsString(workspaceIDNS))
	t.ProjectID = pgtypeUUID(nsString(projectIDNS))
	t.Description = pgtypeText(descNS.String, descNS.Valid)
	t.Status = statusStr
	if importanceNI.Valid {
		// Schema CHECK constrains importance to 1..3, so the int32 → int16 cast
		// cannot overflow.
		imp := int16(importanceNI.Int32) //nolint:gosec // G115: schema CHECK (importance BETWEEN 1 AND 3) guarantees int32 fits int16
		t.Importance = pgtype.Int2{Int16: imp, Valid: true}
	}
	t.Context = pgtypeText(contextNS.String, contextNS.Valid)
	t.Assignee = pgtypeText(assigneeNS.String, assigneeNS.Valid)
	t.DueDate = parseTimestamptz(dueDateNS)
	t.Artifact = pgtypeText(artifactNS.String, artifactNS.Valid)
	t.CreatedAt = parseTimestamptz(createdNS)
	t.UpdatedAt = parseTimestamptz(updNS)
	return t, nil
}

// nsString returns the underlying string only when Valid; otherwise empty.
func nsString(ns sql.NullString) string {
	if !ns.Valid {
		return ""
	}
	return ns.String
}

const projectsSelectCols = `id, workspace_id, goal_id, name, title, description, status, area, priority, created_at, updated_at`

func scanProject(scan func(...any) error) (db.Project, error) {
	var (
		p                         db.Project
		idStr, statusStr, areaStr string
		workspaceIDNS, goalIDNS   sql.NullString
		descNS, createdNS, updNS  sql.NullString
	)
	err := scan(&idStr, &workspaceIDNS, &goalIDNS, &p.Name, &p.Title, &descNS,
		&statusStr, &areaStr, &p.Priority, &createdNS, &updNS)
	if err != nil {
		return db.Project{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		p.ID = id
	}
	p.WorkspaceID = pgtypeUUID(nsString(workspaceIDNS))
	p.GoalID = pgtypeUUID(nsString(goalIDNS))
	p.Description = pgtypeText(descNS.String, descNS.Valid)
	p.Status = statusStr
	p.Area = areaStr
	p.CreatedAt = parseTimestamptz(createdNS)
	p.UpdatedAt = parseTimestamptz(updNS)
	return p, nil
}

const goalsSelectCols = `id, workspace_id, title, description, status, area, due_date, created_at, updated_at`

func scanGoal(scan func(...any) error) (db.Goal, error) {
	var (
		g                                       db.Goal
		idStr, statusStr                        string
		workspaceIDNS                           sql.NullString
		descNS, areaNS, dueNS, createdNS, updNS sql.NullString
	)
	err := scan(&idStr, &workspaceIDNS, &g.Title, &descNS, &statusStr,
		&areaNS, &dueNS, &createdNS, &updNS)
	if err != nil {
		return db.Goal{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		g.ID = id
	}
	g.WorkspaceID = pgtypeUUID(nsString(workspaceIDNS))
	g.Description = pgtypeText(descNS.String, descNS.Valid)
	g.Status = statusStr
	g.Area = pgtypeText(areaNS.String, areaNS.Valid)
	g.DueDate = parseTimestamptz(dueNS)
	g.CreatedAt = parseTimestamptz(createdNS)
	g.UpdatedAt = parseTimestamptz(updNS)
	return g, nil
}

// parseTimestamptz parses an RFC3339 timestamp from a SQLite TEXT column.
// Empty / NULL → invalid (zero) pgtype.Timestamptz.
func parseTimestamptz(ns sql.NullString) pgtype.Timestamptz {
	if !ns.Valid || ns.String == "" {
		return pgtype.Timestamptz{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, ns.String); err == nil {
			return pgtype.Timestamptz{Time: t, Valid: true}
		}
	}
	return pgtype.Timestamptz{}
}

// nowRFC3339 returns the current UTC time in RFC3339 format with exactly
// 3 millisecond digits (.000), matching SQLite's strftime('%Y-%m-%dT%H:%M:%fZ','now')
// output which produces 3 fractional digits. Using time.RFC3339Nano would emit
// up to 9 digits, causing length inconsistencies when comparing timestamps stored
// by the app vs the DB default expression.
func nowRFC3339() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00") }

// nullStringFromText collapses pgtype-style "" to NULL for inserts.
func nullStringIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullStringFromUUID returns NULL for nil pointer, otherwise the canonical UUID string.
func nullStringFromUUID(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

// ----- StoreIface methods -----

// ListActiveProjects returns all active projects in the configured workspace.
func (s *GTDStore) ListActiveProjects(ctx context.Context) ([]db.Project, error) {
	const q = `SELECT ` + projectsSelectCols + ` FROM projects
		WHERE status = 'active'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY priority ASC, updated_at DESC`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("ListActiveProjects", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Project
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, errWrap("ListActiveProjects scan", err)
		}
		out = append(out, p)
	}
	return out, errWrap("ListActiveProjects iter", rows.Err())
}

// ProjectByName looks up a single project by unique name within the workspace.
func (s *GTDStore) ProjectByName(ctx context.Context, name string) (*db.Project, error) {
	const q = `SELECT ` + projectsSelectCols + ` FROM projects
		WHERE name = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, name, s.db.workspaceArg())
	p, err := scanProject(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gtd.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("ProjectByName", err)
	}
	return &p, nil
}

// ProjectsByRepoName returns every project whose `repo_name` column matches
// the given repo, scoped to the configured workspace. Empty repoName → empty
// slice (fast-path; avoids a wildcard scan). Empty result is not an error.
func (s *GTDStore) ProjectsByRepoName(ctx context.Context, repoName string) ([]db.Project, error) {
	if repoName == "" {
		return nil, nil
	}
	const q = `SELECT ` + projectsSelectCols + ` FROM projects
		WHERE repo_name = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY priority ASC, created_at ASC`
	rows, err := s.db.conn.QueryContext(ctx, q, repoName, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("ProjectsByRepoName", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Project
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, errWrap("ProjectsByRepoName scan", err)
		}
		out = append(out, p)
	}
	return out, errWrap("ProjectsByRepoName iter", rows.Err())
}

// CreateProject inserts a new project, generating a UUID and returning the row.
func (s *GTDStore) CreateProject(ctx context.Context, p gtd.CreateProjectParams) (*db.Project, error) {
	id := uuid.New()
	area := p.Area
	if area == "" {
		area = defaultProjectArea
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	const q = `INSERT INTO projects (id, workspace_id, goal_id, name, title, description, area, priority, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?9)`
	now := nowRFC3339()
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.GoalID),
		p.Name, p.Title, nullStringIfEmpty(p.Description), area, priority, now)
	if err != nil {
		// SQLite UNIQUE failure surfaces as constraint code 2067 / SQLITE_CONSTRAINT_UNIQUE.
		// Match by message rather than introducing a driver-specific dependency.
		if isUniqueViolation(err) {
			return nil, gtd.ErrConflict
		}
		return nil, errWrap("CreateProject", err)
	}
	// Re-read so callers see all server defaults populated.
	return s.projectByID(ctx, id)
}

// CreateGoalTx inserts a new goal within the provided *sql.Tx.
// It is the transactional counterpart of CreateGoal and is used by the
// confirm_proposal accept path for atomic cross-store writes.
func (s *GTDStore) CreateGoalTx(ctx context.Context, tx *sql.Tx, p gtd.CreateGoalParams) (uuid.UUID, error) {
	id := uuid.New()
	var dueVal any
	if p.DueDate != nil {
		dueVal = p.DueDate.UTC().Format(time.RFC3339Nano)
	}
	const q = `INSERT INTO goals (id, workspace_id, title, description, area, due_date, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?7)`
	now := nowRFC3339()
	_, err := tx.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), p.Title,
		nullStringIfEmpty(p.Description), nullStringIfEmpty(p.Area), dueVal, now)
	if err != nil {
		return uuid.UUID{}, errWrap("CreateGoalTx", err)
	}
	return id, nil
}

// CreateProjectTx inserts a new project within the provided *sql.Tx.
// It is the transactional counterpart of CreateProject and is used by the
// confirm_proposal accept path for atomic cross-store writes.
func (s *GTDStore) CreateProjectTx(ctx context.Context, tx *sql.Tx, p gtd.CreateProjectParams) (uuid.UUID, error) {
	id := uuid.New()
	area := p.Area
	if area == "" {
		area = defaultProjectArea
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	const q = `INSERT INTO projects (id, workspace_id, goal_id, name, title, description, area, priority, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?9)`
	now := nowRFC3339()
	_, err := tx.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.GoalID),
		p.Name, p.Title, nullStringIfEmpty(p.Description), area, priority, now)
	if err != nil {
		if isUniqueViolation(err) {
			return uuid.UUID{}, gtd.ErrConflict
		}
		return uuid.UUID{}, errWrap("CreateProjectTx", err)
	}
	return id, nil
}

// GetProjectByID returns a single project by UUID, regardless of status.
func (s *GTDStore) GetProjectByID(ctx context.Context, id uuid.UUID) (*db.Project, error) {
	return s.projectByID(ctx, id)
}

func (s *GTDStore) projectByID(ctx context.Context, id uuid.UUID) (*db.Project, error) {
	const q = `SELECT ` + projectsSelectCols + ` FROM projects
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg())
	p, err := scanProject(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gtd.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("projectByID", err)
	}
	return &p, nil
}

// scanTaskRows drains a *sql.Rows result (already opened) into a []db.Task
// slice and closes the rows. The caller-supplied label is used in error
// messages for traceability. Extracted to eliminate structural duplication
// between the date-range query methods (dupl linter).
func (s *GTDStore) scanTaskRows(rows *sql.Rows, label string) ([]db.Task, error) {
	defer func() { _ = rows.Close() }()
	var out []db.Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, errWrap(label+" scan", err)
		}
		out = append(out, t)
	}
	return out, errWrap(label+" iter", rows.Err())
}

// TasksByDueDateRange returns pending / in_progress tasks whose due_date
// falls inside [from, to] (inclusive on both ends), scoped to the
// configured workspace. The status filter intentionally excludes
// 'completed' so the calendar planning view shows only work that still
// needs to happen.
//
// SQLite stores due_date as RFC3339 TEXT (see CreateTask), so range filters
// rely on lexicographic comparison — RFC3339 sorts identically to chronologic
// order at the same UTC offset, which CreateTask enforces (.UTC().Format(...)).
func (s *GTDStore) TasksByDueDateRange(ctx context.Context, from, to time.Time) ([]db.Task, error) {
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks
		WHERE status IN ('pending','in_progress')
		  AND due_date IS NOT NULL
		  AND due_date >= ?1
		  AND due_date <= ?2
		  AND (?3 IS NULL OR workspace_id = ?3)
		ORDER BY due_date ASC, created_at ASC`
	fromStr := from.UTC().Format(time.RFC3339Nano)
	toStr := to.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.conn.QueryContext(ctx, q, fromStr, toStr, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("TasksByDueDateRange", err)
	}
	return s.scanTaskRows(rows, "TasksByDueDateRange")
}

// TasksForTimeline returns all tasks (any status) where created_at OR
// (status='completed' AND updated_at) falls inside [from, to] (inclusive),
// scoped to the configured workspace. Mirrors the Postgres-side
// gtd.Store.TasksForTimeline; both back the timeline aggregator's
// historical task_created / task_completed event query.
//
// SQLite stores timestamps as RFC3339 TEXT, so range filters rely on
// lexicographic comparison — RFC3339 sorts identically to chronologic order
// at the same UTC offset, which CreateTask enforces (.UTC().Format(...)).
func (s *GTDStore) TasksForTimeline(ctx context.Context, from, to time.Time) ([]db.Task, error) {
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks
		WHERE (
		    (created_at >= ?1 AND created_at <= ?2)
		    OR (status = 'completed' AND updated_at >= ?1 AND updated_at <= ?2)
		)
		AND (?3 IS NULL OR workspace_id = ?3)
		ORDER BY COALESCE(updated_at, created_at) DESC
		LIMIT 10000`
	fromStr := from.UTC().Format(time.RFC3339Nano)
	toStr := to.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.conn.QueryContext(ctx, q, fromStr, toStr, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("TasksForTimeline", err)
	}
	return s.scanTaskRows(rows, "TasksForTimeline")
}

// UpcomingTasks returns pending/in_progress tasks relevant to the upcoming
// window rooted at refDate. The window includes:
//   - tasks with a due_date <= windowEnd (refDate + days, end-of-day UTC)
//   - tasks with importance=1 and no due_date (unscheduled_important bucket)
//
// SQLite stores timestamps as RFC3339 TEXT; lexicographic comparison works
// because CreateTask enforces .UTC().Format(time.RFC3339Nano) on due_date.
func (s *GTDStore) UpcomingTasks(ctx context.Context, refDate time.Time, days, limit int) ([]db.Task, error) {
	windowEnd := refDate.UTC().AddDate(0, 0, days).Truncate(24 * time.Hour).Add(24*time.Hour - time.Nanosecond)
	windowEndStr := windowEnd.UTC().Format(time.RFC3339Nano)
	fetchLimit := limit * 2
	if fetchLimit < limit {
		fetchLimit = limit
	}
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks
		WHERE status IN ('pending','in_progress')
		  AND (?1 IS NULL OR workspace_id = ?1)
		  AND (
		      (due_date IS NOT NULL AND due_date <= ?2)
		      OR (due_date IS NULL AND importance = 1)
		  )
		ORDER BY due_date ASC NULLS LAST, priority ASC, created_at ASC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), windowEndStr, fetchLimit)
	if err != nil {
		return nil, errWrap("UpcomingTasks", err)
	}
	return s.scanTaskRows(rows, "UpcomingTasks")
}

// Tasks returns pending/in-progress tasks, optionally filtered by projectID.
func (s *GTDStore) Tasks(ctx context.Context, projectID *uuid.UUID) ([]db.Task, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if projectID != nil {
		const q = `SELECT ` + tasksSelectCols + ` FROM tasks
			WHERE project_id = ?1
			  AND status IN ('pending','in_progress')
			  AND (?2 IS NULL OR workspace_id = ?2)
			ORDER BY priority ASC, created_at ASC`
		rows, err = s.db.conn.QueryContext(ctx, q, projectID.String(), s.db.workspaceArg())
	} else {
		const q = `SELECT ` + tasksSelectCols + ` FROM tasks
			WHERE status IN ('pending','in_progress')
			  AND (?1 IS NULL OR workspace_id = ?1)
			ORDER BY priority ASC, created_at ASC`
		rows, err = s.db.conn.QueryContext(ctx, q, s.db.workspaceArg())
	}
	if err != nil {
		return nil, errWrap("Tasks", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, errWrap("Tasks scan", err)
		}
		out = append(out, t)
	}
	return out, errWrap("Tasks iter", rows.Err())
}

// TasksByProjectAllStatuses returns every task in the project regardless of
// status, ordered by COALESCE(updated_at, created_at) DESC. Mirrors the
// Postgres-side gtd.Store.TasksByProjectAllStatuses; both back the
// `?status=all` variant of the project-detail tasks endpoint.
func (s *GTDStore) TasksByProjectAllStatuses(ctx context.Context, projectID uuid.UUID) ([]db.Task, error) {
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks
		WHERE project_id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY COALESCE(updated_at, created_at) DESC`
	rows, err := s.db.conn.QueryContext(ctx, q, projectID.String(), s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("TasksByProjectAllStatuses", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, errWrap("TasksByProjectAllStatuses scan", err)
		}
		out = append(out, t)
	}
	return out, errWrap("TasksByProjectAllStatuses iter", rows.Err())
}

// CreateTask inserts a new task with all Phase A/B fields supported.
func (s *GTDStore) CreateTask(ctx context.Context, p gtd.CreateTaskParams) (*db.Task, error) {
	id := uuid.New()
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	var importance any
	if p.Importance != nil {
		importance = int(*p.Importance)
	}
	var dueVal any
	if p.DueDate != nil {
		dueVal = p.DueDate.UTC().Format(time.RFC3339Nano)
	}
	const q = `INSERT INTO tasks
		(id, workspace_id, project_id, title, description, priority,
		 importance, context, assignee, due_date, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?11)`
	now := nowRFC3339()
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.ProjectID),
		p.Title, nullStringIfEmpty(p.Description), priority, importance,
		nullStringIfEmpty(p.Context), nullStringIfEmpty(p.Assignee), dueVal, now)
	if err != nil {
		return nil, errWrap("CreateTask", err)
	}
	return s.taskByID(ctx, id)
}

func (s *GTDStore) taskByID(ctx context.Context, id uuid.UUID) (*db.Task, error) {
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks WHERE id = ?1 LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	t, err := scanTask(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gtd.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("taskByID", err)
	}
	return &t, nil
}

// CompleteTask marks a task completed and records the optional artifact URL.
func (s *GTDStore) CompleteTask(ctx context.Context, id uuid.UUID, artifact *string) (*db.Task, error) {
	const q = `UPDATE tasks
		SET status = 'completed', artifact = ?2, updated_at = ?3
		WHERE id = ?1
		  AND (?4 IS NULL OR workspace_id = ?4)`
	now := nowRFC3339()
	var artVal any
	if artifact != nil {
		artVal = *artifact
	}
	res, err := s.db.conn.ExecContext(ctx, q, id.String(), artVal, now, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("CompleteTask", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, gtd.ErrNotFound
	}
	return s.taskByID(ctx, id)
}

// ListActivityLogsSince returns activity_log rows created on or after since,
// scoped to the configured workspace. Results are ordered created_at ASC.
func (s *GTDStore) ListActivityLogsSince(ctx context.Context, since time.Time, maxRows int32) ([]db.ActivityLog, error) {
	const q = `SELECT id, workspace_id, actor, project_id, action, notes, created_at
		FROM activity_log
		WHERE created_at >= ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at ASC
		LIMIT ?3`
	sinceStr := since.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.conn.QueryContext(ctx, q, sinceStr, s.db.workspaceArg(), maxRows)
	if err != nil {
		return nil, errWrap("ListActivityLogsSince", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.ActivityLog
	for rows.Next() {
		var (
			a                      db.ActivityLog
			idStr                  string
			workspaceNS, projectNS sql.NullString
			notesNS, createdNS     sql.NullString
		)
		if err := rows.Scan(&idStr, &workspaceNS, &a.Actor, &projectNS, &a.Action, &notesNS, &createdNS); err != nil {
			return nil, errWrap("ListActivityLogsSince scan", err)
		}
		if id, err := uuid.Parse(idStr); err == nil {
			a.ID = id
		}
		a.WorkspaceID = pgtypeUUID(nsString(workspaceNS))
		a.ProjectID = pgtypeUUID(nsString(projectNS))
		a.Notes = pgtypeText(notesNS.String, notesNS.Valid)
		a.CreatedAt = parseTimestamptz(createdNS)
		out = append(out, a)
	}
	return out, errWrap("ListActivityLogsSince iter", rows.Err())
}

// LogActivity records an activity log entry. project may be nil.
func (s *GTDStore) LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error {
	const q = `INSERT INTO activity_log (id, workspace_id, actor, project_id, action, notes)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6)`
	_, err := s.db.conn.ExecContext(ctx, q,
		uuid.New().String(), s.db.workspaceArg(), actor,
		nullStringFromUUID(projectID), action, nullStringIfEmpty(notes))
	if err != nil {
		return errWrap("LogActivity", err)
	}
	return nil
}

// ActiveGoals returns all active goals ordered by due_date ascending NULLS last.
func (s *GTDStore) ActiveGoals(ctx context.Context) ([]db.Goal, error) {
	// SQLite: NULLS LAST is supported since 3.30 (2019-10). modernc.org/sqlite
	// ships modern SQLite, so the syntax is safe.
	const q = `SELECT ` + goalsSelectCols + ` FROM goals
		WHERE status = 'active'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY due_date ASC NULLS LAST`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("ActiveGoals", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Goal
	for rows.Next() {
		g, err := scanGoal(rows.Scan)
		if err != nil {
			return nil, errWrap("ActiveGoals scan", err)
		}
		out = append(out, g)
	}
	return out, errWrap("ActiveGoals iter", rows.Err())
}

// CreateGoal inserts a new goal.
func (s *GTDStore) CreateGoal(ctx context.Context, p gtd.CreateGoalParams) (*db.Goal, error) {
	id := uuid.New()
	var dueVal any
	if p.DueDate != nil {
		dueVal = p.DueDate.UTC().Format(time.RFC3339Nano)
	}
	const q = `INSERT INTO goals (id, workspace_id, title, description, area, due_date, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?7)`
	now := nowRFC3339()
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), p.Title,
		nullStringIfEmpty(p.Description), nullStringIfEmpty(p.Area), dueVal, now)
	if err != nil {
		return nil, errWrap("CreateGoal", err)
	}
	return s.goalByID(ctx, id)
}

func (s *GTDStore) goalByID(ctx context.Context, id uuid.UUID) (*db.Goal, error) {
	const q = `SELECT ` + goalsSelectCols + ` FROM goals WHERE id = ?1 LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	g, err := scanGoal(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gtd.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("goalByID", err)
	}
	return &g, nil
}

// UpdateTaskStatus sets the status of a task.
func (s *GTDStore) UpdateTaskStatus(ctx context.Context, id uuid.UUID, status gtd.TaskStatus) (*db.Task, error) {
	const q = `UPDATE tasks
		SET status = ?2, updated_at = ?3
		WHERE id = ?1
		  AND (?4 IS NULL OR workspace_id = ?4)`
	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q, id.String(), string(status), now, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("UpdateTaskStatus", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, gtd.ErrNotFound
	}
	return s.taskByID(ctx, id)
}

// mergedTaskFields holds the resolved column values for an UpdateTask write,
// computed by mergeTaskFields from the existing row and the patch params.
type mergedTaskFields struct {
	title      string
	desc       any
	priority   int32
	importance any
	assignee   any
	dueDate    any
	taskCtx    any
	status     string
}

// mergeTaskFields merges non-nil patch params over the existing task row values,
// producing a complete set of column values ready for an UPDATE statement.
func mergeTaskFields(existing *db.Task, p gtd.UpdateTaskParams) mergedTaskFields {
	m := mergedTaskFields{
		title:    existing.Title,
		priority: existing.Priority,
		status:   existing.Status,
	}
	if p.Title != nil {
		m.title = *p.Title
	}
	if p.Priority != nil {
		m.priority = *p.Priority
	}
	if p.Status != nil {
		m.status = *p.Status
	}

	if p.Description != nil {
		m.desc = nullStringIfEmpty(*p.Description)
	} else if existing.Description.Valid {
		m.desc = existing.Description.String
	}
	if p.Importance != nil {
		m.importance = int(*p.Importance)
	} else if existing.Importance.Valid {
		m.importance = int(existing.Importance.Int16)
	}
	if p.Assignee != nil {
		m.assignee = nullStringIfEmpty(*p.Assignee)
	} else if existing.Assignee.Valid {
		m.assignee = existing.Assignee.String
	}
	if p.DueDate != nil {
		m.dueDate = p.DueDate.UTC().Format(time.RFC3339Nano)
	} else if existing.DueDate.Valid {
		m.dueDate = existing.DueDate.Time.UTC().Format(time.RFC3339Nano)
	}
	if p.Context != nil {
		m.taskCtx = nullStringIfEmpty(*p.Context)
	} else if existing.Context.Valid {
		m.taskCtx = existing.Context.String
	}
	return m
}

// UpdateTask performs a partial update of a task by ID. nil fields in p are
// preserved from the existing row (no null-clear support). Pre-reads the existing
// task to fill nil params, then executes a single UPDATE.
// Returns ErrNotFound when no row matching id exists in the configured workspace.
func (s *GTDStore) UpdateTask(ctx context.Context, id uuid.UUID, p gtd.UpdateTaskParams) (*db.Task, error) {
	existing, err := s.taskByID(ctx, id)
	if err != nil {
		return nil, err // ErrNotFound propagated as-is
	}

	m := mergeTaskFields(existing, p)

	const q = `UPDATE tasks
		SET title       = ?2,
		    description = ?3,
		    priority    = ?4,
		    importance  = ?5,
		    assignee    = ?6,
		    due_date    = ?7,
		    context     = ?8,
		    status      = ?9,
		    updated_at  = ?10
		WHERE id = ?1
		  AND (?11 IS NULL OR workspace_id = ?11)`
	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q,
		id.String(), m.title, m.desc, m.priority, m.importance, m.assignee, m.dueDate, m.taskCtx, m.status,
		now, s.db.workspaceArg(),
	)
	if err != nil {
		return nil, errWrap("UpdateTask", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, gtd.ErrNotFound
	}
	return s.taskByID(ctx, id)
}

// UpdateGoal performs a full update of a goal by ID, replacing all mutable fields.
func (s *GTDStore) UpdateGoal(ctx context.Context, id uuid.UUID, p gtd.UpdateGoalParams) (*db.Goal, error) {
	var dueVal any
	if p.DueDate != nil {
		dueVal = p.DueDate.UTC().Format(time.RFC3339Nano)
	}
	const q = `UPDATE goals
		SET title = ?2, description = ?3, area = ?4, status = ?5, due_date = ?6, updated_at = ?7
		WHERE id = ?1
		  AND (?8 IS NULL OR workspace_id = ?8)`
	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		p.Title,
		nullStringIfEmpty(p.Description),
		nullStringIfEmpty(p.Area),
		string(p.Status),
		dueVal,
		now,
		s.db.workspaceArg(),
	)
	if err != nil {
		return nil, errWrap("UpdateGoal", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, gtd.ErrNotFound
	}
	return s.goalByID(ctx, id)
}

// UpdateProject performs a full update of a project by ID, replacing all mutable fields.
func (s *GTDStore) UpdateProject(ctx context.Context, id uuid.UUID, p gtd.UpdateProjectParams) (*db.Project, error) {
	area := p.Area
	if area == "" {
		area = defaultProjectArea
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	const q = `UPDATE projects
		SET title = ?2, description = ?3, area = ?4, priority = ?5, status = ?6, goal_id = ?7, updated_at = ?8
		WHERE id = ?1
		  AND (?9 IS NULL OR workspace_id = ?9)`
	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		p.Title,
		nullStringIfEmpty(p.Description),
		area,
		priority,
		string(p.Status),
		nullStringFromUUID(p.GoalID),
		now,
		s.db.workspaceArg(),
	)
	if err != nil {
		return nil, errWrap("UpdateProject", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, gtd.ErrNotFound
	}
	return s.projectByID(ctx, id)
}

// UpdateProjectStatus sets the status of a project.
func (s *GTDStore) UpdateProjectStatus(ctx context.Context, id uuid.UUID, status gtd.ProjectStatus) (*db.Project, error) {
	const q = `UPDATE projects
		SET status = ?2, updated_at = ?3
		WHERE id = ?1
		  AND (?4 IS NULL OR workspace_id = ?4)`
	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q, id.String(), string(status), now, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("UpdateProjectStatus", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, gtd.ErrNotFound
	}
	return s.projectByID(ctx, id)
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
// All statements run inside a single SQLite transaction so a partial state is
// impossible. Tx pattern matches WorkSessionStore.Create (manual Begin +
// defer Rollback + Commit). Workspace authorisation is enforced by an
// explicit pre-check inside the tx BEFORE any cleanup runs: if the task does
// not exist in the configured workspace the tx is rolled back and the call is
// a silent no-op (matching the pre-fix behaviour where a workspace-mismatched
// DELETE simply affected 0 rows on the parent table). The pre-check ensures
// cleanup never touches another workspace's join rows or work_sessions; the
// parent DELETE's workspace filter is now redundant defence-in-depth.
func (s *GTDStore) DeleteTask(ctx context.Context, id uuid.UUID) error {
	idStr := id.String()

	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return errWrap("DeleteTask begin", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Best-effort rollback; ignore err because tx may already be
			// closed if Commit succeeded between the check and this defer.
			_ = tx.Rollback()
		}
	}()

	// 0. Workspace authorisation pre-check. The cleanup statements below are
	// keyed only by task_id, so without this guard a cross-workspace caller
	// could delete another workspace's join rows / NULL its current_task_id
	// pointer (the parent DELETE's workspace filter would 0-row but the
	// damage to neighbouring tables would already be done).
	var exists int
	row := tx.QueryRowContext(ctx,
		`SELECT EXISTS(
		    SELECT 1 FROM tasks
		     WHERE id = ?1
		       AND (?2 IS NULL OR workspace_id = ?2)
		 )`,
		idStr, s.db.workspaceArg(),
	)
	if err = row.Scan(&exists); err != nil {
		return errWrap("DeleteTask workspace pre-check", err)
	}
	if exists == 0 {
		// Roll back the empty tx and silently no-op, matching the pre-fix
		// behaviour where a missing / workspace-mismatched task simply
		// affected 0 rows on the parent DELETE.
		if rbErr := tx.Rollback(); rbErr != nil {
			return errWrap("DeleteTask rollback after workspace miss", rbErr)
		}
		committed = true // suppress the deferred Rollback (already done).
		return nil
	}

	// 1. Remove join-table rows (was ON DELETE CASCADE on work_session_tasks.task_id).
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM work_session_tasks WHERE task_id = ?1`, idStr,
	); err != nil {
		return errWrap("DeleteTask cleanup work_session_tasks", err)
	}

	// 2. NULL out work_sessions.current_task_id (was ON DELETE SET NULL).
	if _, err = tx.ExecContext(ctx,
		`UPDATE work_sessions
		    SET current_task_id = NULL,
		        updated_at      = ?2
		  WHERE current_task_id = ?1`,
		idStr, nowRFC3339(),
	); err != nil {
		return errWrap("DeleteTask nullify work_sessions.current_task_id", err)
	}

	// 3. Delete the task itself, scoped to the configured workspace.
	if _, err = tx.ExecContext(ctx,
		`DELETE FROM tasks
		   WHERE id = ?1
		     AND (?2 IS NULL OR workspace_id = ?2)`,
		idStr, s.db.workspaceArg(),
	); err != nil {
		return errWrap("DeleteTask", err)
	}

	if err = tx.Commit(); err != nil {
		return errWrap("DeleteTask commit", err)
	}
	committed = true
	return nil
}

// TopPendingTask returns the single highest-priority pending task in the
// configured workspace, ordered by priority ASC NULLS LAST, importance ASC
// NULLS LAST, created_at ASC. Returns nil, nil when no pending task exists.
func (s *GTDStore) TopPendingTask(ctx context.Context) (*db.Task, error) {
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks
		WHERE status = 'pending'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY priority ASC NULLS LAST, importance ASC NULLS LAST, created_at ASC
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, s.db.workspaceArg())
	t, err := scanTask(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // sentinel: no pending task is not an error; callers render {"task":null}
	}
	if err != nil {
		return nil, errWrap("TopPendingTask", err)
	}
	return &t, nil
}

// RecentCompletedTasks returns recently-completed tasks for a project,
// scoped to the configured workspace, ordered by updated_at DESC. SQLite
// parity with gtd.Store.RecentCompletedTasks.
func (s *GTDStore) RecentCompletedTasks(ctx context.Context, projectID uuid.UUID, limit int32) ([]db.Task, error) {
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks
		WHERE status = 'completed'
		  AND project_id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY updated_at DESC, id DESC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, projectID.String(), s.db.workspaceArg(), limit)
	if err != nil {
		return nil, errWrap("RecentCompletedTasks", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.Task
	for rows.Next() {
		t, err := scanTask(rows.Scan)
		if err != nil {
			return nil, errWrap("RecentCompletedTasks scan", err)
		}
		out = append(out, t)
	}
	return out, errWrap("RecentCompletedTasks iter", rows.Err())
}

// RecentActivityByProject returns activity_log rows for a project since the
// given timestamp, scoped to the configured workspace, newest first. SQLite
// parity with gtd.Store.RecentActivityByProject.
func (s *GTDStore) RecentActivityByProject(
	ctx context.Context, projectID uuid.UUID, since time.Time, maxRows int32,
) ([]db.ActivityLog, error) {
	const q = `SELECT id, workspace_id, actor, project_id, action, notes, created_at
		FROM activity_log
		WHERE project_id = ?1
		  AND created_at >= ?2
		  AND (?3 IS NULL OR workspace_id = ?3)
		ORDER BY created_at DESC, id DESC
		LIMIT ?4`
	sinceStr := since.UTC().Format(time.RFC3339Nano)
	rows, err := s.db.conn.QueryContext(ctx, q, projectID.String(), sinceStr, s.db.workspaceArg(), maxRows)
	if err != nil {
		return nil, errWrap("RecentActivityByProject", err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.ActivityLog
	for rows.Next() {
		var (
			a                      db.ActivityLog
			idStr                  string
			workspaceNS, projectNS sql.NullString
			notesNS, createdNS     sql.NullString
		)
		if err := rows.Scan(&idStr, &workspaceNS, &a.Actor, &projectNS, &a.Action, &notesNS, &createdNS); err != nil {
			return nil, errWrap("RecentActivityByProject scan", err)
		}
		if id, err := uuid.Parse(idStr); err == nil {
			a.ID = id
		}
		a.WorkspaceID = pgtypeUUID(nsString(workspaceNS))
		a.ProjectID = pgtypeUUID(nsString(projectNS))
		a.Notes = pgtypeText(notesNS.String, notesNS.Valid)
		a.CreatedAt = parseTimestamptz(createdNS)
		out = append(out, a)
	}
	return out, errWrap("RecentActivityByProject iter", rows.Err())
}

// WeeklyProgress returns completed-this-week and total-active counts.
func (s *GTDStore) WeeklyProgress(ctx context.Context) (completed, total int64, err error) {
	// SQLite has no date_trunc; compute Monday 00:00 UTC of this week in Go.
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7 // treat Sunday as end of week, ISO style
	}
	monday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(-time.Duration(weekday-1) * 24 * time.Hour)
	weekStart := monday.Format(time.RFC3339Nano)

	const completedQ = `SELECT COUNT(*) FROM tasks
		WHERE status = 'completed'
		  AND updated_at >= ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`
	if err = s.db.conn.QueryRowContext(ctx, completedQ, weekStart, s.db.workspaceArg()).Scan(&completed); err != nil {
		return 0, 0, errWrap("WeeklyProgress completed", err)
	}

	const totalQ = `SELECT COUNT(*) FROM tasks
		WHERE status IN ('pending','in_progress')
		  AND (?1 IS NULL OR workspace_id = ?1)`
	if err = s.db.conn.QueryRowContext(ctx, totalQ, s.db.workspaceArg()).Scan(&total); err != nil {
		return 0, 0, errWrap("WeeklyProgress total", err)
	}
	return completed, total, nil
}

// isUniqueViolation reports whether err is a SQLite UNIQUE-constraint failure.
// modernc.org/sqlite returns errors whose .Error() includes "UNIQUE constraint failed".
func isUniqueViolation(err error) bool {
	return err != nil && containsCI(err.Error(), "UNIQUE constraint failed")
}

func containsCI(s, substr string) bool {
	// Lightweight, locale-naive contains. Imported from strings in Go std lib
	// would suffice; kept inline to avoid an import for a one-liner.
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
