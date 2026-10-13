package gtd_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [F1013-21] [F1013-22] Two probes for the cross-object lock cycles between delete_task and
// delete_project (GTD:e9793de1). Both drive production code (Store.DeleteTask and
// Store.DeleteProjectExpecting) against a real Postgres; neither uses an adapter hook.
//
//	ring A: delete_task(K) x delete_project(P), K not in P. delete_task locks work_sessions and
//	        then knowledge_items; delete_project (project_id stage) locks knowledge_items and then work_sessions.
//	ring B: delete_project(P1) x delete_project(P2) over rows that name the other project's tasks.
//	        Each delete locks the other's rows in its task stage and its own in its project stage.
//
// Method (the F202 method): one delete is parked on a row a holder transaction has locked, the second
// delete starts, and pg_stat_activity / pg_blocking_pids prove the second is queued behind the first
// before the holder lets go. The only wait is the poll interval. The cells run one after another:
// f202WaitsOnPID matches any lock waiter in the database. The assertions state the desired
// behaviour, so a cell is red for as long as the cycle exists.
const (
	e979Waiting        = "waiting"
	e979CompletedFirst = "completed-first"
	e979TaskDelete     = "delete_task"
	e979ProjectDelete  = "delete_project"
	e979PruneTable     = "deletion_tombstones"
	e979WorkSessions   = "work_sessions"
	e979Knowledge      = "knowledge_items"
)

// The park rows. $1 = the row's id, $2 = the workspace, $3 = the id it points at.
const (
	e979SeedCandidate = `INSERT INTO completion_candidates (id, workspace_id, task_id, reason, confidence)
		VALUES ($1,$2,$3,'r','high')`
	e979SeedVision = `INSERT INTO vision_items (id, workspace_id, title, why_blocked, status, promoted_task_id)
		VALUES ($1,$2,'v','b','promoted',$3)`
	e979SeedDecision = `INSERT INTO decisions (id, workspace_id, title, context, decision, rationale, project_id, created_at)
		VALUES ($1,$2,'d','ctx','x','y',$3,NOW())`
	e979SeedActivity = `INSERT INTO activity_log (id, workspace_id, actor, action, project_id, notes, created_at)
		VALUES ($1,$2,'tester','parked',$3,'note',NOW())`

	e979LockCandidate = `SELECT id FROM completion_candidates WHERE id = $1 FOR UPDATE`
	e979LockVision    = `SELECT id FROM vision_items WHERE id = $1 FOR UPDATE`
	e979LockDecision  = `SELECT id FROM decisions WHERE id = $1 FOR UPDATE`
	e979LockActivity  = `SELECT id FROM activity_log WHERE id = $1 FOR UPDATE`
)

// The contested rows: a project_id and a task reference on the same row. $1 = id, $2 = workspace,
// $3 = repo_name (work_sessions) or title (knowledge_items), $4 = project_id, $5 = the task reference.
const (
	e979SeedWorkSession = `INSERT INTO work_sessions (id, workspace_id, repo_name, project_id, title, goal, status, source,
		confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,'s','g','in_progress','manual',NULL,$5,NOW(),NOW(),NOW())`
	e979SeedKnowledge = `INSERT INTO knowledge_items (id, workspace_id, type, title, content, project_id, task_id, created_at, updated_at)
		VALUES ($1,$2,'til',$3,'b',$4,$5,NOW(),NOW())`

	e979ReadWorkSession = `SELECT project_id, current_task_id FROM work_sessions WHERE id = $1`
	e979ReadKnowledge   = `SELECT project_id, task_id FROM knowledge_items WHERE id = $1`
	e979CountTasks      = `SELECT count(*) FROM tasks WHERE id = ANY($1::uuid[])`
	e979CountProjects   = `SELECT count(*) FROM projects WHERE id = ANY($1::uuid[])`
)

// e979Park names a cell and the row its holder locks.
type e979Park struct {
	name string // the subtest name, logged as park=
	seed string // inserts the park row
	lock string // locks it by id
}

