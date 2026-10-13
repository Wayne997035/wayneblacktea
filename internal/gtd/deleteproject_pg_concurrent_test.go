package gtd_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// moveInBeforeDelete wraps the real Postgres adapter. Right before the DELETE
// runs, after the delete transaction has begun, it commits a move of another
// task into the project on a different pool connection. [PR #200 SE-01]
type moveInBeforeDelete struct {
	gtd.DeleteProjectAdapter
	move func(context.Context) error
}

func (w *moveInBeforeDelete) DeleteTaskRows(ctx context.Context) ([]uuid.UUID, error) {
	if err := w.move(ctx); err != nil {
		return nil, err
	}
	return w.DeleteProjectAdapter.DeleteTaskRows(ctx)
}

func TestDeleteProjectOrchestration_PGConcurrentMoveInRefused(t *testing.T) {
	pool := openTestPgPool(t)
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	ctx := context.Background()

	target, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "se01-target", Title: "t"})
	if err != nil {
		t.Fatalf("CreateProject target: %v", err)
	}
	other, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "se01-other", Title: "o"})
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	kept, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &target.ID, Title: "previewed"})
	if err != nil {
		t.Fatalf("CreateTask kept: %v", err)
	}
	mover, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &other.ID, Title: "moved in mid-delete"})
	if err != nil {
		t.Fatalf("CreateTask mover: %v", err)
	}

	ids, err := store.TaskIDsByProjectAllStatuses(ctx, target.ID)
	if err != nil {
		t.Fatalf("TaskIDsByProjectAllStatuses: %v", err)
	}
	preview := gtd.TaskSetDigest(ids)

	moved := false
	w := &moveInBeforeDelete{
		DeleteProjectAdapter: gtd.NewPGDeleteProjectAdapterForTest(store, target.ID),
		move: func(ctx context.Context) error {
			moved = true
			_, err := store.UpdateTask(ctx, mover.ID, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &target.ID}})
			return err
		},
	}
	n, err := gtd.DeleteProjectOrchestration(ctx, target.ID, "se01-probe", time.Now().UTC(), preview, w)
	if !moved {
		t.Fatal("probe broken: the concurrent move never ran")
	}
	if !errors.Is(err, gtd.ErrProjectTasksChanged) {
		t.Fatalf("DeleteProjectOrchestration err = %v (n=%d), want ErrProjectTasksChanged", err, n)
	}
	if _, err := store.GetProjectByID(ctx, target.ID); err != nil {
		t.Errorf("project gone after a refused delete: %v", err)
	}
	for _, id := range []uuid.UUID{kept.ID, mover.ID} {
		if _, err := store.GetTaskByID(ctx, id); err != nil {
			t.Errorf("task %s gone after a refused delete: %v", id, err)
		}
	}
}

// wrapDeleteAdapter runs a hook at an exact point inside the real Postgres
// delete transaction, so a probe can commit a concurrent change there from
// another pool connection. [PR #200 SE-R2-01]
type wrapDeleteAdapter struct {
	gtd.DeleteProjectAdapter
	beforeLock           func()
	beforeSnapshot       func()
	afterLastTaskCleanup func()
}

func (w *wrapDeleteAdapter) LockTaskRows(ctx context.Context) ([]uuid.UUID, error) {
	if w.beforeLock != nil {
		w.beforeLock()
	}
	return w.DeleteProjectAdapter.LockTaskRows(ctx)
}

func (w *wrapDeleteAdapter) SnapshotProjectAndTasks(ctx context.Context, id uuid.UUID, at time.Time, by string) error {
	if w.beforeSnapshot != nil {
		w.beforeSnapshot()
	}
	return w.DeleteProjectAdapter.SnapshotProjectAndTasks(ctx, id, at, by)
}

// NullifyKnowledgeItemTaskRefs is the last task-reference cleanup.
func (w *wrapDeleteAdapter) NullifyKnowledgeItemTaskRefs(ctx context.Context) error {
	err := w.DeleteProjectAdapter.NullifyKnowledgeItemTaskRefs(ctx)
	if err == nil && w.afterLastTaskCleanup != nil {
		w.afterLastTaskCleanup()
	}
	return err
}

