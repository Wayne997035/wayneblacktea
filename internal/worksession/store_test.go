package worksession_test

import (
	"context"
	"testing"

	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/worksession"
	"github.com/google/uuid"
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
		Source:      "test",
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
	if sess.Status != "in_progress" {
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
	if chk.Status != "checkpointed" {
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
	if done.Status != "completed" {
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
