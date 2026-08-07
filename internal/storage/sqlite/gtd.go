package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/sanitize"
	"github.com/Wayne997035/wayneblacktea/internal/validator"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// defaultProjectArea is the fallback area applied when an empty area is
// provided to CreateProject, CreateProjectTx, or UpdateProject.
const defaultProjectArea = "projects"

// defaultTaskKind is the fallback kind applied when an empty kind is
// provided to CreateTask, scanTask, or ImportTask.
const defaultTaskKind = "general"

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
	created_at, updated_at, kind, branch_name, pr_url, commit_shas`

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
		kindStr                                                                string
		branchNameNS, prURLNS                                                  sql.NullString
		commitSHAsStr                                                          sql.NullString
	)

	err := scan(&idStr, &workspaceIDNS, &projectIDNS, &t.Title, &descNS, &statusStr,
		&t.Priority, &importanceNI, &contextNS, &assigneeNS, &dueDateNS, &artifactNS,
		&createdNS, &updNS, &kindStr, &branchNameNS, &prURLNS, &commitSHAsStr)
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
	if kindStr == "" {
		kindStr = defaultTaskKind
	}
	t.Kind = kindStr
	t.BranchName = pgtypeText(branchNameNS.String, branchNameNS.Valid)
	t.PRUrl = pgtypeText(prURLNS.String, prURLNS.Valid)
	// commit_shas is stored as JSON TEXT in SQLite; decode into []string.
	if commitSHAsStr.Valid && commitSHAsStr.String != "" && commitSHAsStr.String != "[]" {
		var shas []string
		if jsonErr := json.Unmarshal([]byte(commitSHAsStr.String), &shas); jsonErr == nil {
			t.CommitSHAs = shas
		}
	}
	return t, nil
}

// nsString returns the underlying string only when Valid; otherwise empty.
func nsString(ns sql.NullString) string {
	if !ns.Valid {
		return ""
	}
	return ns.String
}

const projectsSelectCols = `id, workspace_id, goal_id, name, title, description, status, area, priority, created_at, updated_at, repo_name`

func scanProject(scan func(...any) error) (db.Project, error) {
	var (
		p                         db.Project
		idStr, statusStr, areaStr string
		workspaceIDNS, goalIDNS   sql.NullString
		descNS, createdNS, updNS  sql.NullString
		repoNameNS                sql.NullString
	)
	err := scan(&idStr, &workspaceIDNS, &goalIDNS, &p.Name, &p.Title, &descNS,
		&statusStr, &areaStr, &p.Priority, &createdNS, &updNS, &repoNameNS)
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
	p.RepoName = pgtypeText(repoNameNS.String, repoNameNS.Valid)
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

// nullTimeArg returns NULL for a nil pointer, otherwise t formatted in the
// same RFC3339 TEXT layout this package stores timestamps in (nowRFC3339
// above), so lexicographic comparison against stored updated_at/created_at
// columns is chronologically correct.
func nullTimeArg(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
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
// repo_name is persisted when non-empty; empty string stores NULL (parity with
// migration 000037 which added the nullable TEXT column).
func (s *GTDStore) CreateProject(ctx context.Context, p gtd.CreateProjectParams) (*db.Project, error) {
	if !validator.IsValidRepoName(p.RepoName) {
		return nil, fmt.Errorf("creating project %q: %w", p.Name, gtd.ErrInvalidRepoName)
	}
	id := uuid.New()
	area := p.Area
	if area == "" {
		area = defaultProjectArea
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	const q = `INSERT INTO projects
		(id, workspace_id, goal_id, name, title, description, area, priority, repo_name, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?10)`
	now := nowRFC3339()
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.GoalID),
		p.Name, p.Title, nullStringIfEmpty(p.Description), area, priority,
		nullStringIfEmpty(p.RepoName), now)
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

// ImportProject inserts a project row using p's own id/created_at/updated_at
// instead of generating fresh ones, so cross-table references (goal_id,
// task.project_id) survive a Postgres-to-SQLite copy verbatim. Used by
// cmd/qa-seed to replicate production data into a disposable local SQLite
// file for integration-qa. Fails (no upsert) on a duplicate id — callers
// MUST import into a fresh database, never re-import into one that already
// has the row.
func (s *GTDStore) ImportProject(ctx context.Context, p db.Project) error {
	const q = `INSERT INTO projects
		(id, workspace_id, goal_id, name, title, description, status, area, priority, repo_name, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12)`
	_, err := s.db.conn.ExecContext(ctx, q,
		p.ID.String(), pgUUIDToNullString(p.WorkspaceID), pgUUIDToNullString(p.GoalID),
		p.Name, p.Title, pgTextToNullString(p.Description), p.Status, p.Area, p.Priority,
		pgTextToNullString(p.RepoName),
		pgTimestamptzToString(p.CreatedAt), pgTimestamptzToString(p.UpdatedAt))
	if err != nil {
		return errWrap("ImportProject", err)
	}
	return nil
}

// ImportGoal inserts a goal row using g's own id/created_at/updated_at/status
// instead of generating fresh ones. See ImportProject doc comment for the
// full rationale (cmd/qa-seed fidelity import).
func (s *GTDStore) ImportGoal(ctx context.Context, g db.Goal) error {
	const q = `INSERT INTO goals
		(id, workspace_id, title, description, status, area, due_date, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)`
	_, err := s.db.conn.ExecContext(ctx, q,
		g.ID.String(), pgUUIDToNullString(g.WorkspaceID), g.Title,
		pgTextToNullString(g.Description), g.Status, pgTextToNullString(g.Area),
		pgTimestamptzToNullString(g.DueDate),
		pgTimestamptzToString(g.CreatedAt), pgTimestamptzToString(g.UpdatedAt))
	if err != nil {
		return errWrap("ImportGoal", err)
	}
	return nil
}

// ImportTask inserts a task row using t's own id/created_at/updated_at/status
// (plus checklist/vision_item_id, which CreateTask does not set) instead of
// generating fresh ones. See ImportProject doc comment for the full
// rationale (cmd/qa-seed fidelity import).
func (s *GTDStore) ImportTask(ctx context.Context, t db.Task) error {
	var importance any
	if t.Importance.Valid {
		importance = int(t.Importance.Int16)
	}
	kind := t.Kind
	if kind == "" {
		kind = defaultTaskKind
	}
	commitSHAsJSON := "[]"
	if len(t.CommitSHAs) > 0 {
		b, err := json.Marshal(t.CommitSHAs)
		if err != nil {
			return errWrap("ImportTask marshal commit_shas", err)
		}
		commitSHAsJSON = string(b)
	}
	checklistJSON := "[]"
	if len(t.Checklist) > 0 {
		checklistJSON = string(t.Checklist)
	}
	const q = `INSERT INTO tasks
		(id, workspace_id, project_id, title, description, status, priority, importance,
		 context, assignee, due_date, artifact, kind, branch_name, pr_url, commit_shas,
		 checklist, vision_item_id, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?16, ?17, ?18, ?19, ?20)`
	_, err := s.db.conn.ExecContext(ctx, q,
		t.ID.String(), pgUUIDToNullString(t.WorkspaceID), pgUUIDToNullString(t.ProjectID),
		t.Title, pgTextToNullString(t.Description), t.Status, t.Priority, importance,
		pgTextToNullString(t.Context), pgTextToNullString(t.Assignee),
		pgTimestamptzToNullString(t.DueDate), pgTextToNullString(t.Artifact),
		kind, pgTextToNullString(t.BranchName), pgTextToNullString(t.PRUrl), commitSHAsJSON,
		checklistJSON, pgUUIDToNullString(t.VisionItemID),
		pgTimestamptzToString(t.CreatedAt), pgTimestamptzToString(t.UpdatedAt))
	if err != nil {
		return errWrap("ImportTask", err)
	}
	return nil
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
	const q = `INSERT INTO projects
		(id, workspace_id, goal_id, name, title, description, area, priority, repo_name, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?10)`
	now := nowRFC3339()
	_, err := tx.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.GoalID),
		p.Name, p.Title, nullStringIfEmpty(p.Description), area, priority,
		nullStringIfEmpty(p.RepoName), now)
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

// TasksFiltered returns tasks matching the given gtd.TaskFilter with pagination.
// It is the SQLite backend for the list_tasks MCP tool. The existing Tasks
// method is left untouched so its non-test callers keep active-only semantics.
//
// Status "" or "active" → pending+in_progress; "all" → every task status; any
// other value → exact match. Callers pass Limit+1 to detect has_more without a
// COUNT query.
func (s *GTDStore) TasksFiltered(ctx context.Context, f gtd.TaskFilter) ([]db.Task, error) {
	var (
		rows *sql.Rows
		err  error
	)
	updatedSinceArg := nullTimeArg(f.UpdatedSince)
	switch f.Status {
	case "", "active":
		const q = `SELECT ` + tasksSelectCols + ` FROM tasks
			WHERE status IN ('pending','in_progress')
			  AND (?1 IS NULL OR project_id = ?1)
			  AND (?2 IS NULL OR workspace_id = ?2)
			  AND (?5 IS NULL OR updated_at >= ?5)
			ORDER BY priority ASC, created_at ASC
			LIMIT ?3 OFFSET ?4`
		rows, err = s.db.conn.QueryContext(ctx, q,
			nullStringFromUUID(f.ProjectID), s.db.workspaceArg(), f.Limit, f.Offset, updatedSinceArg)
	case "all":
		const q = `SELECT ` + tasksSelectCols + ` FROM tasks
			WHERE (?1 IS NULL OR project_id = ?1)
			  AND (?2 IS NULL OR workspace_id = ?2)
			  AND (?5 IS NULL OR updated_at >= ?5)
			ORDER BY priority ASC, created_at ASC
			LIMIT ?3 OFFSET ?4`
		rows, err = s.db.conn.QueryContext(ctx, q,
			nullStringFromUUID(f.ProjectID), s.db.workspaceArg(), f.Limit, f.Offset, updatedSinceArg)
	default:
		const q = `SELECT ` + tasksSelectCols + ` FROM tasks
			WHERE status = ?1
			  AND (?2 IS NULL OR project_id = ?2)
			  AND (?3 IS NULL OR workspace_id = ?3)
			  AND (?6 IS NULL OR updated_at >= ?6)
			ORDER BY priority ASC, created_at ASC
			LIMIT ?4 OFFSET ?5`
		rows, err = s.db.conn.QueryContext(ctx, q,
			f.Status, nullStringFromUUID(f.ProjectID), s.db.workspaceArg(), f.Limit, f.Offset, updatedSinceArg)
	}
	if err != nil {
		return nil, errWrap("TasksFiltered", err)
	}
	return s.scanTaskRows(rows, "TasksFiltered")
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
//   - tasks with no due_date, regardless of importance (unscheduled bucket;
//     priority-ordered so high-importance surfaces first)
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
		      OR (due_date IS NULL)
		  )
		ORDER BY due_date ASC NULLS LAST, priority ASC, created_at ASC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), windowEndStr, fetchLimit)
	if err != nil {
		return nil, errWrap("UpcomingTasks", err)
	}
	return s.scanTaskRows(rows, "UpcomingTasks")
}

// PullForwardTasks mirrors the Postgres-side gtd.Store.PullForwardTasks: up to
// gtd.PullForwardCap pending/in_progress, importance=1 tasks whose due_date is
// NULL or falls on/after "tomorrow" (midnight, Asia/Taipei, relative to
// refDate — see gtd.PullForwardTomorrowStart, shared with the Postgres
// backend so the two cannot drift). SQLite stores due_date as RFC3339Nano
// TEXT (UTC), so lexicographic string comparison against the tomorrow-start
// boundary (itself converted to UTC before formatting) is equivalent to a
// timestamp comparison — the same invariant UpcomingTasks relies on above.
func (s *GTDStore) PullForwardTasks(ctx context.Context, refDate time.Time) ([]db.Task, error) {
	tomorrowStart, err := gtd.PullForwardTomorrowStart(refDate)
	if err != nil {
		return nil, errWrap("PullForwardTasks", err)
	}
	tomorrowStartStr := tomorrowStart.UTC().Format(time.RFC3339Nano)
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks
		WHERE status IN ('pending','in_progress')
		  AND importance = 1
		  AND (?1 IS NULL OR workspace_id = ?1)
		  AND (due_date IS NULL OR due_date >= ?2)
		ORDER BY due_date ASC NULLS LAST, priority ASC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), tomorrowStartStr, gtd.PullForwardCap)
	if err != nil {
		return nil, errWrap("PullForwardTasks", err)
	}
	return s.scanTaskRows(rows, "PullForwardTasks")
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
// Hand-rolled INSERT (instead of sqlc CreateTask) so that branch_name, pr_url,
// and commit_shas columns added in migration 000047 are included without
// requiring a sqlc regeneration run.
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
	kind := p.Kind
	if kind == "" {
		kind = defaultTaskKind
	}

	// Domain-layer gate (P6.7, sunk from the MCP-only guard in P6.6): any
	// non-empty assignee MUST resolve through the canonical actor allowlist
	// before it reaches storage, regardless of caller. Empty stays empty
	// (unowned task — new tasks always insert as 'pending', so no
	// in_progress guard applies here).
	assignee := p.Assignee
	if strings.TrimSpace(assignee) != "" {
		normalized, nerr := gtd.NormalizeActor(assignee)
		if nerr != nil {
			return nil, fmt.Errorf("creating task %q: %w", p.Title, nerr)
		}
		assignee = normalized
	}

	// commit_shas stored as JSON TEXT in SQLite; always empty on creation.
	const commitSHAsJSON = "[]"
	const q = `INSERT INTO tasks
		(id, workspace_id, project_id, title, description, priority,
		 importance, context, assignee, due_date, kind,
		 branch_name, pr_url, commit_shas, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?15)`
	now := nowRFC3339()
	var branchVal any
	if p.BranchName != nil && *p.BranchName != "" {
		branchVal = *p.BranchName
	}
	var prURLVal any
	if p.PRUrl != nil && *p.PRUrl != "" {
		prURLVal = *p.PRUrl
	}
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.ProjectID),
		p.Title, nullStringIfEmpty(p.Description), priority, importance,
		nullStringIfEmpty(p.Context), nullStringIfEmpty(assignee), dueVal, kind,
		branchVal, prURLVal, commitSHAsJSON, now)
	if err != nil {
		return nil, errWrap("CreateTask", err)
	}
	return s.taskByID(ctx, id)
}

// CreateTaskTx inserts a new task within the provided *sql.Tx. It is the
// transactional counterpart of CreateTask — mirroring CreateGoalTx /
// CreateProjectTx / LearningStore.CreateConceptTx — and is used by
// confirm_plan's SQLite atomic path so phase tasks and decisions commit or
// roll back together in one transaction. Returns the freshly-generated ID
// only (not the full row), matching the CreateGoalTx/CreateProjectTx return
// shape: callers that already know the input fields (confirm_plan does —
// it just supplied them) don't need a post-commit re-read to get them back.
func (s *GTDStore) CreateTaskTx(ctx context.Context, tx *sql.Tx, p gtd.CreateTaskParams) (uuid.UUID, error) {
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
	kind := p.Kind
	if kind == "" {
		kind = defaultTaskKind
	}

	// Domain-layer gate (P6.7) — same as CreateTask above.
	assignee := p.Assignee
	if strings.TrimSpace(assignee) != "" {
		normalized, nerr := gtd.NormalizeActor(assignee)
		if nerr != nil {
			return uuid.UUID{}, fmt.Errorf("creating task %q: %w", p.Title, nerr)
		}
		assignee = normalized
	}

	// commit_shas stored as JSON TEXT in SQLite; always empty on creation.
	const commitSHAsJSON = "[]"
	const q = `INSERT INTO tasks
		(id, workspace_id, project_id, title, description, priority,
		 importance, context, assignee, due_date, kind,
		 branch_name, pr_url, commit_shas, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?11, ?12, ?13, ?14, ?15, ?15)`
	now := nowRFC3339()
	var branchVal any
	if p.BranchName != nil && *p.BranchName != "" {
		branchVal = *p.BranchName
	}
	var prURLVal any
	if p.PRUrl != nil && *p.PRUrl != "" {
		prURLVal = *p.PRUrl
	}
	_, err := tx.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.ProjectID),
		p.Title, nullStringIfEmpty(p.Description), priority, importance,
		nullStringIfEmpty(p.Context), nullStringIfEmpty(assignee), dueVal, kind,
		branchVal, prURLVal, commitSHAsJSON, now)
	if err != nil {
		return uuid.UUID{}, errWrap("CreateTaskTx", err)
	}
	return id, nil
}

// DB returns the underlying *DB handle. Exported so cross-store transactional
// callers (e.g. confirm_plan's SQLite atomic path, which needs to open one
// *sql.Tx and pass it to both CreateTaskTx here and DecisionStore.LogTx) can
// BeginTx once instead of each store opening its own — SQLite is
// single-writer, so two concurrently open transactions on the same
// underlying connection would deadlock.
func (s *GTDStore) DB() *DB { return s.db }

func (s *GTDStore) taskByID(ctx context.Context, id uuid.UUID) (*db.Task, error) {
	const q = `SELECT ` + tasksSelectCols + ` FROM tasks WHERE id = ?1 AND (?2 IS NULL OR workspace_id = ?2) LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg())
	t, err := scanTask(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, gtd.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("taskByID", err)
	}
	return &t, nil
}

