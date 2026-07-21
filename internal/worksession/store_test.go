package worksession_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
)

const (
	statusCheckpointed = "checkpointed"
	statusCompleted    = "completed"
	statusInProgress   = "in_progress"
	statusPending      = "pending"
)

// assigneeClaude / assigneeHuman are canonical gtd.NormalizeActor values used
// across this file and store_postgres_test.go's P6.8 assignee-gate tests
// (both files share package worksession_test, so goconst counts occurrences
// across both — hoisted here once both crossed the min-occurrences-3
// threshold).
const (
	assigneeClaude = "claude"
	assigneeHuman  = "human"
)

// openTestDB opens an in-memory SQLite DB for testing.
func openTestDB(t *testing.T, workspaceID string) *wbtsqlite.DB {
	t.Helper()
	db, err := wbtsqlite.Open(context.Background(), ":memory:", workspaceID)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newStore(t *testing.T, workspaceID string) worksession.StoreIface {
	t.Helper()
	db := openTestDB(t, workspaceID)
	return wbtsqlite.NewWorkSessionStore(db)
}

// makeCreateParams returns valid CreateParams seeded with a fresh UUID workspace.
func makeCreateParams(wsID uuid.UUID, repoName string) worksession.CreateParams {
	return worksession.CreateParams{
		WorkspaceID: wsID,
		RepoName:    repoName,
		Title:       "Test session",
		Goal:        "Implement P0a",
		Source:      "manual",
	}
}

// ---- workspace_id isolation ----

func TestWorkSessionStore_WorkspaceIsolation(t *testing.T) {
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	storeA := newStore(t, wsA)
	storeB := newStore(t, wsB)

	ctx := context.Background()

	// Create a session in workspace A.
	sessA, err := storeA.Create(ctx, makeCreateParams(uuid.MustParse(wsA), "my-repo"))
	if err != nil {
		t.Fatalf("create session A: %v", err)
	}

	// Workspace B should not see workspace A's session.
	result, err := storeB.GetActive(ctx, uuid.MustParse(wsB), "my-repo")
	if err != nil {
		t.Fatalf("GetActive workspace B: %v", err)
	}
	if result.Active {
		t.Error("workspace B should not see workspace A's session")
	}

	// Workspace A can see its own session.
	resultA, err := storeA.GetActive(ctx, uuid.MustParse(wsA), "my-repo")
	if err != nil {
		t.Fatalf("GetActive workspace A: %v", err)
	}
	if !resultA.Active {
		t.Error("workspace A should see its own session")
	}
	if resultA.Session.ID != sessA.ID {
		t.Errorf("session ID mismatch: got %s, want %s", resultA.Session.ID, sessA.ID)
	}
}

// ---- one-active partial unique index constraint ----

func TestWorkSessionStore_OneActiveConstraint(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	p := makeCreateParams(uuid.MustParse(wsID), "my-repo")

	// First create succeeds.
	_, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Second create for same workspace+repo should fail with ErrAlreadyActive.
	_, err = store.Create(ctx, p)
	if err == nil {
		t.Fatal("expected ErrAlreadyActive, got nil")
	}
	if err != worksession.ErrAlreadyActive {
		t.Errorf("expected ErrAlreadyActive, got %v", err)
	}
}

// ---- cross-repo leakage ----

func TestWorkSessionStore_CrossRepoIsolation(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	p1 := makeCreateParams(uuid.MustParse(wsID), "repo-alpha")
	p2 := makeCreateParams(uuid.MustParse(wsID), "repo-beta")

	// Both repos in the same workspace can have independent sessions.
	_, err := store.Create(ctx, p1)
	if err != nil {
		t.Fatalf("create repo-alpha session: %v", err)
	}
	_, err = store.Create(ctx, p2)
	if err != nil {
		t.Fatalf("create repo-beta session (should not conflict): %v", err)
	}

	// Each repo sees only its own session.
	rAlpha, _ := store.GetActive(ctx, uuid.MustParse(wsID), "repo-alpha")
	rBeta, _ := store.GetActive(ctx, uuid.MustParse(wsID), "repo-beta")

	if !rAlpha.Active || rAlpha.Session.RepoName != "repo-alpha" {
		t.Error("repo-alpha should have active session")
	}
	if !rBeta.Active || rBeta.Session.RepoName != "repo-beta" {
		t.Error("repo-beta should have active session")
	}
}

// ---- status transitions ----

func TestWorkSessionStore_StatusTransitions(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "my-repo"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sess.Status != statusInProgress {
		t.Errorf("initial status: got %q, want in_progress", sess.Status)
	}

	// Checkpoint transitions to checkpointed.
	chk, err := store.Checkpoint(ctx, worksession.CheckpointParams{
		SessionID: sess.ID,
		Summary:   "halfway done",
	})
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if chk.Status != statusCheckpointed {
		t.Errorf("after checkpoint status: got %q, want checkpointed", chk.Status)
	}
	if chk.LastCheckpointAt == nil {
		t.Error("last_checkpoint_at should be set after checkpoint")
	}

	// Finish transitions to completed.
	done, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "all done",
	})
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if done.Status != statusCompleted {
		t.Errorf("after finish status: got %q, want completed", done.Status)
	}
	if done.CompletedAt == nil {
		t.Error("completed_at should be set after finish")
	}
	if done.FinalSummary == nil || *done.FinalSummary != "all done" {
		t.Errorf("final_summary: got %v, want 'all done'", done.FinalSummary)
	}
}

