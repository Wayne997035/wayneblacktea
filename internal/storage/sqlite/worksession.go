package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
)

// txBeginnerConn is the minimal interface of *sql.DB needed by createSessionTx.
type txBeginnerConn interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// sqliteInClause builds positional-placeholder strings "?1,?2,...,?N" for
// SQLite batch updates. Returns the IN clause string (task ID args must be
// provided by the caller starting at position 1).
func sqliteInClause(n int) string {
	placeholders := make([]string, n)
	for i := range n {
		placeholders[i] = fmt.Sprintf("?%d", i+1)
	}
	return strings.Join(placeholders, ",")
}

// batchMarkTasksInProgressSQLite sets status='in_progress' WHERE status='pending'
// for the given task IDs, scoped to workspace ws.
// Arg layout: ?1..?N = task IDs, ?N+1 = workspace (nil or string), ?N+2 = now.
func batchMarkTasksInProgressSQLite(ctx context.Context, conn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, ws any, taskIDs []uuid.UUID,
) error {
	if len(taskIDs) == 0 {
		return nil
	}
	n := len(taskIDs)
	// Pre-allocate the full args slice to avoid gocritic appendAssign.
	args := make([]any, 0, n+2)
	for _, id := range taskIDs {
		args = append(args, id.String())
	}
	args = append(args, ws, nowRFC3339())
	q := fmt.Sprintf(`UPDATE tasks
		SET status = 'in_progress', updated_at = ?%d
		WHERE id IN (%s)
		  AND (?%d IS NULL OR workspace_id = ?%d)
		  AND status = 'pending'`,
		n+2, sqliteInClause(n), n+1, n+1)
	if _, err := conn.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("batchMarkTasksInProgressSQLite: %w", err)
	}
	return nil
}