// GetTaskByID returns a single task by UUID, scoped to the configured workspace.
// Returns ErrNotFound when no matching row exists. Satisfies gtd.StoreIface.
func (s *GTDStore) GetTaskByID(ctx context.Context, id uuid.UUID) (*db.Task, error) {
	return s.taskByID(ctx, id)
}

// BatchCompleteTasksByPRMatch implements gtd.StoreIface.BatchCompleteTasksByPRMatch
// for the SQLite backend. Runs inside a single *sql.Tx so a partial auto-close
// cannot leave half the matches done. See internal/gtd/iface.go for contract.
func (s *GTDStore) BatchCompleteTasksByPRMatch(ctx context.Context, matches []gtd.Match) (map[uuid.UUID]bool, error) {
	if len(matches) == 0 {
		return map[uuid.UUID]bool{}, nil
	}
	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, errWrap("BatchCompleteTasksByPRMatch begin", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	applied := make(map[uuid.UUID]bool, len(matches))
	now := nowRFC3339()
	for _, m := range matches {
		// Guarded UPDATE: only flip tasks that are STILL pending/in_progress
		// at apply time — mirrors the Postgres guard in
		// internal/gtd/store.go's applyOnePRMatch. status IN
		// ('pending','in_progress') matches the exact precondition
		// gtd.MatchMergedPRs used to select this task as a candidate during
		// preview (see reconcile.go), closing a CWE-367 TOCTOU gap: the old
		// "status != 'completed'" guard let a still-valid reconcile_token
		// silently re-complete a task the user cancelled between preview and
		// confirm. This also keeps the pre-existing idempotency for
		// already-completed tasks (RowsAffected=0 either way).
		const upd = `UPDATE tasks
			SET status     = 'completed',
			    artifact   = ?2,
			    pr_url     = ?2,
			    updated_at = ?3
			WHERE id = ?1
			  AND (?4 IS NULL OR workspace_id = ?4)
			  AND status IN ('pending', 'in_progress')`
		res, uerr := tx.ExecContext(ctx, upd, m.TaskID.String(), m.PRUrl, now, s.db.workspaceArg())
		if uerr != nil {
			return nil, errWrap("BatchCompleteTasksByPRMatch update", uerr)
		}
		affected, _ := res.RowsAffected()
		if affected == 0 {
			continue
		}
		applied[m.TaskID] = true

		// audit: same notes shape as the PG path so reads are uniform.
		notes := "auto-closed by reconcile_merged_prs reason=" + string(m.Reason) +
			" pr_url=" + m.PRUrl + " head_ref=" + m.PRHeadRef
		if m.BodyExcerpt != "" {
			notes += " body=" + m.BodyExcerpt
		}
		const ins = `INSERT INTO activity_log (id, workspace_id, actor, project_id, action, notes)
			VALUES (?1, ?2, 'system', NULL, 'pr_auto_close', ?3)`
		if _, ierr := tx.ExecContext(
			ctx, ins,
			uuid.New().String(), s.db.workspaceArg(), sanitize.Notes(notes),
		); ierr != nil {
			return nil, errWrap("BatchCompleteTasksByPRMatch activity_log", ierr)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, errWrap("BatchCompleteTasksByPRMatch commit", err)
	}
	committed = true
	return applied, nil
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
	sinceStr := since.UTC().Format("2006-01-02T15:04:05.000Z07:00")
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

// PruneOlderThan hard-deletes activity_log rows created before cutoff.
// Global cleanup (no workspace filter) — matches the Postgres Store.
func (s *GTDStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM activity_log WHERE created_at < ?1`
	cutoffStr := cutoff.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	res, err := s.db.conn.ExecContext(ctx, q, cutoffStr)
	if err != nil {
		return 0, errWrap("PruneOlderThan", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, errWrap("PruneOlderThan rows affected", err)
	}
	return n, nil
}

// LogActivity records an activity log entry. project may be nil.
func (s *GTDStore) LogActivity(ctx context.Context, actor, action string, projectID *uuid.UUID, notes string) error {
	const q = `INSERT INTO activity_log (id, workspace_id, actor, project_id, action, notes)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6)`
	_, err := s.db.conn.ExecContext(ctx, q,
		uuid.New().String(), s.db.workspaceArg(), actor,
		nullStringFromUUID(projectID), action, nullStringIfEmpty(sanitize.Notes(notes)))
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
	const q = `SELECT ` + goalsSelectCols + ` FROM goals WHERE id = ?1 AND (?2 IS NULL OR workspace_id = ?2) LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg())
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
	// Domain-layer gate (P6.7): UpdateTaskStatus takes no assignee argument,
	// so an existing-row read is the only way to check "does this task have
	// an owner" before allowing the in_progress transition. Skipped for any
	// other target status — no extra read on the hot pending/completed/
	// cancelled paths.
	if status == gtd.TaskStatusInProgress {
		existing, err := s.taskByID(ctx, id)
		if err != nil {
			return nil, err // gtd.ErrNotFound already wrapped
		}
		existingAssignee := ""
		if existing.Assignee.Valid {
			existingAssignee = existing.Assignee.String
		}
		if rerr := gtd.RequireAssigneeForInProgress(existingAssignee, status); rerr != nil {
			return nil, fmt.Errorf("updating task %s status: %w", id, rerr)
		}
	}

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

// BeginTask atomically sets a task to in_progress and records a
// work_session_started activity log entry.
//
// Idempotency: if the task is already in_progress, the task row is returned
// as-is without writing a duplicate activity_log row.
// Returns gtd.ErrNotFound when no task matches id inside the configured workspace.
func (s *GTDStore) BeginTask(ctx context.Context, id uuid.UUID) (*db.Task, error) {
	task, err := gtd.BeginTaskOrchestration(ctx, id, &sqliteBeginTaskAdapter{s: s, id: id})
	if err != nil {
		return nil, fmt.Errorf("%w", err) // context already added by BeginTaskOrchestration
	}
	return task, nil
}

// sqliteBeginTaskAdapter is the SQLite-backed gtd.BeginTaskAdapter used by
// GTDStore.BeginTask. Constructed fresh per call; holds the open tx and the
// pre-tx title (stashed in ReadExisting) as state between the interface's
// method calls.
type sqliteBeginTaskAdapter struct {
	s             *GTDStore
	id            uuid.UUID
	tx            *sql.Tx
	existingTitle string
}

func (a *sqliteBeginTaskAdapter) ReadExisting(ctx context.Context) (*db.Task, error) {
	task, err := a.s.taskByID(ctx, a.id)
	if err != nil {
		return nil, err
	}
	a.existingTitle = task.Title
	return task, nil
}

func (a *sqliteBeginTaskAdapter) BeginTx(ctx context.Context) error {
	tx, err := a.s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w", err) // context added one level up by BeginTaskOrchestration
	}
	a.tx = tx
	return nil
}

// GuardedUpdate only flips status when status != in_progress, guarding
// against a TOCTOU race between the idempotency check and this write.
func (a *sqliteBeginTaskAdapter) GuardedUpdate(ctx context.Context) (bool, error) {
	now := nowRFC3339()
	res, err := a.tx.ExecContext(
		ctx,
		`UPDATE tasks
		    SET status = 'in_progress', updated_at = ?2
		  WHERE id = ?1
		    AND (?3 IS NULL OR workspace_id = ?3)
		    AND status != 'in_progress'`,
		a.id.String(), now, a.s.db.workspaceArg(),
	)
	if err != nil {
		return false, fmt.Errorf("%w", err) // context added one level up by BeginTaskOrchestration
	}
	affected, _ := res.RowsAffected()
	return affected == 0, nil
}

// ResolveGuardBlocked rolls back the open tx BEFORE re-reading outside it.
// SQLite serialises all access through a single pooled connection
// (db.SetMaxOpenConns(1) in Open); re-reading via s.taskByID while the tx
// still holds that one connection would block forever waiting for a
// connection the same goroutine is holding open — the rollback-first order is
// a correctness requirement, not a style choice.
func (a *sqliteBeginTaskAdapter) ResolveGuardBlocked(ctx context.Context) (*db.Task, error) {
	_ = a.tx.Rollback()
	task, err := a.s.taskByID(ctx, a.id)
	if err != nil {
		return nil, err
	}
	if task.Status == string(gtd.TaskStatusInProgress) {
		return task, nil // raced to in_progress — idempotent
	}
	return nil, gtd.ErrNotFound
}

// CreateActivityLog uses a.existingTitle — stashed in ReadExisting, before
// the transaction opened — matching SQLite's original (pre-refactor)
// title-source behavior. Unlike Postgres, SQLite's GuardedUpdate does not
// return a fresh row, so there is no tx-fresh title available here.
func (a *sqliteBeginTaskAdapter) CreateActivityLog(ctx context.Context) error {
	_, err := a.tx.ExecContext(
		ctx,
		`INSERT INTO activity_log (id, workspace_id, actor, project_id, action, notes)
		 VALUES (?1, ?2, 'system', NULL, 'work_session_started', ?3)`,
		uuid.New().String(), a.s.db.workspaceArg(), sanitize.Notes("task: "+a.existingTitle),
	)
	if err != nil {
		return fmt.Errorf("%w", err) // context added one level up by BeginTaskOrchestration
	}
	return nil
}

func (a *sqliteBeginTaskAdapter) Commit(ctx context.Context) (*db.Task, error) {
	if err := a.tx.Commit(); err != nil {
		return nil, fmt.Errorf("%w", err) // context added one level up by BeginTaskOrchestration
	}
	return a.s.taskByID(ctx, a.id)
}

func (a *sqliteBeginTaskAdapter) Rollback(context.Context) {
	_ = a.tx.Rollback()
}

// mergedTaskFields holds the resolved column values for an UpdateTask write,
// computed by mergeTaskFields from the existing row and the patch params.
type mergedTaskFields struct {
	title          string
	desc           any
	priority       int32
	importance     any
	assignee       any
	dueDate        any
	taskCtx        any
	status         string
	kind           string
	branchName     any
	prURL          any
	commitSHAsJSON string
}

// mergeTaskFields merges non-nil patch params over the existing task row values,
// producing a complete set of column values ready for an UPDATE statement.
// Split into two helpers (mergeTaskBaseFields + mergeTaskPRFields) to keep each
// below the gocyclo threshold of 15.
func mergeTaskFields(existing *db.Task, p gtd.UpdateTaskParams) mergedTaskFields {
	m := mergeTaskBaseFields(existing, p)
	mergeTaskPRFields(existing, p, &m)
	return m
}

// mergeTaskBaseFields resolves the core (non-PR) task columns.
func mergeTaskBaseFields(existing *db.Task, p gtd.UpdateTaskParams) mergedTaskFields {
	m := mergedTaskFields{
		title:    existing.Title,
		priority: existing.Priority,
		status:   existing.Status,
		kind:     existing.Kind,
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
	if p.Kind != nil {
		m.kind = *p.Kind
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

// mergeTaskPRFields resolves the branch_name / pr_url / commit_shas columns
// and writes them into m. Extracted to keep mergeTaskBaseFields complexity low.
func mergeTaskPRFields(existing *db.Task, p gtd.UpdateTaskParams, m *mergedTaskFields) {
	// branch_name: nil → preserve; non-nil → overwrite (empty string clears to NULL)
	if p.BranchName != nil {
		if *p.BranchName != "" {
			m.branchName = *p.BranchName
		}
	} else if existing.BranchName.Valid {
		m.branchName = existing.BranchName.String
	}

	// pr_url: nil → preserve; non-nil → overwrite (empty string clears to NULL)
	if p.PRUrl != nil {
		if *p.PRUrl != "" {
			m.prURL = *p.PRUrl
		}
	} else if existing.PRUrl.Valid {
		m.prURL = existing.PRUrl.String
	}

	// commit_shas: nil → preserve; non-nil → replace entirely.
	commitSHAs := existing.CommitSHAs
	if p.CommitSHAs != nil {
		commitSHAs = p.CommitSHAs
	}
	if len(commitSHAs) == 0 {
		m.commitSHAsJSON = "[]"
		return
	}
	if b, marshalErr := json.Marshal(commitSHAs); marshalErr == nil {
		m.commitSHAsJSON = string(b)
	} else {
		m.commitSHAsJSON = "[]"
	}
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

	// Domain-layer gate (P6.7): a NEW assignee value being set THIS call must
	// resolve through the canonical allowlist before merging in. Empty
	// string is the explicit-clear case (mergeTaskFields' nullStringIfEmpty
	// turns it into NULL) and is left untouched — clearing is always allowed.
	if p.Assignee != nil && strings.TrimSpace(*p.Assignee) != "" {
		normalized, nerr := gtd.NormalizeActor(*p.Assignee)
		if nerr != nil {
			return nil, fmt.Errorf("updating task %s: %w", id, nerr)
		}
		p.Assignee = &normalized
	}

	m := mergeTaskFields(existing, p)

	// Once both merged values are known, reject a write that would leave the
	// row in_progress with no resolved assignee — whether that's because this
	// call clears assignee while also setting in_progress, or because the
	// row already had none and this call didn't supply one.
	mergedAssignee := ""
	if av, ok := m.assignee.(string); ok {
		mergedAssignee = av
	}
	if rerr := gtd.RequireAssigneeForInProgress(mergedAssignee, gtd.TaskStatus(m.status)); rerr != nil {
		return nil, fmt.Errorf("updating task %s: %w", id, rerr)
	}

	const q = `UPDATE tasks
		SET title       = ?2,
		    description = ?3,
		    priority    = ?4,
		    importance  = ?5,
		    assignee    = ?6,
		    due_date    = ?7,
		    context     = ?8,
		    status      = ?9,
		    kind        = ?10,
		    branch_name = ?11,
		    pr_url      = ?12,
		    commit_shas = ?13,
		    updated_at  = ?14
		WHERE id = ?1
		  AND (?15 IS NULL OR workspace_id = ?15)`
	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(
		ctx, q,
		id.String(), m.title, m.desc, m.priority, m.importance, m.assignee, m.dueDate, m.taskCtx, m.status, m.kind,
		m.branchName, m.prURL, m.commitSHAsJSON,
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
	res, err := s.db.conn.ExecContext(
		ctx, q,
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
// RepoName semantics: nil → preserve existing DB value; non-nil → overwrite
// (empty string clears to NULL). Two query branches avoid a gap in parameter
// positions that would confuse SQLite's positional binding.
func (s *GTDStore) UpdateProject(ctx context.Context, id uuid.UUID, p gtd.UpdateProjectParams) (*db.Project, error) {
	area := p.Area
	if area == "" {
		area = defaultProjectArea
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	now := nowRFC3339()
	var (
		res sql.Result
		err error
	)
	if p.RepoName != nil {
		const q = `UPDATE projects
			SET title = ?2, description = ?3, area = ?4, priority = ?5, status = ?6,
			    goal_id = ?7, repo_name = ?8, updated_at = ?9
			WHERE id = ?1
			  AND (?10 IS NULL OR workspace_id = ?10)`
		res, err = s.db.conn.ExecContext(
			ctx, q,
			id.String(),
			p.Title,
			nullStringIfEmpty(p.Description),
			area,
			priority,
			string(p.Status),
			nullStringFromUUID(p.GoalID),
			nullStringIfEmpty(*p.RepoName),
			now,
			s.db.workspaceArg(),
		)
	} else {
		const q = `UPDATE projects
			SET title = ?2, description = ?3, area = ?4, priority = ?5, status = ?6, goal_id = ?7, updated_at = ?8
			WHERE id = ?1
			  AND (?9 IS NULL OR workspace_id = ?9)`
		res, err = s.db.conn.ExecContext(
			ctx, q,
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
	}
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
// not exist in the configured workspace the call is a silent no-op (matching
// the pre-fix behaviour where a workspace-mismatched DELETE simply affected 0
// rows on the parent table). The pre-check ensures cleanup never touches
// another workspace's join rows or work_sessions; the parent DELETE's
// workspace filter is now redundant defence-in-depth. See
// gtd.DeleteTaskOrchestration (internal/gtd/deletetask_orchestration.go) for
// the shared control flow this delegates to.
func (s *GTDStore) DeleteTask(ctx context.Context, id uuid.UUID) error {
	if err := gtd.DeleteTaskOrchestration(ctx, id, &sqliteDeleteTaskAdapter{s: s, id: id}); err != nil {
		return fmt.Errorf("%w", err) // context already added by DeleteTaskOrchestration
	}
	return nil
}

// sqliteDeleteTaskAdapter is the SQLite-backed gtd.DeleteTaskAdapter used by
// GTDStore.DeleteTask. Constructed fresh per call; holds the open tx as
// state between the interface's method calls.
type sqliteDeleteTaskAdapter struct {
	s  *GTDStore
	id uuid.UUID
	tx *sql.Tx
}

func (a *sqliteDeleteTaskAdapter) BeginTx(ctx context.Context) error {
	tx, err := a.s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w", err) // context added one level up by DeleteTaskOrchestration
	}
	a.tx = tx
	return nil
}

func (a *sqliteDeleteTaskAdapter) WorkspacePrecheck(ctx context.Context) (bool, error) {
	var exists int
	row := a.tx.QueryRowContext(
		ctx,
		`SELECT EXISTS(
		    SELECT 1 FROM tasks
		     WHERE id = ?1
		       AND (?2 IS NULL OR workspace_id = ?2)
		 )`,
		a.id.String(), a.s.db.workspaceArg(),
	)
	if err := row.Scan(&exists); err != nil {
		return false, fmt.Errorf("%w", err) // context added one level up by DeleteTaskOrchestration
	}
	return exists != 0, nil
}

// CleanupWorkSessionTasks removes join-table rows (was ON DELETE CASCADE on
// work_session_tasks.task_id).
func (a *sqliteDeleteTaskAdapter) CleanupWorkSessionTasks(ctx context.Context) error {
	if _, err := a.tx.ExecContext(
		ctx,
		`DELETE FROM work_session_tasks WHERE task_id = ?1`, a.id.String(),
	); err != nil {
		return fmt.Errorf("%w", err) // context added one level up by DeleteTaskOrchestration
	}
	return nil
}

// NullifyWorkSessionsCurrentTask NULLs out work_sessions.current_task_id
// (was ON DELETE SET NULL).
func (a *sqliteDeleteTaskAdapter) NullifyWorkSessionsCurrentTask(ctx context.Context) error {
	if _, err := a.tx.ExecContext(
		ctx,
		`UPDATE work_sessions
		    SET current_task_id = NULL,
		        updated_at      = ?2
		  WHERE current_task_id = ?1`,
		a.id.String(), nowRFC3339(),
	); err != nil {
		return fmt.Errorf("%w", err) // context added one level up by DeleteTaskOrchestration
	}
	return nil
}

// CleanupCompletionCandidates removes completion_candidates rows
// referencing this task.
func (a *sqliteDeleteTaskAdapter) CleanupCompletionCandidates(ctx context.Context) error {
	if _, err := a.tx.ExecContext(
		ctx,
		`DELETE FROM completion_candidates WHERE task_id = ?1`, a.id.String(),
	); err != nil {
		return fmt.Errorf("%w", err) // context added one level up by DeleteTaskOrchestration
	}
	return nil
}

// ResetPromotedVisionItems resets vision_items that were promoted from this
// task.
func (a *sqliteDeleteTaskAdapter) ResetPromotedVisionItems(ctx context.Context) error {
	if _, err := a.tx.ExecContext(
		ctx,
		`UPDATE vision_items
		    SET promoted_task_id = NULL,
		        status           = 'open'
		  WHERE promoted_task_id = ?1`,
		a.id.String(),
	); err != nil {
		return fmt.Errorf("%w", err) // context added one level up by DeleteTaskOrchestration
	}
	return nil
}

// DeleteTaskRow deletes the task itself, scoped to the configured workspace.
func (a *sqliteDeleteTaskAdapter) DeleteTaskRow(ctx context.Context) error {
	if _, err := a.tx.ExecContext(
		ctx,
		`DELETE FROM tasks
		   WHERE id = ?1
		     AND (?2 IS NULL OR workspace_id = ?2)`,
		a.id.String(), a.s.db.workspaceArg(),
	); err != nil {
		return fmt.Errorf("%w", err) // context added one level up by DeleteTaskOrchestration
	}
	return nil
}

func (a *sqliteDeleteTaskAdapter) Commit(context.Context) error {
	if err := a.tx.Commit(); err != nil {
		return fmt.Errorf("%w", err) // context added one level up by DeleteTaskOrchestration
	}
	return nil
}

func (a *sqliteDeleteTaskAdapter) Rollback(context.Context) {
	_ = a.tx.Rollback()
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
	sinceStr := since.UTC().Format("2006-01-02T15:04:05.000Z07:00")
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

// LatestActivityAt returns the created_at of the most-recent activity_log row,
// workspace-scoped, or nil if the table is empty. SQLite parity with gtd.Store.LatestActivityAt.
func (s *GTDStore) LatestActivityAt(ctx context.Context) (*time.Time, error) {
	const q = `SELECT created_at FROM activity_log
		WHERE (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC LIMIT 1`
	var createdNS sql.NullString
	row := s.db.conn.QueryRowContext(ctx, q, s.db.workspaceArg())
	if err := row.Scan(&createdNS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // sentinel: empty table is not an error; callers render null timestamp
		}
		return nil, errWrap("LatestActivityAt", err)
	}
	ts := parseTimestamptz(createdNS)
	if !ts.Valid {
		return nil, nil //nolint:nilnil // sentinel: NULL timestamp in DB is not an error
	}
	t := ts.Time.UTC()
	return &t, nil
}

// sqliteAutomationActions is the set of action strings considered "automation"
// for SQLite's ListRecentAutomation. Must stay in sync with gtd.automationActions.
var sqliteAutomationActions = map[string]bool{
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
// Go-side by sqliteAutomationActions, workspace-scoped.
// Query: WHERE created_at >= NOW()-7d ORDER BY created_at DESC LIMIT 200,
// then filter in Go, then slice to limit.
func (s *GTDStore) ListRecentAutomation(ctx context.Context, limit int32) ([]db.ActivityLog, error) {
	since := time.Now().UTC().Add(-7 * 24 * time.Hour)
	sinceStr := since.Format("2006-01-02T15:04:05.000Z07:00")
	const q = `SELECT id, workspace_id, actor, project_id, action, notes, created_at
		FROM activity_log
		WHERE created_at >= ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC
		LIMIT 200`
	rows, err := s.db.conn.QueryContext(ctx, q, sinceStr, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("ListRecentAutomation", err)
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
			return nil, errWrap("ListRecentAutomation scan", err)
		}
		if id, err := uuid.Parse(idStr); err == nil {
			a.ID = id
		}
		a.WorkspaceID = pgtypeUUID(nsString(workspaceNS))
		a.ProjectID = pgtypeUUID(nsString(projectNS))
		a.Notes = pgtypeText(notesNS.String, notesNS.Valid)
		a.CreatedAt = parseTimestamptz(createdNS)
		if sqliteAutomationActions[a.Action] {
			out = append(out, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("ListRecentAutomation iter", err)
	}
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

// LatestActionAt returns the created_at of the most-recent activity_log row
// matching the given action, workspace-scoped, or nil if none found.
// SQLite parity with gtd.Store.LatestActionAt.
func (s *GTDStore) LatestActionAt(ctx context.Context, action string) (*time.Time, error) {
	const q = `SELECT created_at FROM activity_log
		WHERE action = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC LIMIT 1`
	var createdNS sql.NullString
	row := s.db.conn.QueryRowContext(ctx, q, action, s.db.workspaceArg())
	if err := row.Scan(&createdNS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // sentinel: no matching action row is not an error; callers render null timestamp
		}
		return nil, errWrap("LatestActionAt", err)
	}
	ts := parseTimestamptz(createdNS)
	if !ts.Valid {
		return nil, nil //nolint:nilnil // sentinel: NULL timestamp in DB is not an error
	}
	t := ts.Time.UTC()
	return &t, nil
}

// WeeklyProgress returns completed-this-week and total-week-relevant counts.
// total = tasks completed this week + pending/in_progress due this week or created this week.
func (s *GTDStore) WeeklyProgress(ctx context.Context) (completed, total int64, err error) {
	// SQLite has no date_trunc; compute Monday 00:00 UTC of this week in Go.
	now := time.Now().UTC()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7 // treat Sunday as end of week, ISO style
	}
	monday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Add(-time.Duration(weekday-1) * 24 * time.Hour)
	weekStart := monday.Format("2006-01-02T15:04:05.000Z07:00")
	weekEnd := monday.Add(7 * 24 * time.Hour).Format("2006-01-02T15:04:05.000Z07:00")

	const completedQ = `SELECT COUNT(*) FROM tasks
		WHERE status = 'completed'
		  AND updated_at >= ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`
	if err = s.db.conn.QueryRowContext(ctx, completedQ, weekStart, s.db.workspaceArg()).Scan(&completed); err != nil {
		return 0, 0, errWrap("WeeklyProgress completed", err)
	}

	const totalQ = `SELECT COUNT(*) FROM tasks
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND (
		    (status = 'completed' AND updated_at >= ?2 AND updated_at < ?3)
		    OR (status IN ('pending','in_progress')
		        AND ((due_date >= ?2 AND due_date < ?3)
		          OR created_at >= ?2))
		  )`
	if err = s.db.conn.QueryRowContext(ctx, totalQ, s.db.workspaceArg(), weekStart, weekEnd).Scan(&total); err != nil {
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

// sqliteWorkspaceFilter converts a workspaceID to the value used in SQLite
// queries: empty string → NULL (unscoped), otherwise the UUID string.
func sqliteWorkspaceFilter(workspaceID uuid.UUID) any {
	if workspaceID == (uuid.UUID{}) {
		return nil
	}
	return workspaceID.String()
}

// sqliteLoadChecklistTx reads the checklist TEXT column for taskID inside an
// existing *sql.Tx. Returns gtd.ErrNotFound when the row does not exist.
func sqliteLoadChecklistTx(ctx context.Context, tx *sql.Tx, taskID uuid.UUID, wsFilter any) ([]gtd.ChecklistItem, error) {
	const q = `SELECT checklist FROM tasks WHERE id = ?1 AND (?2 IS NULL OR workspace_id = ?2) LIMIT 1`
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, q, taskID.String(), wsFilter).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, gtd.ErrNotFound
		}
		return nil, errWrap("sqliteLoadChecklistTx", err)
	}
	var items []gtd.ChecklistItem
	if raw.Valid && raw.String != "" && raw.String != "[]" {
		if err := json.Unmarshal([]byte(raw.String), &items); err != nil {
			return nil, errWrap("sqliteLoadChecklistTx unmarshal", err)
		}
	}
	if items == nil {
		items = []gtd.ChecklistItem{}
	}
	return items, nil
}

// sqliteSaveChecklistTx serialises items and writes them back to the checklist
// column inside an existing *sql.Tx.
func sqliteSaveChecklistTx(ctx context.Context, tx *sql.Tx, taskID uuid.UUID, wsFilter any, items []gtd.ChecklistItem) error {
	data, err := json.Marshal(items)
	if err != nil {
		return errWrap("sqliteSaveChecklistTx marshal", err)
	}
	const q = `UPDATE tasks SET checklist = ?1, updated_at = ?2 WHERE id = ?3 AND (?4 IS NULL OR workspace_id = ?4)`
	if _, err := tx.ExecContext(ctx, q, string(data), nowRFC3339(), taskID.String(), wsFilter); err != nil {
		return errWrap("sqliteSaveChecklistTx", err)
	}
	return nil
}

// AddChecklistItem appends a new ChecklistItem (server-generated ID) to the
// task's checklist and returns the full updated slice.
// Returns gtd.ErrNotFound when no task matches taskID + workspaceID.
//
// The load+save pair runs inside a BEGIN IMMEDIATE transaction to prevent lost
// updates when concurrent callers modify the same task's checklist.
func (s *GTDStore) AddChecklistItem(
	ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID, item gtd.ChecklistItem,
) ([]gtd.ChecklistItem, error) {
	tx, err := s.db.conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, errWrap("AddChecklistItem begin", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	wsFilter := sqliteWorkspaceFilter(workspaceID)
	items, err := sqliteLoadChecklistTx(ctx, tx, taskID, wsFilter)
	if err != nil {
		return nil, err
	}
	items = append(items, item)
	if err := sqliteSaveChecklistTx(ctx, tx, taskID, wsFilter, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, errWrap("AddChecklistItem commit", err)
	}
	committed = true
	return items, nil
}

// UpdateChecklistItem applies a partial patch to the checklist item identified
// by itemID inside the given task. Returns the full updated checklist.
// Returns gtd.ErrNotFound when task or item is not found.
//
// The load+save pair runs inside a BEGIN IMMEDIATE transaction to prevent lost
// updates when concurrent callers modify the same task's checklist.
func (s *GTDStore) UpdateChecklistItem(
	ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID,
	itemID uuid.UUID, update gtd.UpdateChecklistItemParams,
) ([]gtd.ChecklistItem, error) {
	tx, err := s.db.conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, errWrap("UpdateChecklistItem begin", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	wsFilter := sqliteWorkspaceFilter(workspaceID)
	items, err := sqliteLoadChecklistTx(ctx, tx, taskID, wsFilter)
	if err != nil {
		return nil, err
	}
	// Delegate in-memory patch to the shared helper in the gtd package to
	// keep cyclomatic complexity of this method below the lint threshold.
	items, err = gtd.ApplyChecklistItemPatch(items, itemID, update)
	if err != nil {
		return nil, errWrap("UpdateChecklistItem patch", err)
	}
	if err := sqliteSaveChecklistTx(ctx, tx, taskID, wsFilter, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, errWrap("UpdateChecklistItem commit", err)
	}
	committed = true
	return items, nil
}

// DeleteChecklistItem removes the item identified by itemID from the task's
// checklist. Returns gtd.ErrNotFound when task or item is not found.
//
// The load+save pair runs inside a BEGIN IMMEDIATE transaction to prevent lost
// updates when concurrent callers modify the same task's checklist.
func (s *GTDStore) DeleteChecklistItem(ctx context.Context, taskID uuid.UUID, workspaceID uuid.UUID, itemID uuid.UUID) error {
	tx, err := s.db.conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return errWrap("DeleteChecklistItem begin", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	wsFilter := sqliteWorkspaceFilter(workspaceID)
	items, err := sqliteLoadChecklistTx(ctx, tx, taskID, wsFilter)
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
		return gtd.ErrNotFound
	}
	if err := sqliteSaveChecklistTx(ctx, tx, taskID, wsFilter, next); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errWrap("DeleteChecklistItem commit", err)
	}
	committed = true
	return nil
}