// ---- finish on non-existent session ----

func TestWorkSessionStore_FinishNotFound(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	_, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: uuid.New(),
		Summary:   "ghost",
	})
	if err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---- GetByID cross-workspace must not leak ----

func TestWorkSessionStore_GetByID_CrossWorkspace(t *testing.T) {
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// storeA creates a session; storeB uses its own in-memory DB so isolation
	// is per-DB (separate :memory: databases). For cross-workspace test we use
	// a single DB shared between two stores pointing to different workspaces.
	db := openTestDB(t, wsA)
	storeA := wbtsqlite.NewWorkSessionStore(db)

	// Manually override workspace in a second store pointing to same DB.
	// We test GetByID workspace filter using storeA to create, then query
	// with workspace B's UUID directly.
	ctx := context.Background()

	sess, err := storeA.Create(ctx, makeCreateParams(uuid.MustParse(wsA), "my-repo"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// GetByID with wrong workspace should return ErrNotFound.
	_, err = storeA.GetByID(ctx, uuid.MustParse(wsB), sess.ID)
	if err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound for wrong workspace, got %v", err)
	}

	// GetByID with correct workspace should succeed.
	got, err := storeA.GetByID(ctx, uuid.MustParse(wsA), sess.ID)
	if err != nil {
		t.Errorf("expected success for correct workspace, got %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("ID mismatch: got %s, want %s", got.ID, sess.ID)
	}
}

// insertTestTask inserts a minimal task row to satisfy the FK constraint in
// work_session_tasks.task_id. The tasks table requires workspace_id, title
// and status; other columns use schema defaults.
func insertTestTask(t *testing.T, db *wbtsqlite.DB, wsID, taskID string) {
	t.Helper()
	const q = `INSERT INTO tasks (id, workspace_id, title, status, priority)
		VALUES (?1,?2,'test task','pending',3)`
	if err := db.ExecContext(context.Background(), q, taskID, wsID); err != nil {
		t.Fatalf("insertTestTask: %v", err)
	}
}

// insertTestTaskWithAssignee is the P6.8 sibling of insertTestTask, seeding a
// task with an existing assignee — used to verify batchMarkTasksInProgressSQLite
// preserves it instead of overwriting with the session's assignee.
func insertTestTaskWithAssignee(t *testing.T, db *wbtsqlite.DB, wsID, taskID, assignee string) {
	t.Helper()
	const q = `INSERT INTO tasks (id, workspace_id, title, status, priority, assignee)
		VALUES (?1,?2,'test task','pending',3,?3)`
	if err := db.ExecContext(context.Background(), q, taskID, wsID, assignee); err != nil {
		t.Fatalf("insertTestTaskWithAssignee: %v", err)
	}
}

// queryTaskAssignee reads the assignee column for a task directly from the
// SQLite DB. Returns "" for SQL NULL.
func queryTaskAssignee(t *testing.T, db *wbtsqlite.DB, taskID string) string {
	t.Helper()
	row := db.QueryRowContext(context.Background(),
		`SELECT COALESCE(assignee, '') FROM tasks WHERE id = ?1`, taskID)
	var assignee string
	if err := row.Scan(&assignee); err != nil {
		t.Fatalf("queryTaskAssignee %s: %v", taskID, err)
	}
	return assignee
}

// ---- LinkTask and LinkedTasks ----

func TestWorkSessionStore_LinkedTasks(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()

	// Insert tasks to satisfy FK constraints.
	insertTestTask(t, db, wsID, taskA.String())
	insertTestTask(t, db, wsID, taskB.String())

	p := makeCreateParams(uuid.MustParse(wsID), "my-repo")
	p.TaskIDs = []uuid.UUID{taskA, taskB}

	sess, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("create with tasks: %v", err)
	}

	tasks, err := store.LinkedTasks(ctx, sess.ID)
	if err != nil {
		t.Fatalf("LinkedTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Errorf("expected 2 linked tasks, got %d", len(tasks))
	}
	for _, task := range tasks {
		if task.Role != "primary" {
			t.Errorf("expected role=primary, got %q", task.Role)
		}
	}

	// current_task_id should be first task.
	if sess.CurrentTaskID == nil || *sess.CurrentTaskID != taskA {
		t.Errorf("current_task_id should be %s, got %v", taskA, sess.CurrentTaskID)
	}
}

// ---- GetActive returns no tasks when none linked ----

func TestWorkSessionStore_GetActive_NoTasks(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	_, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "no-task-repo"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	result, err := store.GetActive(ctx, uuid.MustParse(wsID), "no-task-repo")
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if !result.Active {
		t.Error("expected active=true")
	}
	if len(result.LinkedTasks) != 0 {
		t.Errorf("expected 0 linked tasks, got %d", len(result.LinkedTasks))
	}
}

// ---- Create validation errors ----

func TestWorkSessionStore_Create_Validation(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	cases := []struct {
		name string
		p    worksession.CreateParams
	}{
		{"empty repo_name", worksession.CreateParams{WorkspaceID: uuid.MustParse(wsID), Title: "t", Goal: "g", Source: "s"}},
		{"empty title", worksession.CreateParams{WorkspaceID: uuid.MustParse(wsID), RepoName: "r", Goal: "g", Source: "s"}},
		{"empty goal", worksession.CreateParams{WorkspaceID: uuid.MustParse(wsID), RepoName: "r", Title: "t", Source: "s"}},
		{"empty source", worksession.CreateParams{WorkspaceID: uuid.MustParse(wsID), RepoName: "r", Title: "t", Goal: "g"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.Create(ctx, tc.p)
			if err == nil {
				t.Error("expected validation error, got nil")
			}
		})
	}
}

// ---- Checkpoint on non-existent session ----

func TestWorkSessionStore_Checkpoint_NotFound(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	_, err := store.Checkpoint(ctx, worksession.CheckpointParams{
		SessionID: uuid.New(),
		Summary:   "ghost",
	})
	if err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---- task auto-status tests ----

// queryTaskStatus reads the status column for a task directly from the SQLite DB.
func queryTaskStatus(t *testing.T, db *wbtsqlite.DB, taskID string) string {
	t.Helper()
	row := db.QueryRowContext(context.Background(),
		`SELECT status FROM tasks WHERE id = ?1`, taskID)
	var status string
	if err := row.Scan(&status); err != nil {
		t.Fatalf("queryTaskStatus %s: %v", taskID, err)
	}
	return status
}

// TestStartWork_AutoMarksTasksInProgress verifies that start_work (Create)
// transitions linked tasks from pending → in_progress. Assignee is supplied
// (P6.8 gate) so the transition is allowed to happen — the no-assignee
// reject path is covered separately by
// TestStartWork_NoAssigneeAnywhere_TaskStaysPending.
func TestStartWork_AutoMarksTasksInProgress(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())
	insertTestTask(t, db, wsID, taskB.String())

	p := makeCreateParams(uuid.MustParse(wsID), "auto-in-progress-repo")
	p.TaskIDs = []uuid.UUID{taskA, taskB}
	p.Assignee = assigneeClaude

	_, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusInProgress {
		t.Errorf("taskA status: got %q, want in_progress", got)
	}
	if got := queryTaskStatus(t, db, taskB.String()); got != statusInProgress {
		t.Errorf("taskB status: got %q, want in_progress", got)
	}
}

// TestStartWork_Idempotent verifies that tasks already in_progress are not
// downgraded when start_work links them again.
func TestStartWork_Idempotent(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())

	// Manually set the task to in_progress before start_work.
	if err := db.ExecContext(
		ctx,
		`UPDATE tasks SET status = 'in_progress' WHERE id = ?1`, taskA.String(),
	); err != nil {
		t.Fatalf("pre-set in_progress: %v", err)
	}

	p := makeCreateParams(uuid.MustParse(wsID), "idempotent-repo")
	p.TaskIDs = []uuid.UUID{taskA}
	_, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Task was already in_progress; it must remain in_progress (not downgraded).
	if got := queryTaskStatus(t, db, taskA.String()); got != statusInProgress {
		t.Errorf("taskA status: got %q, want in_progress (must stay)", got)
	}
}

// ---------------------------------------------------------------------------
// P6.8 — assignee gate on the worksession-start in_progress transition
// (SQLite; mirrors the Postgres tests of the same shape in
// store_postgres_test.go)
// ---------------------------------------------------------------------------

// TestStartWork_StampsAssigneeOntoUnassignedTask verifies a session-level
// assignee is stamped onto a linked task that currently has none, at the
// moment it flips pending → in_progress.
func TestStartWork_StampsAssigneeOntoUnassignedTask(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())

	p := makeCreateParams(uuid.MustParse(wsID), "stamp-assignee-repo")
	p.TaskIDs = []uuid.UUID{taskA}
	p.Assignee = assigneeClaude
	_, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusInProgress {
		t.Errorf("taskA status: got %q, want in_progress", got)
	}
	if got := queryTaskAssignee(t, db, taskA.String()); got != assigneeClaude {
		t.Errorf("taskA assignee: got %q, want stamped %q", got, assigneeClaude)
	}
}