func mustProjectID(t *testing.T, store *gtd.Store, name string) uuid.UUID {
	t.Helper()
	p, err := store.CreateProject(context.Background(), gtd.CreateProjectParams{Name: name, Title: name})
	if err != nil {
		t.Fatalf("CreateProject %s: %v", name, err)
	}
	return p.ID
}

func mustTaskID(t *testing.T, store *gtd.Store, projectID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	task, err := store.CreateTask(context.Background(), gtd.CreateTaskParams{ProjectID: &projectID, Title: title})
	if err != nil {
		t.Fatalf("CreateTask %s: %v", title, err)
	}
	return task.ID
}

func moveTask(t *testing.T, store *gtd.Store, taskID, projectID uuid.UUID) {
	t.Helper()
	if _, err := store.UpdateTask(context.Background(), taskID, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &projectID}}); err != nil {
		t.Fatalf("move task %s: %v", taskID, err)
	}
}

func taskProjectID(t *testing.T, store *gtd.Store, taskID uuid.UUID) uuid.UUID {
	t.Helper()
	task, err := store.GetTaskByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetTaskByID %s: %v", taskID, err)
	}
	return uuid.UUID(task.ProjectID.Bytes)
}

func previewDigest(t *testing.T, store *gtd.Store, projectID uuid.UUID) string {
	t.Helper()
	ids, err := store.TaskIDsByProjectAllStatuses(context.Background(), projectID)
	if err != nil {
		t.Fatalf("TaskIDsByProjectAllStatuses: %v", err)
	}
	return gtd.TaskSetDigest(ids)
}

// A previewed task moved out before the delete locks the project's tasks
// leaves the locked set short of the preview: refused, nothing deleted.
func TestDeleteProjectOrchestration_PGMoveOutBeforeLockRefused(t *testing.T) {
	pool := openTestPgPool(t)
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	ctx := context.Background()
	target, other := mustProjectID(t, store, "ser201-a-target"), mustProjectID(t, store, "ser201-a-other")
	k1, k2 := mustTaskID(t, store, target, "k1"), mustTaskID(t, store, target, "k2")
	preview := previewDigest(t, store, target)

	moved := false
	w := &wrapDeleteAdapter{
		DeleteProjectAdapter: gtd.NewPGDeleteProjectAdapterForTest(store, target),
		beforeLock:           func() { moved = true; moveTask(t, store, k1, other) },
	}
	n, err := gtd.DeleteProjectOrchestration(ctx, target, testerActor, time.Now().UTC(), preview, w)
	if !moved {
		t.Fatal("probe broken: the move never ran")
	}
	if !errors.Is(err, gtd.ErrProjectTasksChanged) {
		t.Fatalf("err = %v (n=%d), want ErrProjectTasksChanged", err, n)
	}
	if _, err := store.GetProjectByID(ctx, target); err != nil {
		t.Errorf("project gone after a refused delete: %v", err)
	}
	if got := taskProjectID(t, store, k2); got != target {
		t.Errorf("k2 project = %s, want %s (untouched)", got, target)
	}
	if got := taskProjectID(t, store, k1); got != other {
		t.Errorf("k1 project = %s, want %s (where it was moved)", got, other)
	}
}