// batchMarkTasksCompletedSQLite sets status='completed' for tasks not already
// completed or cancelled. Workspace boundary enforced by WHERE clause.
// Arg layout: ?1..?N = task IDs, ?N+1 = workspace, ?N+2 = artifact (or nil), ?N+3 = now.
func batchMarkTasksCompletedSQLite(ctx context.Context, conn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, ws any, taskIDs []uuid.UUID, artifact *string,
) error {
	if len(taskIDs) == 0 {
		return nil
	}
	n := len(taskIDs)
	var artVal any
	if artifact != nil {
		artVal = *artifact
	}
	// Pre-allocate the full args slice to avoid gocritic appendAssign.
	args := make([]any, 0, n+3)
	for _, id := range taskIDs {
		args = append(args, id.String())
	}
	args = append(args, ws, artVal, nowRFC3339())
	q := fmt.Sprintf(`UPDATE tasks
		SET status = 'completed',
		    artifact = COALESCE(?%d, artifact),
		    updated_at = ?%d
		WHERE id IN (%s)
		  AND (?%d IS NULL OR workspace_id = ?%d)
		  AND status NOT IN ('completed','cancelled')`,
		n+2, n+3, sqliteInClause(n), n+1, n+1)
	if _, err := conn.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("batchMarkTasksCompletedSQLite: %w", err)
	}
	return nil
}

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
	created_at, updated_at,
	context_pack_id, verification_status, verification_command,
	verification_output_excerpt, outcome_id, final_result, branch_name`

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
		contextPackIDNS, outcomeIDNS                               sql.NullString
		verificationStatusNS, verificationCommandNS                sql.NullString
		verificationOutputExcerptNS, finalResultNS, branchNameNS   sql.NullString
	)
	if err := scan(
		&idStr, &wsIDStr, &repoName, &projectIDNS,
		&title, &goal, &status, &source,
		&confirmedPlanIDNS, &currentTaskIDNS,
		&finalSummaryNS, &startedAtNS, &lastCheckpointNS, &completedNS,
		&createdAt, &updatedAt,
		&contextPackIDNS, &verificationStatusNS, &verificationCommandNS,
		&verificationOutputExcerptNS, &outcomeIDNS, &finalResultNS, &branchNameNS,
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
	s.ContextPackID = nullStringToUUIDPtr(contextPackIDNS)
	s.OutcomeID = nullStringToUUIDPtr(outcomeIDNS)
	s.VerificationStatus = nullStringToPtr(verificationStatusNS)
	s.VerificationCommand = nullStringToPtr(verificationCommandNS)
	s.VerificationOutputExcerpt = nullStringToPtr(verificationOutputExcerptNS)
	s.FinalResult = nullStringToPtr(finalResultNS)
	s.BranchName = nullStringToPtr(branchNameNS)
	return &s, nil
}

// validateCreateParams returns an error if any required field is missing.
func validateCreateParams(p worksession.CreateParams) error {
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
		if reason := worksession.CheckControlChars("branch_name", *p.BranchName); reason != "" {
			return fmt.Errorf("worksession.Create: %s", reason)
		}
	}
	return nil
}

// strPtrArg returns nil for a nil pointer, otherwise the dereferenced string.
// Used for SQLite ? placeholder args backing nullable TEXT columns.
func strPtrArg(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// createSessionTx inserts the work_session row and links task_ids inside a
// single transaction. Extracted from Create to reduce cyclomatic complexity.
func createSessionTx(ctx context.Context, conn txBeginnerConn, wsID string, id uuid.UUID, p worksession.CreateParams, now string) error {
	firstTaskIDArg := any(nil)
	if len(p.TaskIDs) > 0 {
		firstTaskIDArg = p.TaskIDs[0].String()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("worksession.Create begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	const insertQ = `INSERT INTO work_sessions
		(id, workspace_id, repo_name, project_id, title, goal, status, source,
		 confirmed_plan_id, current_task_id, started_at, created_at, updated_at, branch_name)
		VALUES (?1,?2,?3,?4,?5,?6,'in_progress',?7,?8,?9,?10,?11,?12,?13)`

	_, err = tx.ExecContext(
		ctx, insertQ,
		id.String(), wsID, p.RepoName,
		nullStringFromUUID(p.ProjectID),
		p.Title, p.Goal, p.Source,
		nullStringFromUUID(p.ConfirmedPlanID),
		firstTaskIDArg,
		now, now, now,
		strPtrArg(p.BranchName),
	)
	if err != nil {
		if isUniqueViolationSQLite(err) {
			return worksession.ErrAlreadyActive
		}
		return fmt.Errorf("worksession.Create insert: %w", err)
	}

	const linkQ = `INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		VALUES (?1,?2,'primary',?3)
		ON CONFLICT (session_id, task_id) DO NOTHING`
	for _, taskID := range p.TaskIDs {
		if _, e := tx.ExecContext(ctx, linkQ, id.String(), taskID.String(), now); e != nil {
			err = fmt.Errorf("worksession.Create link task %s: %w", taskID, e)
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("worksession.Create commit: %w", err)
	}
	return nil
}

// Create inserts a new in_progress work session and links task_ids as primary.
func (s *WorkSessionStore) Create(ctx context.Context, p worksession.CreateParams) (*worksession.Session, error) {
	if err := validateCreateParams(p); err != nil {
		return nil, err
	}

	wsID := p.WorkspaceID.String()
	if s.db.workspaceID != "" {
		wsID = s.db.workspaceID
	}

	id := uuid.New()
	now := nowRFC3339()

	if err := createSessionTx(ctx, s.db.conn, wsID, id, p, now); err != nil {
		return nil, err
	}

	// Batch-update linked tasks to in_progress after the session tx commits.
	// Non-fatal: session is committed; task status update is best-effort.
	if len(p.TaskIDs) > 0 {
		if err := batchMarkTasksInProgressSQLite(ctx, s.db.conn, s.db.workspaceArg(), p.TaskIDs); err != nil {
			slog.Warn(
				"batchMarkTasksInProgressSQLite: failed to mark tasks in_progress",
				"session_id", id,
				"err", err,
			)
		}
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

// Finish sets status=completed and stores final_summary. After the session
// update, linked tasks are batch-marked as completed:
//   - If FinishParams.CompletedTaskIDs is non-empty, only those tasks are marked.
//   - Otherwise, all tasks linked via work_session_tasks are marked completed.
//
// wbt-2.0 P2.2: also persists VerificationStatus/VerificationCommand/
// VerificationOutputExcerpt/FinalResult/OutcomeID and, when p.Evidence is
// non-empty, inserts each evidence row (best-effort, non-fatal on error).
func (s *WorkSessionStore) Finish(ctx context.Context, p worksession.FinishParams) (*worksession.Session, error) {
	ws := s.db.workspaceArg()

	if p.VerificationCommand != nil {
		if reason := worksession.CheckControlChars("verification_command", *p.VerificationCommand); reason != "" {
			return nil, fmt.Errorf("worksession.Finish: %s", reason)
		}
	}

	var verificationExcerptArg any
	if p.VerificationOutputExcerpt != nil {
		verificationExcerptArg = worksession.RedactAndCapOutputExcerpt(*p.VerificationOutputExcerpt)
	}

	const q = `UPDATE work_sessions
		SET status = 'completed',
		    completed_at = ?3,
		    final_summary = ?4,
		    verification_status = ?5,
		    verification_command = ?6,
		    verification_output_excerpt = ?7,
		    final_result = ?8,
		    outcome_id = ?9,
		    updated_at = ?3
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		  AND status IN ('in_progress','checkpointed')`

	now := nowRFC3339()
	res, err := s.db.conn.ExecContext(ctx, q,
		p.SessionID.String(), ws, now, nullStringIfEmpty(p.Summary),
		strPtrArg(p.VerificationStatus), strPtrArg(p.VerificationCommand),
		verificationExcerptArg, strPtrArg(p.FinalResult), nullStringFromUUID(p.OutcomeID))
	if err != nil {
		return nil, errWrap("WorkSessionStore.Finish", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, worksession.ErrNotFound
	}

	// Resolve which tasks to mark completed. deferred_task_ids always wins
	// over completed_task_ids (and over the empty-completed fallback below) —
	// see worksession.ResolveCompletedTaskIDs (wbt-2.0 P2 review F2).
	var linkedIDs []uuid.UUID
	if len(p.CompletedTaskIDs) == 0 {
		// Fallback: find all tasks linked to this session.
		linked, lErr := s.LinkedTasks(ctx, p.SessionID)
		if lErr == nil {
			linkedIDs = make([]uuid.UUID, 0, len(linked))
			for _, lt := range linked {
				linkedIDs = append(linkedIDs, lt.TaskID)
			}
		}
	}
	taskIDs := worksession.ResolveCompletedTaskIDs(p.CompletedTaskIDs, p.DeferredTaskIDs, linkedIDs)
	if len(taskIDs) > 0 {
		_ = batchMarkTasksCompletedSQLite(ctx, s.db.conn, ws, taskIDs, p.Artifact)
	}

	for _, ei := range p.Evidence {
		if _, evErr := s.AddEvidence(ctx, worksession.Evidence{
			SessionID:     p.SessionID,
			EvidenceType:  ei.EvidenceType,
			Status:        ei.Status,
			Command:       ei.Command,
			Artifact:      ei.Artifact,
			OutputExcerpt: ei.OutputExcerpt,
		}); evErr != nil {
			// Non-fatal: session Finish already committed; evidence insertion
			// is best-effort audit data (mirrors batchMarkTasksCompletedSQLite above).
			slog.Warn("WorkSessionStore.Finish: failed to add evidence (non-fatal)",
				"session_id", p.SessionID, "evidence_type", ei.EvidenceType, "err", evErr)
		}
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

// ListRecent returns sessions ordered by created_at DESC, optionally
// filtered by repoName (empty = no filter). limit is hard-capped at
// worksession.MaxListRecentLimit (100) regardless of the caller-requested
// value. Returns an empty (non-nil) slice when the caller's workspaceID does
// not match the store's configured workspace (mirrors GetActive's silent
// cross-workspace deny).
func (s *WorkSessionStore) ListRecent(
	ctx context.Context, workspaceID uuid.UUID, repoName string, limit int,
) ([]worksession.Session, error) {
	if s.db.workspaceID != "" && s.db.workspaceID != workspaceID.String() {
		return []worksession.Session{}, nil
	}
	ws := workspaceID.String()
	if s.db.workspaceID != "" {
		ws = s.db.workspaceID
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > worksession.MaxListRecentLimit {
		limit = worksession.MaxListRecentLimit
	}
	var repoArg any
	if repoName != "" {
		repoArg = repoName
	}

	const q = `SELECT ` + workSessionSelectCols + ` FROM work_sessions
		WHERE workspace_id = ?1
		  AND (?2 IS NULL OR repo_name = ?2)
		ORDER BY created_at DESC
		LIMIT ?3`

	rows, err := s.db.conn.QueryContext(ctx, q, ws, repoArg, limit)
	if err != nil {
		return nil, errWrap("WorkSessionStore.ListRecent", err)
	}
	defer func() { _ = rows.Close() }()

	out := []worksession.Session{}
	for rows.Next() {
		sess, err := scanWorkSession(rows.Scan)
		if err != nil {
			return nil, errWrap("WorkSessionStore.ListRecent scan", err)
		}
		out = append(out, *sess)
	}
	return out, errWrap("WorkSessionStore.ListRecent iter", rows.Err())
}

const evidenceSelectCols = `id, workspace_id, session_id, evidence_type, status,
	command, artifact, output_excerpt, created_at`

func scanEvidence(scan func(...any) error) (*worksession.Evidence, error) {
	var (
		e                                                 worksession.Evidence
		idStr, sessIDStr, evidenceType, status, createdAt string
		wsIDNS, commandNS, artifactNS, outputExcerptNS    sql.NullString
	)
	if err := scan(&idStr, &wsIDNS, &sessIDStr, &evidenceType, &status,
		&commandNS, &artifactNS, &outputExcerptNS, &createdAt); err != nil {
		return nil, err
	}
	e.ID, _ = uuid.Parse(idStr)
	e.SessionID, _ = uuid.Parse(sessIDStr)
	e.EvidenceType = evidenceType
	e.Status = status
	e.CreatedAt = createdAt
	e.WorkspaceID = nullStringToUUIDPtr(wsIDNS)
	e.Command = nullStringToPtr(commandNS)
	e.Artifact = nullStringToPtr(artifactNS)
	e.OutputExcerpt = nullStringToPtr(outputExcerptNS)
	return &e, nil
}

// AddEvidence inserts one evidence row and returns the persisted record.
// ev.OutputExcerpt goes through worksession.RedactAndCapOutputExcerpt
// (redact THEN cap — never the reverse). ev.Command is validated with
// worksession.CheckControlChars.
func (s *WorkSessionStore) AddEvidence(ctx context.Context, ev worksession.Evidence) (*worksession.Evidence, error) {
	if ev.Command != nil {
		if reason := worksession.CheckControlChars("command", *ev.Command); reason != "" {
			return nil, fmt.Errorf("worksession.AddEvidence: %s", reason)
		}
	}

	wsArg := nullStringFromUUID(ev.WorkspaceID)
	if s.db.workspaceID != "" {
		wsArg = s.db.workspaceID
	}

	var excerptArg any
	if ev.OutputExcerpt != nil {
		excerptArg = worksession.RedactAndCapOutputExcerpt(*ev.OutputExcerpt)
	}

	id := uuid.New()
	const q = `INSERT INTO work_session_evidence
		(id, workspace_id, session_id, evidence_type, status, command, artifact, output_excerpt, created_at)
		VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9)`
	if _, err := s.db.conn.ExecContext(
		ctx, q,
		id.String(), wsArg, ev.SessionID.String(), ev.EvidenceType, ev.Status,
		strPtrArg(ev.Command), strPtrArg(ev.Artifact), excerptArg, nowRFC3339(),
	); err != nil {
		return nil, errWrap("WorkSessionStore.AddEvidence", err)
	}
	return s.getEvidenceByID(ctx, id)
}

func (s *WorkSessionStore) getEvidenceByID(ctx context.Context, id uuid.UUID) (*worksession.Evidence, error) {
	const q = `SELECT ` + evidenceSelectCols + ` FROM work_session_evidence WHERE id = ?1 LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	e, err := scanEvidence(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("worksession.AddEvidence: evidence not found after insert")
	}
	if err != nil {
		return nil, errWrap("WorkSessionStore.getEvidenceByID", err)
	}
	return e, nil
}

