package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
)

// WorkSessionStore is the SQLite-backed implementation of worksession.StoreIface.
type WorkSessionStore struct {
	db *DB
}

// NewWorkSessionStore wraps an open DB into a WorkSessionStore.
func NewWorkSessionStore(d *DB) *WorkSessionStore {
	return &WorkSessionStore{db: d}
}

var _ worksession.StoreIface = (*WorkSessionStore)(nil)

const workSessionSelectCols = `id, workspace_id, repo_name, project_id,
	title, goal, status, source, confirmed_plan_id, current_task_id,
	final_summary, started_at, last_checkpoint_at, completed_at,
	created_at, updated_at`

// nullStringToPtr returns a pointer to the NullString's value if valid and
// non-empty, or nil otherwise.
func nullStringToPtr(ns sql.NullString) *string {
	if ns.Valid && ns.String != "" {
		return &ns.String
	}
	return nil
}

// nullStringToUUIDPtr parses the NullString as a UUID pointer, returning nil
// if the string is empty or invalid.
func nullStringToUUIDPtr(ns sql.NullString) *uuid.UUID {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	id, err := uuid.Parse(ns.String)
	if err != nil {
		return nil
	}
	return &id
}

func scanWorkSession(scan func(...any) error) (*worksession.Session, error) {
	var (
		s                                                          worksession.Session
		idStr, wsIDStr, repoName, title, goal, status, source      string
		createdAt, updatedAt                                       string
		projectIDNS, confirmedPlanIDNS, currentTaskIDNS            sql.NullString
		finalSummaryNS, startedAtNS, lastCheckpointNS, completedNS sql.NullString
	)
	if err := scan(
		&idStr, &wsIDStr, &repoName, &projectIDNS,
		&title, &goal, &status, &source,
		&confirmedPlanIDNS, &currentTaskIDNS,
		&finalSummaryNS, &startedAtNS, &lastCheckpointNS, &completedNS,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	s.ID, _ = uuid.Parse(idStr)
	s.WorkspaceID, _ = uuid.Parse(wsIDStr)
	s.RepoName = repoName
	s.Title = title
	s.Goal = goal
	s.Status = status
	s.Source = source
	s.CreatedAt = createdAt
	s.UpdatedAt = updatedAt
	s.ProjectID = nullStringToUUIDPtr(projectIDNS)
	s.ConfirmedPlanID = nullStringToUUIDPtr(confirmedPlanIDNS)
	s.CurrentTaskID = nullStringToUUIDPtr(currentTaskIDNS)
	s.FinalSummary = nullStringToPtr(finalSummaryNS)
	s.StartedAt = nullStringToPtr(startedAtNS)
	s.LastCheckpointAt = nullStringToPtr(lastCheckpointNS)
	s.CompletedAt = nullStringToPtr(completedNS)
	return &s, nil
}

// Create inserts a new in_progress work session and links task_ids as primary.
func (s *WorkSessionStore) Create(ctx context.Context, p worksession.CreateParams) (*worksession.Session, error) {
	if p.RepoName == "" {
		return nil, fmt.Errorf("worksession.Create: repo_name is required")
	}
	if p.Title == "" {
		return nil, fmt.Errorf("worksession.Create: title is required")
	}
	if p.Goal == "" {
		return nil, fmt.Errorf("worksession.Create: goal is required")
	}
	if p.Source == "" {
		return nil, fmt.Errorf("worksession.Create: source is required")
	}

	wsID := p.WorkspaceID.String()
	if s.db.workspaceID != "" {
		wsID = s.db.workspaceID
	}

	id := uuid.New()

	firstTaskIDArg := any(nil)
	if len(p.TaskIDs) > 0 {
		firstTaskIDArg = p.TaskIDs[0].String()
	}

	tx, err := s.db.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("worksession.Create begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	const insertQ = `INSERT INTO work_sessions
		(id, workspace_id, repo_name, project_id, title, goal, status, source,
		 confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		VALUES (?1,?2,?3,?4,?5,?6,'in_progress',?7,?8,?9,?10,?11,?12)`

	now := nowRFC3339()
	_, err = tx.ExecContext(ctx, insertQ,
		id.String(), wsID, p.RepoName,
		nullStringFromUUID(p.ProjectID),
		p.Title, p.Goal, p.Source,
		nullStringFromUUID(p.ConfirmedPlanID),
		firstTaskIDArg,
		now, now, now,
	)
	if err != nil {
		if isUniqueViolationSQLite(err) {
			return nil, worksession.ErrAlreadyActive
		}
		return nil, fmt.Errorf("worksession.Create insert: %w", err)
	}

	// Link tasks with role=primary.
	for _, taskID := range p.TaskIDs {
		const linkQ = `INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
			VALUES (?1,?2,'primary',?3)
			ON CONFLICT (session_id, task_id) DO NOTHING`
		if _, e := tx.ExecContext(ctx, linkQ, id.String(), taskID.String(), now); e != nil {
			err = fmt.Errorf("worksession.Create link task %s: %w", taskID, e)
			return nil, err
		}
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("worksession.Create commit: %w", err)
	}

	return s.byID(ctx, id)
}

// GetActive returns the in_progress session for workspace+repo.
func (s *WorkSessionStore) GetActive(
	ctx context.Context, workspaceID uuid.UUID, repoName string,
) (*worksession.ActiveSessionResult, error) {
	// If the store has a server-level workspace configured and the caller
	// passes a different workspace UUID, deny access — the store only serves
	// its own workspace.
	if s.db.workspaceID != "" && s.db.workspaceID != workspaceID.String() {
		return &worksession.ActiveSessionResult{Active: false, ImplementationAllowed: false}, nil
	}
	ws := workspaceID.String()
	if s.db.workspaceID != "" {
		ws = s.db.workspaceID
	}

	const q = `SELECT ` + workSessionSelectCols + ` FROM work_sessions
		WHERE workspace_id = ?1
		  AND repo_name = ?2
		  AND status = 'in_progress'
		LIMIT 1`

	row := s.db.conn.QueryRowContext(ctx, q, ws, repoName)
	sess, err := scanWorkSession(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return &worksession.ActiveSessionResult{Active: false, ImplementationAllowed: false}, nil
	}
	if err != nil {
		return nil, errWrap("WorkSessionStore.GetActive", err)
	}

	tasks, err := s.LinkedTasks(ctx, sess.ID)
	if err != nil {
		return nil, errWrap("WorkSessionStore.GetActive linked tasks", err)
	}

	return &worksession.ActiveSessionResult{
		Active:                true,
		Session:               sess,
		LinkedTasks:           tasks,
		LastCheckpoint:        sess.LastCheckpointAt,
		ImplementationAllowed: true,
	}, nil
}

// Checkpoint sets status=checkpointed and updates last_checkpoint_at.
func (s *WorkSessionStore) Checkpoint(ctx context.Context, p worksession.CheckpointParams) (*worksession.Session, error) {
	ws := s.db.workspaceArg()

	const q = `UPDATE work_sessions
		SET status = 'checkpointed',
		    last_checkpoint_at = ?3,
		    updated_at = ?3
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		  AND status IN ('in_progress','checkpointed')`

	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q, p.SessionID.String(), ws, now)
	if err != nil {
		return nil, errWrap("WorkSessionStore.Checkpoint", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, worksession.ErrNotFound
	}
	return s.byID(ctx, p.SessionID)
}

// Finish sets status=completed and stores final_summary.
func (s *WorkSessionStore) Finish(ctx context.Context, p worksession.FinishParams) (*worksession.Session, error) {
	ws := s.db.workspaceArg()

	const q = `UPDATE work_sessions
		SET status = 'completed',
		    completed_at = ?3,
		    final_summary = ?4,
		    updated_at = ?3
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		  AND status IN ('in_progress','checkpointed')`

	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q,
		p.SessionID.String(), ws, now, nullStringIfEmpty(p.Summary))
	if err != nil {
		return nil, errWrap("WorkSessionStore.Finish", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, worksession.ErrNotFound
	}
	return s.byID(ctx, p.SessionID)
}

// GetByID returns the session scoped to workspaceID.
func (s *WorkSessionStore) GetByID(ctx context.Context, workspaceID, sessionID uuid.UUID) (*worksession.Session, error) {
	// If the store has a server-level workspace configured and the caller
	// passes a different workspace UUID, deny access.
	if s.db.workspaceID != "" && s.db.workspaceID != workspaceID.String() {
		return nil, worksession.ErrNotFound
	}
	ws := workspaceID.String()
	if s.db.workspaceID != "" {
		ws = s.db.workspaceID
	}

	const q = `SELECT ` + workSessionSelectCols + ` FROM work_sessions
		WHERE id = ?1 AND workspace_id = ?2 LIMIT 1`

	row := s.db.conn.QueryRowContext(ctx, q, sessionID.String(), ws)
	sess, err := scanWorkSession(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, worksession.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("WorkSessionStore.GetByID", err)
	}
	return sess, nil
}

// LinkTask attaches a task to a session with the given role.
func (s *WorkSessionStore) LinkTask(ctx context.Context, sessionID, taskID uuid.UUID, role string) error {
	const q = `INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		VALUES (?1,?2,?3,?4)
		ON CONFLICT (session_id, task_id) DO NOTHING`
	if _, err := s.db.conn.ExecContext(ctx, q,
		sessionID.String(), taskID.String(), role, nowRFC3339()); err != nil {
		return errWrap("WorkSessionStore.LinkTask", err)
	}
	return nil
}

// LinkedTasks returns all task links for the given session.
func (s *WorkSessionStore) LinkedTasks(ctx context.Context, sessionID uuid.UUID) ([]worksession.SessionTask, error) {
	const q = `SELECT session_id, task_id, role, created_at
		FROM work_session_tasks WHERE session_id = ?1 ORDER BY created_at`

	rows, err := s.db.conn.QueryContext(ctx, q, sessionID.String())
	if err != nil {
		return nil, errWrap("WorkSessionStore.LinkedTasks", err)
	}
	defer func() { _ = rows.Close() }()

	var out []worksession.SessionTask
	for rows.Next() {
		var sessIDStr, taskIDStr, role, createdAt string
		if err := rows.Scan(&sessIDStr, &taskIDStr, &role, &createdAt); err != nil {
			return nil, errWrap("WorkSessionStore.LinkedTasks scan", err)
		}
		st := worksession.SessionTask{Role: role, CreatedAt: createdAt}
		st.SessionID, _ = uuid.Parse(sessIDStr)
		st.TaskID, _ = uuid.Parse(taskIDStr)
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("WorkSessionStore.LinkedTasks iter", err)
	}
	return out, nil
}

// byID is a helper to fetch a session by its primary key, scoped to the
// store's workspace (if configured). Called after insert to return the
// created session — workspace constraint was already enforced by the INSERT.
func (s *WorkSessionStore) byID(ctx context.Context, id uuid.UUID) (*worksession.Session, error) {
	ws := s.db.workspaceArg()
	const q = `SELECT ` + workSessionSelectCols + ` FROM work_sessions
		WHERE id = ?1 AND (?2 IS NULL OR workspace_id = ?2) LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String(), ws)
	sess, err := scanWorkSession(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, worksession.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("WorkSessionStore.byID", err)
	}
	return sess, nil
}

func isUniqueViolationSQLite(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "idx_work_sessions_one_active")
}