// TestStartWork_PreservesExistingAssignee verifies a task that already has an
// assignee keeps it — the session's assignee must NOT overwrite an existing
// owner.
func TestStartWork_PreservesExistingAssignee(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	insertTestTaskWithAssignee(t, db, wsID, taskA.String(), assigneeHuman)

	p := makeCreateParams(uuid.MustParse(wsID), "preserve-assignee-repo")
	p.TaskIDs = []uuid.UUID{taskA}
	p.Assignee = assigneeClaude
	_, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusInProgress {
		t.Errorf("taskA status: got %q, want in_progress", got)
	}
	if got := queryTaskAssignee(t, db, taskA.String()); got != assigneeHuman {
		t.Errorf("taskA assignee: got %q, want preserved %q (not overwritten by session assignee)", got, assigneeHuman)
	}
}

// TestStartWork_NoAssigneeAnywhere_TaskStaysPending verifies the reject half
// of the P6.8 gate: a task with no assignee, linked to a session that also
// supplies none, is excluded from the flip entirely and stays pending —
// rather than becoming an ownerless in_progress row.
func TestStartWork_NoAssigneeAnywhere_TaskStaysPending(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())

	p := makeCreateParams(uuid.MustParse(wsID), "no-assignee-anywhere-repo")
	p.TaskIDs = []uuid.UUID{taskA}
	_, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusPending {
		t.Errorf("taskA status: got %q, want pending (must NOT flip with no assignee anywhere)", got)
	}
	if got := queryTaskAssignee(t, db, taskA.String()); got != "" {
		t.Errorf("taskA assignee: got %q, want empty", got)
	}
}

