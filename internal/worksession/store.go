package worksession

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed implementation of StoreIface.
//
// All queries are hand-written (no sqlc) because the work_sessions schema was
// introduced after the sqlc generation pass and adding it to sqlc.yaml is a
// separate concern.
type Store struct {
	pool        *pgxpool.Pool
	workspaceID *uuid.UUID
}

// NewStore returns a Postgres-backed Store.
func NewStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{pool: pool, workspaceID: workspaceID}
}

var _ StoreIface = (*Store)(nil)

// sessionSelectReturning is the RETURNING clause (or SELECT cols) that scans
// all columns as text so we avoid pgtype dependency in this package.
const sessionSelectReturning = `id::text, workspace_id::text, repo_name,
	project_id::text, title, goal, status, source,
	confirmed_plan_id::text, current_task_id::text, final_summary,
	to_char(started_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
	to_char(last_checkpoint_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
	to_char(completed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
	to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
	to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`

func scanSessionFromRow(row pgx.Row) (*Session, error) {
	var s Session
	var idStr, wsIDStr, repoName, title, goal, status, source, createdAt, updatedAt string
	var projectIDStr, confirmedPlanIDStr, currentTaskIDStr *string
	var finalSummary, startedAt, lastCheckpointAt, completedAt *string

	err := row.Scan(
		&idStr, &wsIDStr, &repoName,
		&projectIDStr, &title, &goal, &status, &source,
		&confirmedPlanIDStr, &currentTaskIDStr, &finalSummary,
		&startedAt, &lastCheckpointAt, &completedAt,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan session row: %w", err)
	}
	s.ID, _ = uuid.Parse(idStr)
	s.WorkspaceID, _ = uuid.Parse(wsIDStr)
	s.RepoName = repoName
	s.Title = title
	s.Goal = goal
	s.Status = status
	s.Source = source
	s.FinalSummary = finalSummary
	s.StartedAt = startedAt
	s.LastCheckpointAt = lastCheckpointAt
	s.CompletedAt = completedAt
	s.CreatedAt = createdAt
	s.UpdatedAt = updatedAt
	if projectIDStr != nil {
		if id, e := uuid.Parse(*projectIDStr); e == nil {
			s.ProjectID = &id
		}
	}
	if confirmedPlanIDStr != nil {
		if id, e := uuid.Parse(*confirmedPlanIDStr); e == nil {
			s.ConfirmedPlanID = &id
		}
	}
	if currentTaskIDStr != nil {
		if id, e := uuid.Parse(*currentTaskIDStr); e == nil {
			s.CurrentTaskID = &id
		}
	}
	return &s, nil
}

func linkTaskTx(ctx context.Context, tx pgx.Tx, sessionID, taskID uuid.UUID, role string) error {
	const q = `INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		VALUES ($1,$2,$3,NOW())
		ON CONFLICT (session_id, task_id) DO NOTHING`
	_, err := tx.Exec(ctx, q, sessionID, taskID, role)
	if err != nil {
		return fmt.Errorf("link task: %w", err)
	}
	return nil
}