// Once the delete has locked the project's tasks, moving one out waits for the
// delete to end: the move times out, the delete goes through and restore
// brings every task back. This closes the move-out-and-back hole (SE-R2-01).
func TestDeleteProjectOrchestration_PGMoveOutBlockedByLock(t *testing.T) {
	pool := openTestPgPool(t)
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	ctx := context.Background()
	target, other := mustProjectID(t, store, "ser201-b-target"), mustProjectID(t, store, "ser201-b-other")
	parking := mustProjectID(t, store, "ser201-b-parking")
	k1, k2 := mustTaskID(t, store, target, "k1"), mustTaskID(t, store, target, "k2")
	unrelated := mustTaskID(t, store, other, "unrelated")

	tryMove := func(taskID, projectID uuid.UUID) error {
		c, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := store.UpdateTask(c, taskID, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &projectID}})
		return err
	}
	if err := tryMove(unrelated, parking); err != nil {
		t.Fatalf("probe broken: a 1-second move with no lock held failed: %v", err)
	}
	preview := previewDigest(t, store, target)

	tried := false
	var blocked error
	w := &wrapDeleteAdapter{
		DeleteProjectAdapter: gtd.NewPGDeleteProjectAdapterForTest(store, target),
		beforeSnapshot:       func() { tried = true; blocked = tryMove(k1, other) },
	}
	n, err := gtd.DeleteProjectOrchestration(ctx, target, testerActor, time.Now().UTC(), preview, w)
	if !tried {
		t.Fatal("probe broken: the move never ran")
	}
	if !errors.Is(blocked, context.DeadlineExceeded) {
		t.Errorf("moving a locked task out returned %v, want context.DeadlineExceeded (held by the lock)", blocked)
	}
	if err != nil || n != 2 {
		t.Fatalf("delete = (%d, %v), want (2, nil)", n, err)
	}
	if _, restored, err := store.RestoreProject(ctx, target, testerActor); err != nil || restored != 2 {
		t.Fatalf("RestoreProject = (%d, %v), want (2, nil)", restored, err)
	}
	for _, id := range []uuid.UUID{k1, k2} {
		if got := taskProjectID(t, store, id); got != target {
			t.Errorf("restored task %s project = %s, want %s", id, got, target)
		}
	}
}

// taskRefs holds one row per task-reference column the delete cleans.
type taskRefs struct{ session, candidate, vision, decision, knowledge uuid.UUID }