// GetEvidence returns all work_session_evidence rows for sessionID, scoped to
// the store's configured workspace (wbt-2.0 P2 review F5 — mirrors
// Checkpoint/Finish's `(?N IS NULL OR workspace_id = ?N)` pattern via
// workspaceArg(); defence in depth alongside the caller-side GetByID gate
// that handleGetWorkSessionTrace already performs before calling this).
// AddEvidence (called only from within Finish) stamps workspace_id with this
// same resolved value whenever the store is configured, so the predicate
// here never excludes rows written through the normal Finish path. Ordered
// by created_at ASC. Returns an empty (non-nil) slice when no rows exist.
func (s *WorkSessionStore) GetEvidence(ctx context.Context, sessionID uuid.UUID) ([]worksession.Evidence, error) {
	ws := s.db.workspaceArg()
	const q = `SELECT ` + evidenceSelectCols + ` FROM work_session_evidence
		WHERE (?1 IS NULL OR workspace_id = ?1) AND session_id = ?2 ORDER BY created_at ASC`

	rows, err := s.db.conn.QueryContext(ctx, q, ws, sessionID.String())
	if err != nil {
		return nil, errWrap("WorkSessionStore.GetEvidence", err)
	}
	defer func() { _ = rows.Close() }()

	out := []worksession.Evidence{}
	for rows.Next() {
		e, err := scanEvidence(rows.Scan)
		if err != nil {
			return nil, errWrap("WorkSessionStore.GetEvidence scan", err)
		}
		out = append(out, *e)
	}
	return out, errWrap("WorkSessionStore.GetEvidence iter", rows.Err())
}