// TestStartWork_WhitespaceAssignee_TaskStaysPending pins the P6.8 round-3
// security fix: a whitespace-only assignee (persistable via the HTTP
// CreateTask path, which skips NormalizeActor when strings.TrimSpace is empty)
// must be treated as ownerless by the SQL gate's TRIM, so the task stays
// pending instead of flipping to an effectively-ownerless in_progress row.
// Without TRIM, NULLIF('   ',”) is non-empty and the task would wrongly flip.
func TestStartWork_WhitespaceAssignee_TaskStaysPending(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	insertTestTaskWithAssignee(t, db, wsID, taskA.String(), "   ")

	p := makeCreateParams(uuid.MustParse(wsID), "whitespace-assignee-repo")
	p.TaskIDs = []uuid.UUID{taskA}
	if _, err := store.Create(ctx, p); err != nil { // no session assignee
		t.Fatalf("Create: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusPending {
		t.Errorf("taskA status: got %q, want pending (whitespace-only assignee must be treated as ownerless)", got)
	}
}

// TestWorkSessionStore_Create_RejectsInvalidAssignee verifies the
// domain-layer defense-in-depth re-validation (mirrors
// gtd.Store.CreateTask/UpdateTask): an unrecognized Assignee value is
// rejected even though the MCP layer (tools_worksession.go/tools_plan.go)
// already validates on the way in.
func TestWorkSessionStore_Create_RejectsInvalidAssignee(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	p := makeCreateParams(uuid.MustParse(wsID), "invalid-assignee-repo")
	p.Assignee = "gemini"
	_, err := store.Create(ctx, p)
	if err == nil {
		t.Fatal("Create with unrecognized assignee must error")
	}
	if !errors.Is(err, gtd.ErrInvalidAssignee) {
		t.Errorf("expected gtd.ErrInvalidAssignee, got: %v", err)
	}
}

// TestFinishWork_AutoMarksTasksCompleted verifies that finish_work (Finish)
// marks linked tasks as completed when CompletedTaskIDs is provided.
func TestFinishWork_AutoMarksTasksCompleted(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())
	insertTestTask(t, db, wsID, taskB.String())

	p := makeCreateParams(uuid.MustParse(wsID), "auto-completed-repo")
	p.TaskIDs = []uuid.UUID{taskA, taskB}
	sess, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = store.Finish(ctx, worksession.FinishParams{
		SessionID:        sess.ID,
		Summary:          "done",
		CompletedTaskIDs: []uuid.UUID{taskA, taskB},
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryTaskStatus(t, db, taskB.String()); got != statusCompleted {
		t.Errorf("taskB status: got %q, want completed", got)
	}
}

// TestFinishWork_NoTaskIDs verifies that finish_work (Finish) marks ALL linked
// session tasks as completed when CompletedTaskIDs is empty.
func TestFinishWork_NoTaskIDs(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())
	insertTestTask(t, db, wsID, taskB.String())

	p := makeCreateParams(uuid.MustParse(wsID), "no-task-ids-repo")
	p.TaskIDs = []uuid.UUID{taskA, taskB}
	sess, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Finish with no CompletedTaskIDs — store must discover linked tasks automatically.
	_, err = store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "done without explicit task ids",
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryTaskStatus(t, db, taskB.String()); got != statusCompleted {
		t.Errorf("taskB status: got %q, want completed", got)
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2.2 — evidence chain / verification / outcome linkage (SQLite)
// ---------------------------------------------------------------------------

const (
	evidenceStatusPassed = "passed"
	finalResultSuccess   = "success"
	// taskCheckCmd is the canonical verification/evidence command literal used
	// across several tests in this package (both SQLite and Postgres test
	// files) — named to satisfy goconst's cross-file min-occurrences check.
	taskCheckCmd = "task check"
)

func strPtr(s string) *string { return &s }

// assertEvidenceColumns asserts the 5 evidence-chain columns on a *Session
// match the expected values. Extracted from TestFinish_WritesEvidenceColumns
// to keep that test's cyclomatic complexity under the linter threshold.
func assertEvidenceColumns(t *testing.T, sess *worksession.Session, outcomeID uuid.UUID) {
	t.Helper()
	if sess.VerificationStatus == nil || *sess.VerificationStatus != evidenceStatusPassed {
		t.Errorf("VerificationStatus: got %v, want %s", sess.VerificationStatus, evidenceStatusPassed)
	}
	if sess.VerificationCommand == nil || *sess.VerificationCommand != "cd build && task check" {
		t.Errorf("VerificationCommand: got %v, want the check command", sess.VerificationCommand)
	}
	if sess.VerificationOutputExcerpt == nil || *sess.VerificationOutputExcerpt != "0 issues" {
		t.Errorf("VerificationOutputExcerpt: got %v, want '0 issues'", sess.VerificationOutputExcerpt)
	}
	if sess.FinalResult == nil || *sess.FinalResult != finalResultSuccess {
		t.Errorf("FinalResult: got %v, want %s", sess.FinalResult, finalResultSuccess)
	}
	if sess.OutcomeID == nil || *sess.OutcomeID != outcomeID {
		t.Errorf("OutcomeID: got %v, want %s", sess.OutcomeID, outcomeID)
	}
}

// TestFinish_WritesEvidenceColumns verifies that Finish persists
// VerificationStatus/VerificationCommand/VerificationOutputExcerpt/
// FinalResult/OutcomeID onto the work_sessions row.
func TestFinish_WritesEvidenceColumns(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "evidence-cols-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	outcomeID := uuid.New()
	done, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:                 sess.ID,
		Summary:                   "verified and shipped",
		VerificationStatus:        strPtr(evidenceStatusPassed),
		VerificationCommand:       strPtr("cd build && task check"),
		VerificationOutputExcerpt: strPtr("0 issues"),
		FinalResult:               strPtr(finalResultSuccess),
		OutcomeID:                 &outcomeID,
	})
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	assertEvidenceColumns(t, done, outcomeID)

	// Read back via GetByID to prove persistence, not just the Finish return value.
	reread, err := store.GetByID(ctx, uuid.MustParse(wsID), sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	assertEvidenceColumns(t, reread, outcomeID)
}

// TestAddEvidence_TruncatesOutputExcerpt verifies output_excerpt longer than
// worksession.EvidenceOutputExcerptCap (2000) runes is truncated to exactly
// that length on read-back.
func TestAddEvidence_TruncatesOutputExcerpt(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "truncate-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	longExcerpt := strings.Repeat("a", 2500)
	got, err := store.AddEvidence(ctx, worksession.Evidence{
		SessionID:     sess.ID,
		EvidenceType:  "command",
		Status:        evidenceStatusPassed,
		OutputExcerpt: &longExcerpt,
	})
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if got.OutputExcerpt == nil {
		t.Fatal("expected non-nil OutputExcerpt")
	}
	if n := len([]rune(*got.OutputExcerpt)); n != worksession.EvidenceOutputExcerptCap {
		t.Errorf("output_excerpt length: got %d runes, want %d", n, worksession.EvidenceOutputExcerptCap)
	}
}

// TestAddEvidence_RedactsGithubToken verifies a ghp_-style token embedded in
// output_excerpt is redacted to [REDACTED:github-token] in what gets stored.
func TestAddEvidence_RedactsGithubToken(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "redact-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	token := "ghp_" + strings.Repeat("a", 36) // >= 30 chars after ghp_ per redact regex
	excerpt := "auth failed: token=" + token + " rejected by remote"
	got, err := store.AddEvidence(ctx, worksession.Evidence{
		SessionID:     sess.ID,
		EvidenceType:  "command",
		Status:        "failed",
		OutputExcerpt: &excerpt,
	})
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if got.OutputExcerpt == nil {
		t.Fatal("expected non-nil OutputExcerpt")
	}
	if strings.Contains(*got.OutputExcerpt, token) {
		t.Errorf("raw token must not survive redaction, got: %s", *got.OutputExcerpt)
	}
	if !strings.Contains(*got.OutputExcerpt, "[REDACTED:github-token]") {
		t.Errorf("expected [REDACTED:github-token] placeholder, got: %s", *got.OutputExcerpt)
	}

	// Read back via GetEvidence to prove persistence, not just the AddEvidence return value.
	evs, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	if len(evs) != 1 || evs[0].OutputExcerpt == nil || strings.Contains(*evs[0].OutputExcerpt, token) {
		t.Errorf("stored evidence row must not contain raw token: %+v", evs)
	}
}

// TestAddEvidence_RedactBeforeCap_CredentialStraddlingBoundary proves the
// redact-THEN-cap ordering: a ghp_ token whose original (pre-redaction) span
// straddles the 2000-rune cap boundary must still be fully redacted, not
// left as an unredacted partial fragment. If capping happened before
// redaction, only the first ~26 chars of the 44-char token would survive
// truncation — far short of the regex's `{30,}` requirement — and the
// partial raw token would leak into storage unredacted.
func TestAddEvidence_RedactBeforeCap_CredentialStraddlingBoundary(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "straddle-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const paddingLen = 1970
	const tokenSuffixLen = 40 // token = "ghp_" + 40 chars = 44 chars total
	padding := strings.Repeat("x", paddingLen)
	token := "ghp_" + strings.Repeat("b", tokenSuffixLen)
	excerpt := padding + token // token spans runes [1970, 2014) — straddles rune index 2000

	got, err := store.AddEvidence(ctx, worksession.Evidence{
		SessionID:     sess.ID,
		EvidenceType:  "command",
		Status:        "failed",
		OutputExcerpt: &excerpt,
	})
	if err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}
	if got.OutputExcerpt == nil {
		t.Fatal("expected non-nil OutputExcerpt")
	}
	if strings.Contains(*got.OutputExcerpt, token) {
		t.Fatalf("raw token straddling the cap boundary must not survive — redact-then-cap ordering violated, got: %s", *got.OutputExcerpt)
	}
	if !strings.Contains(*got.OutputExcerpt, "[REDACTED:github-token]") {
		t.Errorf("expected full [REDACTED:github-token] placeholder (not truncated), got: %s", *got.OutputExcerpt)
	}
}

// TestListRecent_DescOrder verifies ListRecent returns sessions ordered by
// created_at DESC.
func TestListRecent_DescOrder(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	sessOld, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "recent-repo-old"))
	if err != nil {
		t.Fatalf("Create old: %v", err)
	}
	if _, err := store.Finish(ctx, worksession.FinishParams{SessionID: sessOld.ID, Summary: "done"}); err != nil {
		t.Fatalf("Finish old: %v", err)
	}
	sessNew, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "recent-repo-new"))
	if err != nil {
		t.Fatalf("Create new: %v", err)
	}
	if _, err := store.Finish(ctx, worksession.FinishParams{SessionID: sessNew.ID, Summary: "done"}); err != nil {
		t.Fatalf("Finish new: %v", err)
	}

	// Force deterministic timestamps so DESC ordering isn't flaky under fast
	// sequential inserts within the same millisecond.
	const forceTimestampQ = `UPDATE work_sessions SET created_at = ?2 WHERE id = ?1`
	if err := db.ExecContext(ctx, forceTimestampQ, sessOld.ID.String(), "2020-01-01T00:00:00.000Z"); err != nil {
		t.Fatalf("force old timestamp: %v", err)
	}
	if err := db.ExecContext(ctx, forceTimestampQ, sessNew.ID.String(), "2020-01-02T00:00:00.000Z"); err != nil {
		t.Fatalf("force new timestamp: %v", err)
	}

	got, err := store.ListRecent(ctx, uuid.MustParse(wsID), "", 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(got))
	}
	if got[0].ID != sessNew.ID {
		t.Errorf("expected newest session first, got %s want %s", got[0].ID, sessNew.ID)
	}
	if got[1].ID != sessOld.ID {
		t.Errorf("expected oldest session last, got %s want %s", got[1].ID, sessOld.ID)
	}
}

