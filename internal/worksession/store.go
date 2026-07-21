package worksession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
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
	if workspaceID == nil {
		slog.Warn("worksession.Store: no WORKSPACE_ID configured — operating in legacy mode, task batch updates scoped to zero-UUID workspace")
	}
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
	to_char(updated_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
	context_pack_id::text, verification_status, verification_command,
	verification_output_excerpt, outcome_id::text, final_result, branch_name`

func scanSessionFromRow(row pgx.Row) (*Session, error) {
	var s Session
	var idStr, wsIDStr, repoName, title, goal, status, source, createdAt, updatedAt string
	var projectIDStr, confirmedPlanIDStr, currentTaskIDStr *string
	var finalSummary, startedAt, lastCheckpointAt, completedAt *string
	var contextPackIDStr, outcomeIDStr *string
	var verificationStatus, verificationCommand, verificationOutputExcerpt, finalResult, branchName *string

	err := row.Scan(
		&idStr, &wsIDStr, &repoName,
		&projectIDStr, &title, &goal, &status, &source,
		&confirmedPlanIDStr, &currentTaskIDStr, &finalSummary,
		&startedAt, &lastCheckpointAt, &completedAt,
		&createdAt, &updatedAt,
		&contextPackIDStr, &verificationStatus, &verificationCommand,
		&verificationOutputExcerpt, &outcomeIDStr, &finalResult, &branchName,
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
	s.VerificationStatus = verificationStatus
	s.VerificationCommand = verificationCommand
	s.VerificationOutputExcerpt = verificationOutputExcerpt
	s.FinalResult = finalResult
	s.BranchName = branchName
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
	if contextPackIDStr != nil {
		if id, e := uuid.Parse(*contextPackIDStr); e == nil {
			s.ContextPackID = &id
		}
	}
	if outcomeIDStr != nil {
		if id, e := uuid.Parse(*outcomeIDStr); e == nil {
			s.OutcomeID = &id
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
	if p.BranchName != nil {
		if reason := CheckControlChars("branch_name", *p.BranchName); reason != "" {
			return fmt.Errorf("worksession.Create: %s", reason)
		}
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

	// P6.8: resolve p.Assignee through gtd.NormalizeActor before it can reach
	// batchMarkTasksInProgress's stamping below — defense in depth alongside
	// the MCP-layer validation in tools_worksession.go/tools_plan.go (mirrors
	// gtd.Store.CreateTask/UpdateTask re-validating despite the MCP layer
	// already having validated on the way in). Empty stays empty.
	sessionAssignee := strings.TrimSpace(p.Assignee)
	if sessionAssignee != "" {
		normalized, err := gtd.NormalizeActor(sessionAssignee)
		if err != nil {
			return nil, fmt.Errorf("worksession.Create: %w", err)
		}
		sessionAssignee = normalized
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
			 confirmed_plan_id, current_task_id, started_at, created_at, updated_at, branch_name)
		VALUES ($1,$2,$3,$4,$5,$6,'in_progress',$7,$8,$9,$10,$11,$12,$13)
		RETURNING ` + sessionSelectReturning

	var currentTaskArg any
	if len(p.TaskIDs) > 0 {
		currentTaskArg = p.TaskIDs[0]
	}

	row := tx.QueryRow(
		ctx, insertQ,
		id, wsID, p.RepoName,
		uuidOrNil(p.ProjectID),
		p.Title, p.Goal, p.Source,
		uuidOrNil(p.ConfirmedPlanID),
		currentTaskArg,
		now, now, now,
		strOrNil(p.BranchName),
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
	// Non-fatal: session is committed; task status update is best-effort —
	// but the error is still observed and logged (P6.8 fix: this used to be
	// `_ = s.batchMarkTasksInProgress(...)`, silently discarding real
	// execution errors such as a lost DB connection; now mirrors the SQLite
	// store's Create, which already logged this error correctly).
	if len(p.TaskIDs) > 0 {
		if err := s.batchMarkTasksInProgress(ctx, wsID, p.TaskIDs, sessionAssignee); err != nil {
			slog.Warn("worksession.Create: batchMarkTasksInProgress failed (non-fatal)",
				"session_id", id, "err", err)
		}
	}

	return sess, nil
}

// batchMarkTasksInProgress sets status='in_progress' on tasks that are
// currently 'pending'. It is idempotent: tasks already in_progress are
// untouched. Workspace boundary is enforced by the WHERE clause so a
// compromised caller cannot update tasks belonging to a different workspace.
//
// P6.8 assignee gate: sessionAssignee (already gtd.NormalizeActor'd by Create,
// or "") is stamped onto a task's assignee column ONLY when that task
// currently has none — COALESCE(NULLIF(assignee,”), NULLIF($2,”)) keeps an
// existing assignee untouched and falls back to sessionAssignee otherwise.
// The WHERE clause's trailing predicate excludes any row that would end up
// with neither (task has no assignee AND sessionAssignee is empty) — that
// task_id stays 'pending' instead of flipping to an ownerless in_progress
// row. TRIM(assignee) matches gtd.RequireAssigneeForInProgress's
// strings.TrimSpace, so a whitespace-only assignee (persistable via the HTTP
// CreateTask path, which skips NormalizeActor when TrimSpace is empty) is
// treated as empty here too — no divergence (P6.8 round-3 security fix).
// This is the batch-update equivalent of
// gtd.RequireAssigneeForInProgress: reject the transition rather than
// silently allow it, applied per-row since this is a bulk operation across
// potentially many task_ids with different existing-assignee states.
func (s *Store) batchMarkTasksInProgress(ctx context.Context, wsID uuid.UUID, taskIDs []uuid.UUID, sessionAssignee string) error {
	if len(taskIDs) == 0 {
		return nil
	}
	// Build $N placeholders for the IN clause.
	// $1 = workspace_id, $2 = session assignee, $3…$N+2 = task IDs.
	idArgs := make([]any, 0, len(taskIDs)+2)
	idArgs = append(idArgs, wsID, sessionAssignee)
	placeholders := make([]string, len(taskIDs))
	for i, id := range taskIDs {
		idArgs = append(idArgs, id)
		placeholders[i] = fmt.Sprintf("$%d", i+3)
	}
	q := fmt.Sprintf(`UPDATE tasks
		SET status = 'in_progress',
		    assignee = COALESCE(NULLIF(TRIM(assignee), ''), NULLIF($2, '')),
		    updated_at = NOW()
		WHERE id IN (%s)
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND status = 'pending'
		  AND (NULLIF(TRIM(assignee), '') IS NOT NULL OR NULLIF($2, '') IS NOT NULL)`,
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
//
// wbt-2.0 P2.2: also persists VerificationStatus/VerificationCommand/
// VerificationOutputExcerpt/FinalResult/OutcomeID and, when p.Evidence is
// non-empty, inserts each evidence row. Evidence insertion is best-effort
// (non-fatal on error) — the session UPDATE has already committed by then.
func (s *Store) Finish(ctx context.Context, p FinishParams) (*Session, error) {
	var ws uuid.UUID
	if s.workspaceID != nil {
		ws = *s.workspaceID
	}

	if p.VerificationCommand != nil {
		if reason := CheckControlChars("verification_command", *p.VerificationCommand); reason != "" {
			return nil, fmt.Errorf("worksession.Finish: %s", reason)
		}
	}

	var verificationExcerptArg any
	if p.VerificationOutputExcerpt != nil {
		verificationExcerptArg = RedactAndCapOutputExcerpt(*p.VerificationOutputExcerpt)
	}

	const q = `UPDATE work_sessions
		SET status = 'completed',
		    completed_at = NOW(),
		    final_summary = $3,
		    verification_status = $4,
		    verification_command = $5,
		    verification_output_excerpt = $6,
		    final_result = $7,
		    outcome_id = $8,
		    updated_at = NOW()
		WHERE id = $1
		  AND workspace_id = $2
		  AND status IN ('in_progress','checkpointed')
		RETURNING ` + sessionSelectReturning

	row := s.pool.QueryRow(ctx, q, p.SessionID, ws, p.Summary,
		strOrNil(p.VerificationStatus), strOrNil(p.VerificationCommand),
		verificationExcerptArg, strOrNil(p.FinalResult), uuidOrNil(p.OutcomeID))
	sess, err := scanSessionFromRow(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("worksession.Finish: %w", err)
	}

	// Resolve which tasks to mark completed. deferred_task_ids always wins
	// over completed_task_ids (and over the empty-completed fallback below) —
	// see ResolveCompletedTaskIDs (wbt-2.0 P2 review F2).
	var linkedIDs []uuid.UUID
	if len(p.CompletedTaskIDs) == 0 {
		// Fallback: find all linked tasks for this session.
		linked, lErr := s.LinkedTasks(ctx, p.SessionID)
		if lErr == nil {
			linkedIDs = make([]uuid.UUID, 0, len(linked))
			for _, lt := range linked {
				linkedIDs = append(linkedIDs, lt.TaskID)
			}
		}
	}
	taskIDs := ResolveCompletedTaskIDs(p.CompletedTaskIDs, p.DeferredTaskIDs, linkedIDs)

	if len(taskIDs) > 0 {
		if markErr := s.batchMarkTasksCompleted(ctx, ws, taskIDs, p.Artifact); markErr != nil {
			// Non-fatal: session is committed; task status update is best-effort.
			_ = markErr
		}
	}

	for _, ei := range p.Evidence {
		wsCopy := ws
		_, evErr := s.AddEvidence(ctx, Evidence{
			WorkspaceID:   &wsCopy,
			SessionID:     p.SessionID,
			EvidenceType:  ei.EvidenceType,
			Status:        ei.Status,
			Command:       ei.Command,
			Artifact:      ei.Artifact,
			OutputExcerpt: ei.OutputExcerpt,
		})
		if evErr != nil {
			// Non-fatal: session Finish already committed; evidence insertion
			// is best-effort audit data (mirrors batchMarkTasksCompleted above).
			slog.Warn("worksession.Finish: failed to add evidence (non-fatal)",
				"session_id", p.SessionID, "evidence_type", ei.EvidenceType, "err", evErr)
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

// ListRecent returns sessions ordered by created_at DESC, optionally
// filtered by repoName (empty = no filter). limit is hard-capped at
// MaxListRecentLimit (100) regardless of the caller-requested value.
func (s *Store) ListRecent(ctx context.Context, workspaceID uuid.UUID, repoName string, limit int) ([]Session, error) {
	ws := workspaceID
	if s.workspaceID != nil {
		ws = *s.workspaceID
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > MaxListRecentLimit {
		limit = MaxListRecentLimit
	}
	var repoArg any
	if repoName != "" {
		repoArg = repoName
	}

	const q = `SELECT ` + sessionSelectReturning + `
		FROM work_sessions
		WHERE workspace_id = $1
		  AND ($2::text IS NULL OR repo_name = $2)
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, ws, repoArg, limit)
	if err != nil {
		return nil, fmt.Errorf("worksession.ListRecent: %w", err)
	}
	defer rows.Close()

	var out []Session
	for rows.Next() {
		sess, err := scanSessionFromRow(rows)
		if err != nil {
			return nil, fmt.Errorf("worksession.ListRecent scan: %w", err)
		}
		out = append(out, *sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("worksession.ListRecent iter: %w", err)
	}
	return out, nil
}

// evidenceSelectReturning is the RETURNING clause (or SELECT cols) for
// work_session_evidence, scanned as text to avoid a pgtype dependency.
const evidenceSelectReturning = `id::text, workspace_id::text, session_id::text,
	evidence_type, status, command, artifact, output_excerpt,
	to_char(created_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`

func scanEvidenceFromRow(row pgx.Row) (*Evidence, error) {
	var e Evidence
	var idStr, sessIDStr, evidenceType, status, createdAt string
	var wsIDStr, command, artifact, outputExcerpt *string

	if err := row.Scan(&idStr, &wsIDStr, &sessIDStr, &evidenceType, &status,
		&command, &artifact, &outputExcerpt, &createdAt); err != nil {
		return nil, fmt.Errorf("scan evidence row: %w", err)
	}
	e.ID, _ = uuid.Parse(idStr)
	e.SessionID, _ = uuid.Parse(sessIDStr)
	e.EvidenceType = evidenceType
	e.Status = status
	e.Command = command
	e.Artifact = artifact
	e.OutputExcerpt = outputExcerpt
	e.CreatedAt = createdAt
	if wsIDStr != nil {
		if id, err := uuid.Parse(*wsIDStr); err == nil {
			e.WorkspaceID = &id
		}
	}
	return &e, nil
}

// AddEvidence inserts one evidence row and returns the persisted record.
// ev.OutputExcerpt goes through RedactAndCapOutputExcerpt (redact.ForLLM
// THEN rune-cap — never the reverse). ev.Command is validated with
// CheckControlChars (single-line, no shell-injection-prone newlines).
func (s *Store) AddEvidence(ctx context.Context, ev Evidence) (*Evidence, error) {
	if ev.Command != nil {
		if reason := CheckControlChars("command", *ev.Command); reason != "" {
			return nil, fmt.Errorf("worksession.AddEvidence: %s", reason)
		}
	}

	ws := ev.WorkspaceID
	if s.workspaceID != nil {
		ws = s.workspaceID
	}

	var excerptArg any
	if ev.OutputExcerpt != nil {
		excerptArg = RedactAndCapOutputExcerpt(*ev.OutputExcerpt)
	}

	const q = `INSERT INTO work_session_evidence
		(id, workspace_id, session_id, evidence_type, status, command, artifact, output_excerpt, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING ` + evidenceSelectReturning

	id := uuid.New()
	row := s.pool.QueryRow(ctx, q, id, uuidOrNil(ws), ev.SessionID, ev.EvidenceType, ev.Status,
		strOrNil(ev.Command), strOrNil(ev.Artifact), excerptArg, time.Now().UTC())
	e, err := scanEvidenceFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("worksession.AddEvidence: %w", err)
	}
	return e, nil
}

// GetEvidence returns all work_session_evidence rows for sessionID, scoped to
// the store's configured workspace (wbt-2.0 P2 review F5 — mirrors GetByID's
// workspace predicate; defence in depth alongside the caller-side GetByID
// gate that handleGetWorkSessionTrace already performs before calling this).
// AddEvidence (called only from within Finish, after Finish's own
// workspace-scoped UPDATE has matched a row) always stamps workspace_id with
// this same resolved value, so the predicate here never excludes rows
// written through the normal Finish path. Ordered by created_at ASC. Returns
// an empty (non-nil) slice when no rows exist.
func (s *Store) GetEvidence(ctx context.Context, sessionID uuid.UUID) ([]Evidence, error) {
	var ws uuid.UUID
	if s.workspaceID != nil {
		ws = *s.workspaceID
	}

	const q = `SELECT ` + evidenceSelectReturning + `
		FROM work_session_evidence WHERE workspace_id = $1 AND session_id = $2
		ORDER BY created_at ASC LIMIT $3`

	rows, err := s.pool.Query(ctx, q, ws, sessionID, MaxEvidenceListLimit)
	if err != nil {
		return nil, fmt.Errorf("worksession.GetEvidence: %w", err)
	}
	defer rows.Close()

	out := []Evidence{}
	for rows.Next() {
		e, err := scanEvidenceFromRow(rows)
		if err != nil {
			return nil, fmt.Errorf("worksession.GetEvidence scan: %w", err)
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("worksession.GetEvidence iter: %w", err)
	}
	return out, nil
}

// SetOutcomeLink sets work_sessions.outcome_id for sessionID, scoped to the
// store's workspace. Returns ErrNotFound when sessionID does not exist in
// that workspace.
func (s *Store) SetOutcomeLink(ctx context.Context, sessionID, outcomeID uuid.UUID) error {
	var ws uuid.UUID
	if s.workspaceID != nil {
		ws = *s.workspaceID
	}

	const q = `UPDATE work_sessions SET outcome_id = $1, updated_at = NOW()
		WHERE id = $2 AND workspace_id = $3`

	tag, err := s.pool.Exec(ctx, q, outcomeID, sessionID, ws)
	if err != nil {
		return fmt.Errorf("worksession.SetOutcomeLink: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PruneOlderThan hard-deletes work_session_evidence rows older than cutoff.
// Mirrors outcome.Store.PruneOlderThan exactly: no workspace scoping (this is
// a maintenance/retention operation across the whole table, not a
// per-request read/write isolation boundary).
func (s *Store) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM work_session_evidence WHERE created_at < $1`
	tag, err := s.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("worksession.PruneOlderThan: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ---- helpers ----

func uuidOrNil(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return *id
}

// strOrNil returns nil for a nil pointer, otherwise the dereferenced string.
// Used for query args backing nullable TEXT columns.
func strOrNil(s *string) any {
	if s == nil {
		return nil
	}
	return *s
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