// SetOutcomeLink sets work_sessions.outcome_id for sessionID, scoped to the
// store's workspace. Returns worksession.ErrNotFound when sessionID does not
// exist in that workspace.
func (s *WorkSessionStore) SetOutcomeLink(ctx context.Context, sessionID, outcomeID uuid.UUID) error {
	ws := s.db.workspaceArg()
	const q = `UPDATE work_sessions SET outcome_id = ?3, updated_at = ?4
		WHERE id = ?1 AND (?2 IS NULL OR workspace_id = ?2)`

	res, err := s.db.conn.ExecContext(ctx, q, sessionID.String(), ws, outcomeID.String(), nowRFC3339())
	if err != nil {
		return errWrap("WorkSessionStore.SetOutcomeLink", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return worksession.ErrNotFound
	}
	return nil
}

// PruneOlderThan hard-deletes work_session_evidence rows older than cutoff.
// Mirrors outcome.Store.PruneOlderThan exactly: no workspace scoping (this is
// a maintenance/retention operation, not a per-request isolation boundary).
func (s *WorkSessionStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffStr := cutoff.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	const q = `DELETE FROM work_session_evidence WHERE created_at < ?1`
	res, err := s.db.conn.ExecContext(ctx, q, cutoffStr)
	if err != nil {
		return 0, errWrap("WorkSessionStore.PruneOlderThan", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, errWrap("WorkSessionStore.PruneOlderThan rows affected", err)
	}
	return n, nil
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