func seedTaskRefs(t *testing.T, pool *pgxpool.Pool, ws, taskID uuid.UUID) taskRefs {
	t.Helper()
	r := taskRefs{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	seeds := []struct {
		table string
		sql   string
		args  []any
	}{
		// repo_name is per task: idx_work_sessions_one_active allows one
		// in_progress session per (workspace, repo) and two tasks are seeded.
		{
			"work_sessions", `INSERT INTO work_sessions (id, workspace_id, repo_name, project_id, title, goal, status, source,
			confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
			VALUES ($1,$2,$4,NULL,'s','g','in_progress','manual',NULL,$3,NOW(),NOW(),NOW())`,
			[]any{r.session, ws, taskID, "repo-" + taskID.String()},
		},
		{"work_session_tasks", `INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
			VALUES ($1,$2,'primary',NOW())`, []any{r.session, taskID}},
		{"completion_candidates", `INSERT INTO completion_candidates (id, workspace_id, task_id, reason, confidence)
			VALUES ($1,$2,$3,'r','high')`, []any{r.candidate, ws, taskID}},
		{"vision_items", `INSERT INTO vision_items (id, workspace_id, title, why_blocked, status, promoted_task_id)
			VALUES ($1,$2,'v','b','promoted',$3)`, []any{r.vision, ws, taskID}},
		{"decisions", `INSERT INTO decisions (id, workspace_id, title, context, decision, rationale, task_id, created_at)
			VALUES ($1,$2,'d','ctx','x','y',$3,NOW())`, []any{r.decision, ws, taskID}},
		{"knowledge_items", `INSERT INTO knowledge_items (id, workspace_id, type, title, content, task_id, created_at, updated_at)
			VALUES ($1,$2,'til','k','b',$3,NOW(),NOW())`, []any{r.knowledge, ws, taskID}},
	}
	for _, s := range seeds {
		if _, err := pool.Exec(context.Background(), s.sql, s.args...); err != nil {
			t.Fatalf("seed %s: %v", s.table, err)
		}
	}
	return r
}

// taskRefsPointAt reports, for each of the six cleaned columns, whether the
// seeded row still references taskID.
func taskRefsPointAt(t *testing.T, pool *pgxpool.Pool, r taskRefs, taskID uuid.UUID) map[string]bool {
	t.Helper()
	ctx := context.Background()
	is := func(id pgtype.UUID) bool { return id.Valid && uuid.UUID(id.Bytes) == taskID }
	got := map[string]bool{}
	var n int
	var id pgtype.UUID
	scan := func(name, q string, arg any, dst ...any) {
		if err := pool.QueryRow(ctx, q, arg).Scan(dst...); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM work_session_tasks WHERE session_id = $1 AND task_id = $2`,
		r.session, taskID).Scan(&n); err != nil {
		t.Fatalf("read work_session_tasks: %v", err)
	}
	got["work_session_tasks.task_id"] = n > 0
	scan("work_sessions", `SELECT current_task_id FROM work_sessions WHERE id = $1`, r.session, &id)
	got["work_sessions.current_task_id"] = is(id)
	scan("completion_candidates", `SELECT count(*) FROM completion_candidates WHERE id = $1`, r.candidate, &n)
	got["completion_candidates.task_id"] = n > 0
	scan("vision_items", `SELECT promoted_task_id FROM vision_items WHERE id = $1`, r.vision, &id)
	got["vision_items.promoted_task_id"] = is(id)
	scan("decisions", `SELECT task_id FROM decisions WHERE id = $1`, r.decision, &id)
	got["decisions.task_id"] = is(id)
	scan("knowledge_items", `SELECT task_id FROM knowledge_items WHERE id = $1`, r.knowledge, &id)
	got["knowledge_items.task_id"] = is(id)
	return got
}

// A task F that was never previewed moves into the project after the lock
// and out again before the DELETE: it must not be tombstoned and none of its
// references may be cleared. The previewed task K, which is deleted, must have
// all six references cleared.
//
//nolint:gocyclo // one seeded delete, then a flat run of independent assertions; splitting detaches each check from its fixture
func TestDeleteProjectOrchestration_PGForeignTaskInAndOut(t *testing.T) {
	pool := openTestPgPool(t)
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	ctx := context.Background()
	target, other := mustProjectID(t, store, "ser201-c-target"), mustProjectID(t, store, "ser201-c-other")
	k, f := mustTaskID(t, store, target, "previewed K"), mustTaskID(t, store, other, "foreign F")
	kRefs, fRefs := seedTaskRefs(t, pool, ws, k), seedTaskRefs(t, pool, ws, f)
	preview := previewDigest(t, store, target)

	movedIn, movedOut := false, false
	w := &wrapDeleteAdapter{
		DeleteProjectAdapter: gtd.NewPGDeleteProjectAdapterForTest(store, target),
		beforeSnapshot:       func() { movedIn = true; moveTask(t, store, f, target) },
		afterLastTaskCleanup: func() { movedOut = true; moveTask(t, store, f, other) },
	}
	n, err := gtd.DeleteProjectOrchestration(ctx, target, testerActor, time.Now().UTC(), preview, w)
	if !movedIn || !movedOut {
		t.Fatalf("probe broken: moved in %v, moved out %v", movedIn, movedOut)
	}
	if err != nil || n != 1 {
		t.Fatalf("delete = (%d, %v), want (1, nil)", n, err)
	}
	if got := taskProjectID(t, store, f); got != other {
		t.Errorf("F project = %s, want %s", got, other)
	}
	var tombs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deletion_tombstones WHERE entity_id = $1`, f).Scan(&tombs); err != nil {
		t.Fatalf("count F tombstones: %v", err)
	}
	if tombs != 0 {
		t.Errorf("F has %d tombstone rows, want 0 (it was never locked)", tombs)
	}
	for col, still := range taskRefsPointAt(t, pool, fRefs, f) {
		if !still {
			t.Errorf("foreign task F's %s reference was cleared", col)
		}
	}
	for col, still := range taskRefsPointAt(t, pool, kRefs, k) {
		if still {
			t.Errorf("deleted task K's %s reference was not cleared", col)
		}
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM vision_items WHERE id = $1`, kRefs.vision).Scan(&status); err != nil {
		t.Fatalf("read K vision status: %v", err)
	}
	if status != "open" {
		t.Errorf("K's promoted vision item status = %q, want open", status)
	}
	if _, restored, err := store.RestoreProject(ctx, target, testerActor); err != nil || restored != 1 {
		t.Errorf("RestoreProject = (%d, %v), want (1, nil)", restored, err)
	}
}

// With the server's default set to REPEATABLE READ, the delete still runs at
// READ COMMITTED, so a task moved in before the DELETE is seen and refused
// instead of being left pointing at the deleted project. [DBI-R2-03]
func TestDeleteProjectOrchestration_PGRepeatableReadDefaultPinned(t *testing.T) {
	pool := openTestPgPool(t)
	ctx := context.Background()
	cfg := pool.Config()
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = "repeatable read"
	rrPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(rrPool.Close)
	var level string
	if err := rrPool.QueryRow(ctx, `SHOW default_transaction_isolation`).Scan(&level); err != nil || level != "repeatable read" {
		t.Fatalf("probe broken: pool default = %q (%v), want repeatable read", level, err)
	}

	ws := uuid.New()
	store := newPgGTDStore(rrPool, &ws)
	target, other := mustProjectID(t, store, "ser201-d-target"), mustProjectID(t, store, "ser201-d-other")
	mustTaskID(t, store, target, "kept")
	mover := mustTaskID(t, store, other, "moved in mid-delete")
	preview := previewDigest(t, store, target)

	moved := false
	w := &moveInBeforeDelete{
		DeleteProjectAdapter: gtd.NewPGDeleteProjectAdapterForTest(store, target),
		move: func(ctx context.Context) error {
			moved = true
			_, err := store.UpdateTask(ctx, mover, gtd.UpdateTaskParams{Project: &gtd.ProjectRef{ID: &target}})
			return err
		},
	}
	n, err := gtd.DeleteProjectOrchestration(ctx, target, testerActor, time.Now().UTC(), preview, w)
	if !moved {
		t.Fatal("probe broken: the concurrent move never ran")
	}
	if !errors.Is(err, gtd.ErrProjectTasksChanged) {
		t.Fatalf("err = %v (n=%d), want ErrProjectTasksChanged", err, n)
	}
	if _, err := store.GetProjectByID(ctx, target); err != nil {
		t.Errorf("project gone after a refused delete: %v", err)
	}
}

// The F202 probes below run delete_task and delete_project, both production
// code, against the same task K that carries all six kinds of reference row.
// pg_stat_activity and pg_blocking_pids prove the interleaving happened before
// either side is let go; no fixed sleep stands in for that.
const (
	f202PollLimit   = 10 * time.Second
	f202PollEvery   = 10 * time.Millisecond
	f202ReturnLimit = 15 * time.Second
	f202Deadlock    = "40P01"
)

// f202Waiter picks client backends of this database, other than the polling
// connection itself, that are waiting on a lock.
const f202Waiter = `w.datname = current_database() AND w.backend_type = 'client backend'
	AND w.pid <> pg_backend_pid() AND w.wait_event_type = 'Lock'`

// f202WaitsOnIdleTx matches a waiter blocked by a backend that sits idle in its
// transaction, which is where delete_project parks inside a Go hook.
const f202WaitsOnIdleTx = `SELECT w.pid FROM pg_stat_activity w WHERE ` + f202Waiter + `
	AND EXISTS (SELECT 1 FROM pg_stat_activity b
	             WHERE b.pid = ANY(pg_blocking_pids(w.pid)) AND b.state = 'idle in transaction')
	ORDER BY w.pid LIMIT 1`

// f202WaitsOnPID matches a waiter that backend $1 blocks.
const f202WaitsOnPID = `SELECT w.pid FROM pg_stat_activity w WHERE ` + f202Waiter + `
	AND $1::int = ANY(pg_blocking_pids(w.pid)) ORDER BY w.pid LIMIT 1`

// f202Fixture is one project holding one task K whose six reference rows are seeded.
type f202Fixture struct {
	pool    *pgxpool.Pool
	store   *gtd.Store
	project uuid.UUID
	task    uuid.UUID
	refs    taskRefs
	preview string
}

// f202Result is what delete_project returned.
type f202Result struct {
	n   int
	err error
}

func newF202Fixture(t *testing.T, name string) *f202Fixture {
	t.Helper()
	pool := openTestPgPool(t)
	var level string
	if err := pool.QueryRow(context.Background(), `SHOW default_transaction_isolation`).Scan(&level); err != nil || level != "read committed" {
		t.Fatalf("probe broken: test pool default_transaction_isolation = %q (%v), want read committed", level, err)
	}
	ws := uuid.New()
	store := newPgGTDStore(pool, &ws)
	// project names are unique across workspaces and a run in which the project delete is
	// refused or aborted leaves its project behind, so every run gets its own name
	project := mustProjectID(t, store, name+"-"+ws.String())
	task := mustTaskID(t, store, project, "K")
	refs := seedTaskRefs(t, pool, ws, task)
	// the digest is taken before either delete starts; "" would skip the set check
	return &f202Fixture{pool: pool, store: store, project: project, task: task, refs: refs, preview: previewDigest(t, store, project)}
}

func f202Context(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// f202Connect opens a connection outside the pool: the pool holds only a few
// connections and the deletes under test need theirs.
func f202Connect(t *testing.T, pool *pgxpool.Pool) *pgx.Conn {
	t.Helper()
	conn, err := pgx.ConnectConfig(context.Background(), pool.Config().ConnConfig)
	if err != nil {
		t.Fatalf("probe broken: connect outside the pool: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Logf("close connection outside the pool: %v", err)
		}
	})
	return conn
}

// f202WaitFor runs query on conn until it yields a pid, for at most f202PollLimit.
func f202WaitFor(conn *pgx.Conn, query string, args ...any) (int32, error) {
	ctx, cancel := context.WithTimeout(context.Background(), f202PollLimit)
	defer cancel()
	for {
		var pid int32
		err := conn.QueryRow(ctx, query, args...).Scan(&pid)
		if err == nil {
			return pid, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(f202PollEvery):
		}
	}
}

func f202Await[V any](t *testing.T, ch <-chan V, who string) V {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(f202ReturnLimit):
		t.Fatalf("probe broken: %s never returned", who)
	}
	var zero V
	return zero
}

func f202DeleteTaskAsync(ctx context.Context, fx *f202Fixture, out chan<- error) {
	go func() { out <- fx.store.DeleteTask(ctx, fx.task, testerActor) }()
}

func f202DeleteProjectAsync(ctx context.Context, fx *f202Fixture, adapter gtd.DeleteProjectAdapter) <-chan f202Result {
	out := make(chan f202Result, 1)
	go func() {
		n, err := gtd.DeleteProjectOrchestration(ctx, fx.project, testerActor, time.Now().UTC(), fx.preview, adapter)
		out <- f202Result{n: n, err: err}
	}()
	return out
}

func f202IsDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == f202Deadlock
}

// f202Verify holds the checks both interleavings share: no deadlock on either
// side, and K gone once, with every reference to it cleared.
func f202Verify(t *testing.T, fx *f202Fixture, tErr error, d f202Result) {
	t.Helper()
	if f202IsDeadlock(tErr) {
		t.Errorf("delete_task was aborted by a deadlock: %v", tErr)
	}
	if f202IsDeadlock(d.err) {
		t.Errorf("delete_project was aborted by a deadlock: %v", d.err)
	}
	ctx := context.Background()
	if _, err := fx.store.GetTaskByID(ctx, fx.task); !errors.Is(err, gtd.ErrNotFound) {
		t.Errorf("GetTaskByID(K) = %v, want gtd.ErrNotFound", err)
	}
	for col, still := range taskRefsPointAt(t, fx.pool, fx.refs, fx.task) {
		if still {
			t.Errorf("%s still points at the deleted task", col)
		}
	}
	var tombs int
	if err := fx.pool.QueryRow(ctx,
		`SELECT count(*) FROM deletion_tombstones WHERE entity_kind = 'task' AND entity_id = $1`, fx.task).Scan(&tombs); err != nil {
		t.Fatalf("count task tombstones: %v", err)
	}
	if tombs != 1 {
		t.Errorf("K has %d task tombstone rows, want exactly 1", tombs)
	}
}

// [F202-02] Guards F202-01 for the interleaving where delete_project reaches K
// first: it holds K's row lock and waits inside a hook, delete_task starts and
// must queue behind that lock instead of taking reference rows first. Without
// the F202-01 lock in WorkspacePrecheck, delete_task is blocked at its own
// DELETE of K while delete_project then waits for the reference rows it holds:
// SQLSTATE 40P01. With it, delete_task finds K gone and is a quiet no-op.
func TestDeleteTaskVsDeleteProject_PGProjectFirst(t *testing.T) {
	fx := newF202Fixture(t, "f202-a")
	ctx := f202Context(t)
	probe := f202Connect(t, fx.pool)

	tCh := make(chan error, 1)
	hookWait := make(chan error, 1)
	w := &wrapDeleteAdapter{
		DeleteProjectAdapter: gtd.NewPGDeleteProjectAdapterForTest(fx.store, fx.project),
		beforeSnapshot: func() {
			// delete_project has locked K and now idles in its transaction
			f202DeleteTaskAsync(ctx, fx, tCh)
			_, err := f202WaitFor(probe, f202WaitsOnIdleTx)
			hookWait <- err
		},
	}
	dRes := f202Await(t, f202DeleteProjectAsync(ctx, fx, w), "delete_project")
	tErr := f202Await(t, tCh, "delete_task")
	t.Logf("F202 result: T=%v D=(%d, %v)", tErr, dRes.n, dRes.err)

	select {
	case err := <-hookWait:
		if err != nil {
			t.Fatalf("probe broken: delete_task never waited on delete_project's lock: %v", err)
		}
	default:
		t.Fatal("probe broken: the beforeSnapshot hook never ran")
	}
	f202Verify(t, fx, tErr, dRes)
	if tErr != nil {
		t.Errorf("delete_task = %v, want nil (K was already deleted by delete_project)", tErr)
	}
	if dRes.err != nil || dRes.n != 1 {
		t.Errorf("delete_project = (%d, %v), want (1, nil)", dRes.n, dRes.err)
	}
}

// f202Holder is a transaction on its own connection that holds one row lock.
type f202Holder struct {
	tx  pgx.Tx
	pid int32
}

// f202HoldCandidate locks K's completion_candidates row, which both deletes clean.
func f202HoldCandidate(t *testing.T, fx *f202Fixture) *f202Holder {
	t.Helper()
	ctx := context.Background()
	tx, err := f202Connect(t, fx.pool).Begin(ctx)
	if err != nil {
		t.Fatalf("probe broken: begin the holding transaction: %v", err)
	}
	h := &f202Holder{tx: tx}
	t.Cleanup(func() { h.release(t) })
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM completion_candidates WHERE id = $1 FOR UPDATE`, fx.refs.candidate).Scan(&id); err != nil {
		t.Fatalf("probe broken: lock the candidate row: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&h.pid); err != nil {
		t.Fatalf("probe broken: read the holder's pid: %v", err)
	}
	return h
}

func (h *f202Holder) release(t *testing.T) {
	t.Helper()
	if err := h.tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Logf("roll back the holding transaction: %v", err)
	}
}

// [F202-02] Guards F202-01 for the interleaving where delete_task reaches the
// reference cleanup first: it is parked there on a row the holder locks, and
// delete_project then starts. Without the F202-01 lock, delete_project takes K
// and waits on the reference rows delete_task already cleaned, while
// delete_task then wants K: SQLSTATE 40P01. With it, delete_project queues at
// its own lock on K and, once K is gone, refuses with ErrProjectTasksChanged.
func TestDeleteTaskVsDeleteProject_PGTaskFirst(t *testing.T) {
	fx := newF202Fixture(t, "f202-b")
	ctx := f202Context(t)
	probe := f202Connect(t, fx.pool)
	hold := f202HoldCandidate(t, fx)

	tCh := make(chan error, 1)
	f202DeleteTaskAsync(ctx, fx, tCh)
	var dCh <-chan f202Result
	// settle lets both deletes finish once the holder is gone, so a broken probe leaves nothing running
	settle := func(msg string, err error) {
		hold.release(t)
		if tErr := f202Await(t, tCh, "delete_task"); tErr != nil {
			t.Logf("delete_task ended with: %v", tErr)
		}
		if dCh != nil {
			d := f202Await(t, dCh, "delete_project")
			t.Logf("delete_project ended with: (%d, %v)", d.n, d.err)
		}
		t.Fatalf("probe broken: %s: %v", msg, err)
	}

	tPID, err := f202WaitFor(probe, f202WaitsOnPID, hold.pid)
	if err != nil {
		settle("delete_task never waited on the held candidate row", err)
	}
	dCh = f202DeleteProjectAsync(ctx, fx, gtd.NewPGDeleteProjectAdapterForTest(fx.store, fx.project))
	if _, err := f202WaitFor(probe, f202WaitsOnPID, tPID); err != nil {
		settle("delete_project never waited on delete_task", err)
	}
	hold.release(t)

	tErr := f202Await(t, tCh, "delete_task")
	dRes := f202Await(t, dCh, "delete_project")
	t.Logf("F202 result: T=%v D=(%d, %v)", tErr, dRes.n, dRes.err)

	f202Verify(t, fx, tErr, dRes)
	if tErr != nil {
		t.Errorf("delete_task = %v, want nil", tErr)
	}
	if !errors.Is(dRes.err, gtd.ErrProjectTasksChanged) {
		t.Errorf("delete_project = (%d, %v), want ErrProjectTasksChanged (K was deleted before it locked)", dRes.n, dRes.err)
	}
}

// [F202-03] Guards the READ COMMITTED pin in pgDeleteTaskAdapter.BeginTx. The
// server default is REPEATABLE READ and delete_project holds K, so delete_task
// queues behind it. Once delete_project commits, a delete_task left at the
// server default fails with SQLSTATE 40001 ("could not serialize access");
// pinned to READ COMMITTED it finds K gone and is a quiet no-op, as in
// TestDeleteTaskVsDeleteProject_PGProjectFirst.
func TestDeleteTaskVsDeleteProject_PGProjectFirstRepeatableRead(t *testing.T) {
	const rrLevel = "repeatable read"
	base := openTestPgPool(t)
	ctx := f202Context(t)
	cfg := base.Config()
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["default_transaction_isolation"] = rrLevel
	rr, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("probe broken: NewWithConfig: %v", err)
	}
	t.Cleanup(rr.Close)
	var level string
	if err := rr.QueryRow(ctx, `SHOW default_transaction_isolation`).Scan(&level); err != nil || level != rrLevel {
		t.Fatalf("probe broken: rr pool default_transaction_isolation = %q (%v), want %s", level, err, rrLevel)
	}

	// newF202Fixture asserts a read committed pool, so build the fixture by hand:
	// the store, project and task live on the rr pool, the seeds and checks use base
	ws := uuid.New()
	store := newPgGTDStore(rr, &ws)
	project := mustProjectID(t, store, "f202-rr-"+ws.String())
	task := mustTaskID(t, store, project, "K")
	refs := seedTaskRefs(t, base, ws, task)
	fx := &f202Fixture{pool: base, store: store, project: project, task: task, refs: refs, preview: previewDigest(t, store, project)}
	probe := f202Connect(t, base)

	tCh := make(chan error, 1)
	hookWait := make(chan error, 1)
	w := &wrapDeleteAdapter{
		DeleteProjectAdapter: gtd.NewPGDeleteProjectAdapterForTest(fx.store, fx.project),
		beforeSnapshot: func() {
			// delete_project has locked K and now idles in its transaction
			f202DeleteTaskAsync(ctx, fx, tCh)
			_, err := f202WaitFor(probe, f202WaitsOnIdleTx)
			hookWait <- err
		},
	}
	dRes := f202Await(t, f202DeleteProjectAsync(ctx, fx, w), "delete_project")
	tErr := f202Await(t, tCh, "delete_task")
	t.Logf("F202 result: T=%v D=(%d, %v)", tErr, dRes.n, dRes.err)

	select {
	case err := <-hookWait:
		if err != nil {
			t.Fatalf("probe broken: delete_task never waited on delete_project's lock: %v", err)
		}
	default:
		t.Fatal("probe broken: the beforeSnapshot hook never ran")
	}
	f202Verify(t, fx, tErr, dRes)
	if tErr != nil {
		t.Errorf("delete_task = %v, want nil (K was already deleted by delete_project)", tErr)
	}
	if dRes.err != nil || dRes.n != 1 {
		t.Errorf("delete_project = (%d, %v), want (1, nil)", dRes.n, dRes.err)
	}
}