var e979ParksA = []e979Park{
	{"after-W-candidates", e979SeedCandidate, e979LockCandidate},
	{"at-V-vision", e979SeedVision, e979LockVision},
}

var e979ParksB = []e979Park{
	{"mid-decisions", e979SeedDecision, e979LockDecision},
	{"late-activity_log", e979SeedActivity, e979LockActivity},
}

// e979Outcome is what one delete returned; withCount marks delete_project, which reports a task count.
type e979Outcome struct {
	n         int
	err       error
	withCount bool
}

func (o e979Outcome) String() string {
	if o.withCount {
		return fmt.Sprintf("(%d, %s)", o.n, e979Flat(fmt.Sprint(o.err)))
	}
	return e979Flat(fmt.Sprint(o.err))
}

// e979Flat folds whitespace so a value always stays on the one result line.
func e979Flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// e979Where is the Where of a deadlock error, "" for anything else.
func e979Where(err error) string {
	var pgErr *pgconn.PgError
	if f202IsDeadlock(err) && errors.As(err, &pgErr) {
		return pgErr.Where
	}
	return ""
}

// e979Step is one delete of a cell: its name for the failure log, and the call itself.
type e979Step struct {
	name string
	run  func(context.Context) e979Outcome
}

// e979Run is a started delete and, once collected, what it returned.
type e979Run struct {
	step e979Step
	ch   chan e979Outcome
	out  *e979Outcome
}

func e979Start(ctx context.Context, s e979Step) *e979Run {
	r := &e979Run{step: s, ch: make(chan e979Outcome, 1)}
	go func() { r.ch <- s.run(ctx) }()
	return r
}

func (r *e979Run) collect(t *testing.T) e979Outcome {
	t.Helper()
	if r.out == nil {
		o := f202Await(t, r.ch, r.step.name)
		r.out = &o
	}
	return *r.out
}

// e979Cell is what one interleaving produced.
type e979Cell struct {
	gate        string
	waiterQuery string
	first       e979Outcome
	second      e979Outcome
}

// e979Env is one cell's database: a fresh workspace on the shared pool, plus a probe connection.
type e979Env struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	store *gtd.Store
	ws    uuid.UUID
	probe *pgx.Conn
}

func newE979Env(t *testing.T) *e979Env {
	t.Helper()
	pool := openTestPgPool(t)
	ctx := f202Context(t)
	var level string
	if err := pool.QueryRow(ctx, `SHOW default_transaction_isolation`).Scan(&level); err != nil || level != "read committed" {
		t.Fatalf("probe broken: test pool default_transaction_isolation = %q (%v), want read committed", level, err)
	}
	// Neither delete may block on the other's expired-tombstone prune: it would satisfy the gate without the ring.
	cutoff := time.Now().UTC().Add(-gtd.DeletionTombstoneRetention)
	if _, err := pool.Exec(ctx, `DELETE FROM deletion_tombstones WHERE deleted_at < $1`, cutoff); err != nil {
		t.Fatalf("probe broken: clear expired tombstones: %v", err)
	}
	ws := uuid.New()
	return &e979Env{ctx: ctx, pool: pool, store: newPgGTDStore(pool, &ws), ws: ws, probe: f202Connect(t, pool)}
}

func e979Exec(t *testing.T, env *e979Env, what, q string, args ...any) {
	t.Helper()
	if _, err := env.pool.Exec(context.Background(), q, args...); err != nil {
		t.Fatalf("probe broken: seed %s: %v", what, err)
	}
}