// TestListRecent_HardCapsLimit verifies limit is hard-capped at
// worksession.MaxListRecentLimit regardless of what the caller requests —
// exercised indirectly by requesting an absurd limit and confirming no error
// (the store must clamp, not reject).
func TestListRecent_HardCapsLimit(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	if _, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "cap-repo")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.ListRecent(ctx, uuid.MustParse(wsID), "", 999999)
	if err != nil {
		t.Fatalf("ListRecent with oversized limit must not error, got: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 session, got %d", len(got))
	}
}

// TestSetOutcomeLink_NotFound verifies SetOutcomeLink returns the store's
// ErrNotFound for an unknown session ID.
func TestSetOutcomeLink_NotFound(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	if err := store.SetOutcomeLink(ctx, uuid.New(), uuid.New()); err != worksession.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// TestSetOutcomeLink_Success verifies SetOutcomeLink persists outcome_id.
func TestSetOutcomeLink_Success(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "link-outcome-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	outcomeID := uuid.New()
	if err := store.SetOutcomeLink(ctx, sess.ID, outcomeID); err != nil {
		t.Fatalf("SetOutcomeLink: %v", err)
	}

	got, err := store.GetByID(ctx, uuid.MustParse(wsID), sess.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.OutcomeID == nil || *got.OutcomeID != outcomeID {
		t.Errorf("OutcomeID: got %v, want %s", got.OutcomeID, outcomeID)
	}
}

// TestCreate_RejectsControlCharsInBranchName verifies branch_name containing
// a newline is rejected (adversarial-input handling, backend-security-design.md §2.1).
func TestCreate_RejectsControlCharsInBranchName(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	p := makeCreateParams(uuid.MustParse(wsID), "branch-repo")
	p.BranchName = strPtr("feature/x\ngit push --force")

	if _, err := store.Create(ctx, p); err == nil {
		t.Error("expected error for branch_name containing newline, got nil")
	}
}

// TestCreate_AcceptsCleanBranchName is the happy-path counterpart to
// TestCreate_RejectsControlCharsInBranchName.
func TestCreate_AcceptsCleanBranchName(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	p := makeCreateParams(uuid.MustParse(wsID), "clean-branch-repo")
	p.BranchName = strPtr("feature/action-lifecycle")

	sess, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.BranchName == nil || *sess.BranchName != "feature/action-lifecycle" {
		t.Errorf("BranchName: got %v, want feature/action-lifecycle", sess.BranchName)
	}
}

// TestFinish_RejectsControlCharsInVerificationCommand verifies
// verification_command containing a newline is rejected.
func TestFinish_RejectsControlCharsInVerificationCommand(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "verify-cmd-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = store.Finish(ctx, worksession.FinishParams{
		SessionID:           sess.ID,
		Summary:             "done",
		VerificationCommand: strPtr("task check\nrm -rf /"),
	})
	if err == nil {
		t.Error("expected error for verification_command containing newline, got nil")
	}
}

