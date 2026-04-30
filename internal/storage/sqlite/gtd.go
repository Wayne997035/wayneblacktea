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
		t.Importance = pgtype.Int2{Int16: int16(importanceNI.Int32), Valid: true} //nolint:gosec
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

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }

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

// CreateProject inserts a new project, generating a UUID and returning the row.
func (s *GTDStore) CreateProject(ctx context.Context, p gtd.CreateProjectParams) (*db.Project, error) {
	id := uuid.New()
	area := p.Area
	if area == "" {
		area = "projects"
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

func (s *GTDStore) projectByID(ctx context.Context, id uuid.UUID) (*db.Project, error) {
	const q = `SELECT ` + projectsSelectCols + ` FROM projects WHERE id = ?1 LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	p, err := scanProject(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gtd.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("projectByID", err)
	}
	return &p, nil
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
	const q = `INSERT INTO tasks
		(id, workspace_id, project_id, title, description, priority,
		 importance, context, assignee, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?10)`
	now := nowRFC3339()
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.ProjectID),
		p.Title, nullStringIfEmpty(p.Description), priority, importance,
		nullStringIfEmpty(p.Context), nullStringIfEmpty(p.Assignee), now)
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

// DeleteTask permanently removes a task by ID.
func (s *GTDStore) DeleteTask(ctx context.Context, id uuid.UUID) error {
	const q = `DELETE FROM tasks
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`
	if _, err := s.db.conn.ExecContext(ctx, q, id.String(), s.db.workspaceArg()); err != nil {
		return errWrap("DeleteTask", err)
	}
	return nil
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
