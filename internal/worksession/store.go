package worksession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// validateCreateParams returns an error if any required field is missing.
func validateCreateParams(p CreateParams) error {
	switch {
	case p.RepoName == "":
		return fmt.Errorf("worksession.Create: repo_name is required")
	case p.Title == "":
		return fmt.Errorf("worksession.Create: title is required")
	case p.Goal == "":
		return fmt.Errorf("worksession.Create: goal is required")
	case p.Source == "":
		return fmt.Errorf("worksession.Create: source is required")
	}
	return nil
}

// Create inserts a new in_progress work session and links task_ids as primary.
// After the session tx commits, linked tasks are batch-updated from
// status='pending' to 'in_progress' (idempotent — tasks already in_progress
// are untouched). Workspace boundary is enforced by the WHERE clause.
func (s *Store) Create(ctx context.Context, p CreateParams) (*Session, error) {
	if err := validateCreateParams(p); err != nil {
		return nil, err
	}

	// Workspace scoping: always use the store-configured workspace, never
	// trust the value from tool input.
	wsID := p.WorkspaceID
	if s.workspaceID != nil {
		wsID = *s.workspaceID
	}

	// SECURITY: task_ids are validated against the workspace boundary in
	// batchMarkTasksInProgress via the WHERE workspace_id clause. Only tasks
	// that belong to wsID (or any workspace when wsID is nil/zero) are updated.

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

	// Batch-update linked tasks to in_progress after the session tx commits.
	// Non-fatal: session is committed; task status update is best-effort.
	if len(p.TaskIDs) > 0 {
		_ = s.batchMarkTasksInProgress(ctx, wsID, p.TaskIDs)
	}

	return sess, nil
}

// batchMarkTasksInProgress sets status='in_progress' on tasks that are
// currently 'pending'. It is idempotent: tasks already in_progress are
// untouched. Workspace boundary is enforced by the WHERE clause so a
// compromised caller cannot update tasks belonging to a different workspace.
func (s *Store) batchMarkTasksInProgress(ctx context.Context, wsID uuid.UUID, taskIDs []uuid.UUID) error {
	if len(taskIDs) == 0 {
		return nil
	}
	// Build $N placeholders for the IN clause.
	// Use $2 for workspace and $3…$N for task IDs.
	idArgs := make([]any, 0, len(taskIDs)+1)
	idArgs = append(idArgs, wsID)
	placeholders := make([]string, len(taskIDs))
	for i, id := range taskIDs {
		idArgs = append(idArgs, id)
		placeholders[i] = fmt.Sprintf("$%d", i+2)
	}
	q := fmt.Sprintf(`UPDATE tasks
		SET status = 'in_progress', updated_at = NOW()
		WHERE id IN (%s)
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND status = 'pending'`,
		strings.Join(placeholders, ","))
	_, err := s.pool.Exec(ctx, q, idArgs...)
	if err != nil {
		return fmt.Errorf("worksession.batchMarkTasksInProgress: %w", err)
	}
	return nil
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
		if errors.Is(err, pgx.ErrNoRows) {
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
	var ws uuid.UUID
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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("worksession.Checkpoint: %w", err)
	}
	return sess, nil
}

// Finish sets status=completed and records final_summary. After the session
// update, linked tasks are batch-marked as completed:
//   - If FinishParams.CompletedTaskIDs is non-empty, only those tasks are marked.
//   - Otherwise, all tasks linked via work_session_tasks are marked completed.
func (s *Store) Finish(ctx context.Context, p FinishParams) (*Session, error) {
	var ws uuid.UUID
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
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("worksession.Finish: %w", err)
	}

	// Resolve which tasks to mark completed.
	taskIDs := p.CompletedTaskIDs
	if len(taskIDs) == 0 {
		// Fallback: find all linked tasks for this session.
		linked, lErr := s.LinkedTasks(ctx, p.SessionID)
		if lErr == nil {
			taskIDs = make([]uuid.UUID, 0, len(linked))
			for _, lt := range linked {
				taskIDs = append(taskIDs, lt.TaskID)
			}
		}
	}

	if len(taskIDs) > 0 {
		if markErr := s.batchMarkTasksCompleted(ctx, ws, taskIDs, p.Artifact); markErr != nil {
			// Non-fatal: session is committed; task status update is best-effort.
			_ = markErr
		}
	}

	return sess, nil
}

// batchMarkTasksCompleted sets status='completed' and optionally artifact on
// tasks that belong to the workspace. Workspace boundary enforced by WHERE.
func (s *Store) batchMarkTasksCompleted(ctx context.Context, wsID uuid.UUID, taskIDs []uuid.UUID, artifact *string) error {
	if len(taskIDs) == 0 {
		return nil
	}
	var artVal any
	if artifact != nil {
		artVal = *artifact
	}
	// Workspace arg is $1, artifact is $2, task IDs start at $3.
	idArgs := make([]any, 0, len(taskIDs)+2)
	idArgs = append(idArgs, wsID, artVal)
	placeholders := make([]string, len(taskIDs))
	for i, id := range taskIDs {
		idArgs = append(idArgs, id)
		placeholders[i] = fmt.Sprintf("$%d", i+3)
	}
	q := fmt.Sprintf(`UPDATE tasks
		SET status = 'completed', artifact = COALESCE($2, artifact), updated_at = NOW()
		WHERE id IN (%s)
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND status NOT IN ('completed','cancelled')`,
		strings.Join(placeholders, ","))
	_, err := s.pool.Exec(ctx, q, idArgs...)
	if err != nil {
		return fmt.Errorf("worksession.batchMarkTasksCompleted: %w", err)
	}
	return nil
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
		if errors.Is(err, pgx.ErrNoRows) {
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
	// Use errors.As to unwrap pgconn.PgError and check the SQL state code
	// "23505" (unique_violation). This is the idiomatic pgx v5 pattern
	// (mirrors internal/gtd/store.go:121).
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