// TestGetEvidence_EmptyReturnsEmptySlice verifies GetEvidence returns an
// empty (non-nil) slice, never nil, when no evidence rows exist.
func TestGetEvidence_EmptyReturnsEmptySlice(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "no-evidence-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	if got == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("expected 0 evidence rows, got %d", len(got))
	}
}

// TestFinish_WithEvidence verifies Finish inserts evidence rows supplied via
// FinishParams.Evidence.
func TestFinish_WithEvidence(t *testing.T) {
	wsID := uuid.New().String()
	store := newStore(t, wsID)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "finish-evidence-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID: sess.ID,
		Summary:   "done with evidence",
		Evidence: []worksession.EvidenceInput{
			{EvidenceType: "command", Status: evidenceStatusPassed, Command: strPtr(taskCheckCmd)},
			{EvidenceType: "pr", Status: evidenceStatusPassed, Artifact: strPtr("https://github.com/example/repo/pull/1")},
		},
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	evs, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("expected 2 evidence rows, got %d", len(evs))
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2 review F2 — deferred_task_ids must never be marked completed
// ---------------------------------------------------------------------------

// TestFinishWork_DeferredOnly_ExcludesFromFallback verifies that when
// CompletedTaskIDs is empty (triggering the fallback-to-linked-tasks path),
// tasks listed in DeferredTaskIDs are excluded from that fallback set.
func TestFinishWork_DeferredOnly_ExcludesFromFallback(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())
	insertTestTask(t, db, wsID, taskB.String())

	p := makeCreateParams(uuid.MustParse(wsID), "deferred-only-repo")
	p.TaskIDs = []uuid.UUID{taskA, taskB}
	sess, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// No CompletedTaskIDs — fallback discovers linked tasks, but taskB is
	// deferred and must be excluded from that fallback.
	if _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:       sess.ID,
		Summary:         "deferred taskB to next session",
		DeferredTaskIDs: []uuid.UUID{taskB},
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusCompleted {
		t.Errorf("taskA (not deferred) status: got %q, want completed", got)
	}
	if got := queryTaskStatus(t, db, taskB.String()); got == statusCompleted {
		t.Errorf("taskB (deferred) must NOT be completed, got %q", got)
	}
}