// Create inserts a new in_progress work session and links task_ids as primary.
func (s *Store) Create(ctx context.Context, p CreateParams) (*Session, error) {
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

	// Workspace scoping: always use the store-configured workspace, never
	// trust the value from tool input.
	wsID := p.WorkspaceID
	if s.workspaceID != nil {
		wsID = *s.workspaceID
	}

	id := uuid.New()
	now := time.Now().UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("worksession.Create begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	const insertQ = `
		INSERT INTO work_sessions
			(id, workspace_id, repo_name, project_id, title, goal, status, source,
			 confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,'in_progress',$7,$8,$9,$10,$11,$12)
		RETURNING ` + sessionSelectReturning

	var currentTaskArg any
	if len(p.TaskIDs) > 0 {
		currentTaskArg = p.TaskIDs[0]
	}

	row := tx.QueryRow(ctx, insertQ,
		id, wsID, p.RepoName,
		uuidOrNil(p.ProjectID),
		p.Title, p.Goal, p.Source,
		uuidOrNil(p.ConfirmedPlanID),
		currentTaskArg,
		now, now, now,
	)

	var sess *Session
	sess, err = scanSessionFromRow(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyActive
		}
		return nil, fmt.Errorf("worksession.Create insert: %w", err)
	}

	// Link tasks with role=primary.
	for _, taskID := range p.TaskIDs {
		if err = linkTaskTx(ctx, tx, id, taskID, "primary"); err != nil {
			return nil, fmt.Errorf("worksession.Create link task %s: %w", taskID, err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("worksession.Create commit: %w", err)
	}
	return sess, nil
}

// GetActive returns the in_progress session for workspace+repo.
func (s *Store) GetActive(ctx context.Context, workspaceID uuid.UUID, repoName string) (*ActiveSessionResult, error) {
	ws := workspaceID
	if s.workspaceID != nil {
		ws = *s.workspaceID
	}

	const q = `SELECT ` + sessionSelectReturning + `
		FROM work_sessions
		WHERE workspace_id = $1
		  AND repo_name = $2
		  AND status = 'in_progress'
		LIMIT 1`

	row := s.pool.QueryRow(ctx, q, ws, repoName)
	sess, err := scanSessionFromRow(row)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return &ActiveSessionResult{Active: false, ImplementationAllowed: false}, nil
		}
		return nil, fmt.Errorf("worksession.GetActive: %w", err)
	}

	tasks, err := s.LinkedTasks(ctx, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("worksession.GetActive linked tasks: %w", err)
	}

	return &ActiveSessionResult{
		Active:                true,
		Session:               sess,
		LinkedTasks:           tasks,
		LastCheckpoint:        sess.LastCheckpointAt,
		ImplementationAllowed: true,
	}, nil
}

// Checkpoint updates the session to status=checkpointed and records last_checkpoint_at.
func (s *Store) Checkpoint(ctx context.Context, p CheckpointParams) (*Session, error) {
	ws := p.SessionID // placeholder; actual workspace from store config
	if s.workspaceID != nil {
		ws = *s.workspaceID
	}

	const q = `UPDATE work_sessions
		SET status = 'checkpointed',
		    last_checkpoint_at = NOW(),
		    updated_at = NOW()
		WHERE id = $1
		  AND workspace_id = $2
		  AND status IN ('in_progress','checkpointed')
		RETURNING ` + sessionSelectReturning

	row := s.pool.QueryRow(ctx, q, p.SessionID, ws)
	sess, err := scanSessionFromRow(row)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("worksession.Checkpoint: %w", err)
	}
	return sess, nil
}

// Finish sets status=completed and records final_summary.
func (s *Store) Finish(ctx context.Context, p FinishParams) (*Session, error) {
	ws := p.SessionID // placeholder; actual workspace from store config
	if s.workspaceID != nil {
		ws = *s.workspaceID
	}

	const q = `UPDATE work_sessions
		SET status = 'completed',
		    completed_at = NOW(),
		    final_summary = $3,
		    updated_at = NOW()
		WHERE id = $1
		  AND workspace_id = $2
		  AND status IN ('in_progress','checkpointed')
		RETURNING ` + sessionSelectReturning

	row := s.pool.QueryRow(ctx, q, p.SessionID, ws, p.Summary)
	sess, err := scanSessionFromRow(row)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("worksession.Finish: %w", err)
	}
	return sess, nil
}

// GetByID returns the session with the given ID, scoped to workspaceID.
func (s *Store) GetByID(ctx context.Context, workspaceID, sessionID uuid.UUID) (*Session, error) {
	ws := workspaceID
	if s.workspaceID != nil {
		ws = *s.workspaceID
	}

	const q = `SELECT ` + sessionSelectReturning + `
		FROM work_sessions
		WHERE id = $1 AND workspace_id = $2`

	row := s.pool.QueryRow(ctx, q, sessionID, ws)
	sess, err := scanSessionFromRow(row)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("worksession.GetByID: %w", err)
	}
	return sess, nil
}

// LinkTask attaches a task to a session with the given role.
func (s *Store) LinkTask(ctx context.Context, sessionID, taskID uuid.UUID, role string) error {
	const q = `INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		VALUES ($1,$2,$3,NOW())
		ON CONFLICT (session_id, task_id) DO NOTHING`
	if _, err := s.pool.Exec(ctx, q, sessionID, taskID, role); err != nil {
		return fmt.Errorf("worksession.LinkTask: %w", err)
	}
	return nil
}

// LinkedTasks returns all task links for the given session.
func (s *Store) LinkedTasks(ctx context.Context, sessionID uuid.UUID) ([]SessionTask, error) {
	const q = `SELECT session_id::text, task_id::text, role,
		to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')
		FROM work_session_tasks WHERE session_id = $1 ORDER BY created_at`
	rows, err := s.pool.Query(ctx, q, sessionID)
	if err != nil {
		return nil, fmt.Errorf("worksession.LinkedTasks: %w", err)
	}
	defer rows.Close()

	var out []SessionTask
	for rows.Next() {
		var sessIDStr, taskIDStr, role, createdAt string
		if err := rows.Scan(&sessIDStr, &taskIDStr, &role, &createdAt); err != nil {
			return nil, fmt.Errorf("worksession.LinkedTasks scan: %w", err)
		}
		st := SessionTask{Role: role, CreatedAt: createdAt}
		st.SessionID, _ = uuid.Parse(sessIDStr)
		st.TaskID, _ = uuid.Parse(taskIDStr)
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("worksession.LinkedTasks iter: %w", err)
	}
	return out, nil
}

// ---- helpers ----

func uuidOrNil(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// pgx wraps the error; check code "23505" (unique_violation).
	errMsg := err.Error()
	return strings.Contains(errMsg, "23505") ||
		strings.Contains(errMsg, "idx_work_sessions_one_active")
}