// e979Hold locks one row on its own connection and keeps the transaction open.
func e979Hold(t *testing.T, env *e979Env, lock string, id uuid.UUID) *f202Holder {
	t.Helper()
	ctx := context.Background()
	tx, err := f202Connect(t, env.pool).Begin(ctx)
	if err != nil {
		t.Fatalf("probe broken: begin the holding transaction: %v", err)
	}
	h := &f202Holder{tx: tx}
	t.Cleanup(func() { h.release(t) })
	var locked uuid.UUID
	if err := tx.QueryRow(ctx, lock, id).Scan(&locked); err != nil {
		t.Fatalf("probe broken: lock the park row: %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&h.pid); err != nil {
		t.Fatalf("probe broken: read the holder's pid: %v", err)
	}
	return h
}

// e979Interleave parks first on the holder, starts second, waits for the gate, releases the holder and
// returns both results. A park or gate that does not happen releases the holder, lets every started
// delete finish, logs what each returned, and only then fails: the returned errors are what tells a
// broken probe from the wrong delete failing.
func e979Interleave(t *testing.T, env *e979Env, hold *f202Holder, first, second e979Step) e979Cell {
	t.Helper()
	var runs []*e979Run
	broken := func(msg string) {
		hold.release(t)
		for _, r := range runs {
			t.Logf("%s ended with: %s", r.step.name, r.collect(t))
		}
		t.Fatalf("probe broken: %s", msg)
	}
	r1 := e979Start(env.ctx, first)
	runs = append(runs, r1)
	firstPID, err := f202WaitFor(env.probe, f202WaitsOnPID, hold.pid)
	if err != nil {
		broken(fmt.Sprintf("%s never waited on the held park row: %v", first.name, err))
	}
	r2 := e979Start(env.ctx, second)
	runs = append(runs, r2)
	gate, query := e979Gate(env, firstPID, r2, broken)
	hold.release(t)
	return e979Cell{gate: gate, waiterQuery: query, first: r1.collect(t), second: r2.collect(t)}
}

// e979Gate polls until the second delete waits on the first (waiting) or has already returned
// (completed-first: legal, a lock protocol may make the second delete queue before it holds anything).
func e979Gate(env *e979Env, firstPID int32, second *e979Run, broken func(string)) (gate, query string) {
	ctx, cancel := context.WithTimeout(context.Background(), f202PollLimit)
	defer cancel()
	for {
		var waiter int32
		err := env.probe.QueryRow(ctx, f202WaitsOnPID, firstPID).Scan(&waiter)
		if err == nil {
			return e979Waiting, e979WaiterQuery(ctx, env, waiter, broken)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			broken(fmt.Sprintf("poll for the second delete's wait: %v", err))
		}
		select {
		case out := <-second.ch:
			second.out = &out
			return e979CompletedFirst, ""
		case <-ctx.Done():
			broken(fmt.Sprintf("%s neither waited on the first delete nor returned within %s", second.step.name, f202PollLimit))
		case <-time.After(f202PollEvery):
		}
	}
}

// e979WaiterQuery reads what the waiter is running. A waiter parked on the tombstone prune did not wait
// on the ring, so the gate would be satisfied by the wrong lock.
func e979WaiterQuery(ctx context.Context, env *e979Env, waiter int32, broken func(string)) string {
	var q string
	if err := env.probe.QueryRow(ctx, `SELECT query FROM pg_stat_activity WHERE pid = $1`, waiter).Scan(&q); err != nil {
		broken(fmt.Sprintf("read the waiter's query: %v", err))
	}
	if strings.Contains(q, e979PruneTable) {
		broken("gate satisfied by the tombstone prune: " + e979Flat(q))
	}
	return q
}

// e979CheckOutcome asserts the desired behaviour of one delete: no deadlock, nil, and (for
// delete_project) the expected task count. A deadlock whose Where names none of relations is a
// different cycle from the one under test, and is reported as such.
func e979CheckOutcome(t *testing.T, name string, o e979Outcome, wantN int, relations ...string) {
	t.Helper()
	switch {
	case f202IsDeadlock(o.err):
		t.Errorf("%s was aborted by a deadlock: %v", name, o.err)
		where := e979Where(o.err)
		for _, rel := range relations {
			if strings.Contains(where, rel) {
				return
			}
		}
		t.Errorf("%s: caught a different cycle: Where = %q, want a relation among %v", name, where, relations)
	case o.err != nil:
		t.Errorf("%s returned %v, want nil", name, o.err)
	case o.withCount && o.n != wantN:
		t.Errorf("%s removed %d tasks, want %d", name, o.n, wantN)
	}
}

// e979CheckRefsNull asserts that a contested row survived with both of its references cleared.
func e979CheckRefsNull(t *testing.T, env *e979Env, what, read string, id uuid.UUID) {
	t.Helper()
	var project, task pgtype.UUID
	if err := env.pool.QueryRow(context.Background(), read, id).Scan(&project, &task); err != nil {
		t.Errorf("read %s: %v", what, err)
		return
	}
	if project.Valid || task.Valid {
		t.Errorf("%s still has project_id valid=%v, task reference valid=%v, want both NULL", what, project.Valid, task.Valid)
	}
}

// e979CheckGone asserts that none of the ids is left in the table q counts.
func e979CheckGone(t *testing.T, env *e979Env, what, q string, ids ...uuid.UUID) {
	t.Helper()
	var n int
	if err := env.pool.QueryRow(context.Background(), q, ids).Scan(&n); err != nil {
		t.Errorf("count %s: %v", what, err)
		return
	}
	if n != 0 {
		t.Errorf("%d of the %s are still there", n, what)
	}
}

// [F1013-21] Probe A: W = work_sessions(project_id=P, current_task_id=K), KI = knowledge_items(project_id=P,
// task_id=K). K is not in P.
//
//	after-W-candidates: delete_task is frozen at step 6 (completion_candidates), holding W.
//	at-V-vision:        delete_task is frozen at step 7 (vision_items), holding W; a pre-lock in a different
//	                    table order than delete_task's cannot hide from this point.
func TestE979_RingA_DeleteTaskVsDeleteProject(t *testing.T) {
	for _, pk := range e979ParksA {
		t.Run(pk.name, func(t *testing.T) { e979RunRingA(t, pk) })
	}
}

type e979IDsA struct{ p, t0, k, w, ki uuid.UUID }

func e979RunRingA(t *testing.T, pk e979Park) {
	env := newE979Env(t)
	suffix := env.ws.String()
	p, q := mustProjectID(t, env.store, "e979-a-p-"+suffix), mustProjectID(t, env.store, "e979-a-q-"+suffix)
	ids := e979IDsA{p: p, t0: mustTaskID(t, env.store, p, "T0"), k: mustTaskID(t, env.store, q, "K"), w: uuid.New(), ki: uuid.New()}
	e979Exec(t, env, "work_sessions W", e979SeedWorkSession, ids.w, env.ws, "e979-a-"+suffix, ids.p, ids.k)
	e979Exec(t, env, "knowledge_items KI", e979SeedKnowledge, ids.ki, env.ws, "e979-a-ki", ids.p, ids.k)
	parkID := uuid.New()
	e979Exec(t, env, "park row "+pk.name, pk.seed, parkID, env.ws, ids.k)
	preview := previewDigest(t, env.store, ids.p)
	hold := e979Hold(t, env, pk.lock, parkID)

	cell := e979Interleave(t, env, hold,
		e979Step{e979TaskDelete, func(ctx context.Context) e979Outcome {
			return e979Outcome{err: env.store.DeleteTask(ctx, ids.k, testerActor)}
		}},
		e979Step{e979ProjectDelete, func(ctx context.Context) e979Outcome {
			n, err := env.store.DeleteProjectExpecting(ctx, ids.p, testerActor, preview)
			return e979Outcome{n: n, err: err, withCount: true}
		}})
	t.Logf("E979-A result: park=%s gate=%s T=%s D=%s gate_waiter_query=%q t_deadlock_where=%q d_deadlock_where=%q",
		pk.name, cell.gate, cell.first, cell.second, e979Flat(cell.waiterQuery), e979Where(cell.first.err), e979Where(cell.second.err))

	e979CheckOutcome(t, e979TaskDelete, cell.first, 0, e979WorkSessions, e979Knowledge)
	e979CheckOutcome(t, e979ProjectDelete, cell.second, 1, e979WorkSessions, e979Knowledge)
	e979CheckRefsNull(t, env, "work_sessions W", e979ReadWorkSession, ids.w)
	e979CheckRefsNull(t, env, "knowledge_items KI", e979ReadKnowledge, ids.ki)
	e979CheckGone(t, env, "tasks K and T0", e979CountTasks, ids.k, ids.t0)
	e979CheckGone(t, env, "projects P", e979CountProjects, ids.p)
}

// [F1013-22] Probe B: X = knowledge_items(project_id=P1, task_id=T2), Y = knowledge_items(project_id=P2,
// task_id=T1). The park row has project_id=P1 and no task reference.
//
//	mid-decisions:     delete_project(P1) is frozen at its decisions statement of the project_id stage,
//	                   holding Y, before its knowledge_items statement.
//	late-activity_log: delete_project(P1) is frozen at its activity_log statement; after any pre-lock, so a
//	                   pre-lock is exercised with the second delete waiting.
func TestE979_RingB_DeleteProjectVsDeleteProject(t *testing.T) {
	for _, pk := range e979ParksB {
		t.Run(pk.name, func(t *testing.T) { e979RunRingB(t, pk) })
	}
}

type e979IDsB struct{ p1, p2, t1, t2, x, y uuid.UUID }

func e979RunRingB(t *testing.T, pk e979Park) {
	env := newE979Env(t)
	suffix := env.ws.String()
	p1, p2 := mustProjectID(t, env.store, "e979-b-p1-"+suffix), mustProjectID(t, env.store, "e979-b-p2-"+suffix)
	ids := e979IDsB{
		p1: p1, p2: p2, t1: mustTaskID(t, env.store, p1, "T1"), t2: mustTaskID(t, env.store, p2, "T2"),
		x: uuid.New(), y: uuid.New(),
	}
	e979Exec(t, env, "knowledge_items X", e979SeedKnowledge, ids.x, env.ws, "e979-b-x", ids.p1, ids.t2)
	e979Exec(t, env, "knowledge_items Y", e979SeedKnowledge, ids.y, env.ws, "e979-b-y", ids.p2, ids.t1)
	parkID := uuid.New()
	e979Exec(t, env, "park row "+pk.name, pk.seed, parkID, env.ws, ids.p1)
	pre1, pre2 := previewDigest(t, env.store, ids.p1), previewDigest(t, env.store, ids.p2)
	hold := e979Hold(t, env, pk.lock, parkID)

	deleteProject := func(name string, id uuid.UUID, preview string) e979Step {
		return e979Step{name, func(ctx context.Context) e979Outcome {
			n, err := env.store.DeleteProjectExpecting(ctx, id, testerActor, preview)
			return e979Outcome{n: n, err: err, withCount: true}
		}}
	}
	cell := e979Interleave(t, env, hold,
		deleteProject(e979ProjectDelete+"(P1)", ids.p1, pre1), deleteProject(e979ProjectDelete+"(P2)", ids.p2, pre2))
	t.Logf("E979-B result: park=%s gate=%s D1=%s D2=%s gate_waiter_query=%q d1_deadlock_where=%q d2_deadlock_where=%q",
		pk.name, cell.gate, cell.first, cell.second, e979Flat(cell.waiterQuery), e979Where(cell.first.err), e979Where(cell.second.err))

	e979CheckOutcome(t, e979ProjectDelete+"(P1)", cell.first, 1, e979Knowledge)
	e979CheckOutcome(t, e979ProjectDelete+"(P2)", cell.second, 1, e979Knowledge)
	e979CheckRefsNull(t, env, "knowledge_items X", e979ReadKnowledge, ids.x)
	e979CheckRefsNull(t, env, "knowledge_items Y", e979ReadKnowledge, ids.y)
	e979CheckGone(t, env, "tasks T1 and T2", e979CountTasks, ids.t1, ids.t2)
	e979CheckGone(t, env, "projects P1 and P2", e979CountProjects, ids.p1, ids.p2)
}