// TestFinishWork_CompletedAndDeferred_NoOverlap verifies that when
// CompletedTaskIDs and DeferredTaskIDs are disjoint, only the completed set
// is marked completed.
func TestFinishWork_CompletedAndDeferred_NoOverlap(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())
	insertTestTask(t, db, wsID, taskB.String())

	p := makeCreateParams(uuid.MustParse(wsID), "completed-deferred-repo")
	p.TaskIDs = []uuid.UUID{taskA, taskB}
	sess, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:        sess.ID,
		Summary:          "taskA done, taskB deferred",
		CompletedTaskIDs: []uuid.UUID{taskA},
		DeferredTaskIDs:  []uuid.UUID{taskB},
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryTaskStatus(t, db, taskB.String()); got == statusCompleted {
		t.Errorf("taskB (deferred) must NOT be completed, got %q", got)
	}
}

// TestFinishWork_CompletedDeferredOverlap_DeferredWins verifies that when a
// task ID is listed in BOTH CompletedTaskIDs and DeferredTaskIDs, deferred
// wins — the task must not be marked completed.
func TestFinishWork_CompletedDeferredOverlap_DeferredWins(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	taskA := uuid.New()
	taskB := uuid.New()
	insertTestTask(t, db, wsID, taskA.String())
	insertTestTask(t, db, wsID, taskB.String())

	p := makeCreateParams(uuid.MustParse(wsID), "overlap-repo")
	p.TaskIDs = []uuid.UUID{taskA, taskB}
	sess, err := store.Create(ctx, p)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// taskB is listed in BOTH completed and deferred — deferred must win.
	if _, err := store.Finish(ctx, worksession.FinishParams{
		SessionID:        sess.ID,
		Summary:          "conflicting lists for taskB",
		CompletedTaskIDs: []uuid.UUID{taskA, taskB},
		DeferredTaskIDs:  []uuid.UUID{taskB},
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	if got := queryTaskStatus(t, db, taskA.String()); got != statusCompleted {
		t.Errorf("taskA status: got %q, want completed", got)
	}
	if got := queryTaskStatus(t, db, taskB.String()); got == statusCompleted {
		t.Errorf("taskB (in both completed and deferred) must NOT be completed — deferred must win, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 security backlog — SQLite/Postgres legacy-mode workspace parity for
// GetEvidence, and a hard row cap
// ---------------------------------------------------------------------------

// TestGetEvidence_LegacyModeRoundTrip verifies that in legacy mode (no
// WORKSPACE_ID configured — empty workspaceID string), AddEvidence's
// zero-UUID stamp and GetEvidence's equality filter round-trip correctly.
// Before this fix, GetEvidence's `(?1 IS NULL OR workspace_id = ?1)`
// predicate degenerated to always-true in legacy mode (AddEvidence wrote SQL
// NULL, GetEvidence's ?1 arg was also NULL) — round trips passed by
// accident, not by a real filter. This test locks in the new real-equality
// behavior and mirrors TestPgStore_GetEvidence_NilWorkspace (Postgres),
// which already filtered correctly via a real uuid.Nil equality check.
func TestGetEvidence_LegacyModeRoundTrip(t *testing.T) {
	store := newStore(t, "") // legacy mode: empty workspaceID
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.Nil, "legacy-evidence-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.AddEvidence(ctx, worksession.Evidence{
		SessionID:    sess.ID,
		EvidenceType: "command",
		Status:       evidenceStatusPassed,
		Command:      strPtr(taskCheckCmd),
	}); err != nil {
		t.Fatalf("AddEvidence in legacy mode: %v", err)
	}

	got, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence in legacy mode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 evidence row in legacy mode round trip, got %d", len(got))
	}
	if got[0].Command == nil || *got[0].Command != taskCheckCmd {
		t.Errorf("command mismatch: got %v, want %s", got[0].Command, taskCheckCmd)
	}
}

// TestGetEvidence_HardCapsLimit verifies GetEvidence returns at most
// worksession.MaxEvidenceListLimit (100) rows even when more exist,
// preserving created_at ASC order and dropping the newest row when the cap
// is hit (mirrors TestListRecent_HardCapsLimit's shape).
func TestGetEvidence_HardCapsLimit(t *testing.T) {
	wsID := uuid.New().String()
	db := openTestDB(t, wsID)
	store := wbtsqlite.NewWorkSessionStore(db)
	ctx := context.Background()

	sess, err := store.Create(ctx, makeCreateParams(uuid.MustParse(wsID), "evidence-cap-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const seedCount = worksession.MaxEvidenceListLimit + 1
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range seedCount {
		cmd := fmt.Sprintf("cmd-%03d", i)
		ev, err := store.AddEvidence(ctx, worksession.Evidence{
			SessionID:    sess.ID,
			EvidenceType: "command",
			Status:       evidenceStatusPassed,
			Command:      &cmd,
		})
		if err != nil {
			t.Fatalf("AddEvidence[%d]: %v", i, err)
		}
		// Force strictly increasing timestamps so ORDER BY created_at ASC is
		// deterministic — fast sequential inserts can otherwise share the
		// same millisecond (mirrors TestListRecent_DescOrder's forced-
		// timestamp pattern above).
		ts := base.Add(time.Duration(i) * time.Second).Format("2006-01-02T15:04:05.000Z07:00")
		if err := db.ExecContext(ctx, `UPDATE work_session_evidence SET created_at = ?2 WHERE id = ?1`, ev.ID.String(), ts); err != nil {
			t.Fatalf("force timestamp[%d]: %v", i, err)
		}
	}

	got, err := store.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence: %v", err)
	}
	if len(got) != worksession.MaxEvidenceListLimit {
		t.Fatalf("expected exactly %d evidence rows (hard cap), got %d", worksession.MaxEvidenceListLimit, len(got))
	}
	if got[0].Command == nil || *got[0].Command != "cmd-000" {
		t.Errorf("expected first row command=cmd-000 (oldest, ASC order), got %v", got[0].Command)
	}
	if got[len(got)-1].Command == nil || *got[len(got)-1].Command != "cmd-099" {
		t.Errorf("expected last row command=cmd-099 (cap drops newest row cmd-100), got %v", got[len(got)-1].Command)
	}
}

// ---------------------------------------------------------------------------
// wbt-2.0 P2 review F5 — GetEvidence must be workspace-scoped
// ---------------------------------------------------------------------------

// TestGetEvidence_WorkspaceIsolation verifies that GetEvidence does not leak
// evidence rows across workspaces. Uses two *DB handles pointed at the same
// on-disk file (not two independent :memory: DBs, which would be trivially
// isolated regardless of any WHERE-clause scoping) so the test actually
// exercises the new workspace_id predicate against shared rows.
func TestGetEvidence_WorkspaceIsolation(t *testing.T) {
	wsA := uuid.New().String()
	wsB := uuid.New().String()
	dbPath := filepath.Join(t.TempDir(), "iso-evidence.db")

	dbA, err := wbtsqlite.Open(context.Background(), dbPath, wsA)
	if err != nil {
		t.Fatalf("Open dbA: %v", err)
	}
	t.Cleanup(func() { _ = dbA.Close() })
	storeA := wbtsqlite.NewWorkSessionStore(dbA)

	dbB, err := wbtsqlite.Open(context.Background(), dbPath, wsB)
	if err != nil {
		t.Fatalf("Open dbB: %v", err)
	}
	t.Cleanup(func() { _ = dbB.Close() })
	storeB := wbtsqlite.NewWorkSessionStore(dbB)

	ctx := context.Background()
	sess, err := storeA.Create(ctx, makeCreateParams(uuid.MustParse(wsA), "iso-evidence-repo"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := storeA.AddEvidence(ctx, worksession.Evidence{
		SessionID:    sess.ID,
		EvidenceType: "command",
		Status:       evidenceStatusPassed,
		Command:      strPtr(taskCheckCmd),
	}); err != nil {
		t.Fatalf("AddEvidence: %v", err)
	}

	// Workspace B must not see workspace A's evidence for this session, even
	// though it is querying by the same session ID against the same file.
	got, err := storeB.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence (storeB): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("workspace B must not see workspace A's evidence, got %d rows", len(got))
	}

	// Workspace A must still see its own evidence (no regression).
	gotA, err := storeA.GetEvidence(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetEvidence (storeA): %v", err)
	}
	if len(gotA) != 1 {
		t.Errorf("workspace A should see its own evidence, got %d rows", len(gotA))
	}
}
