package gtd_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// [F203-02] Probe R: the one cycle between delete_task and delete_project that the pre-locks leave open, and
// what a deadlock victim leaves behind. The pre-locks lock the rows that match when they run; a row that
// starts matching after both have run (a concurrent start_work_session and log_decision committing W and X)
// is taken later by each delete's own cleanups, in each delete's own order:
//
//	delete_task    (K): work_session_tasks S, then W (work_sessions), then completion_candidates C, then X (decisions)
//	delete_project (P): activity_log A, then X (decisions), then W (work_sessions)
//
// PostgreSQL aborts one of them with SQLSTATE 40P01. The MCP delete tools run the aborted delete again
// (internal/mcp/delete_retry.go, [F203-01]); that is safe only if the victim's transaction rolled back whole
// and the same call then succeeds. This probe holds both to that: exactly one side is the victim, nothing of
// its delete is left, the same call run again succeeds, and the rows it deletes are then there to be counted
// (the same queries, now with positive expectations, show that they read what they claim to).
//
// If the Store ever closes this window, no cell forms the cycle and the probe fails as broken: rewrite it.
// Method as in crossobj_deadlock_pg_test.go: holders park both deletes, pg_blocking_pids proves each step.
const (
	residualSeedSessionTask = `INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		VALUES ($1,$2,'primary',NOW())`
	// $1 = id, $2 = workspace, $3 = project_id, $4 = task_id.
	residualSeedDecision = `INSERT INTO decisions (id, workspace_id, title, context, decision, rationale, project_id, task_id, created_at)
		VALUES ($1,$2,'d','ctx','x','y',$3,$4,NOW())`
	residualLockSessionTask = `SELECT task_id FROM work_session_tasks WHERE session_id = $1 FOR UPDATE`

	residualCountProjects     = `SELECT count(*) FROM projects WHERE id = $1`
	residualCountProjectTasks = `SELECT count(*) FROM tasks WHERE project_id = $1`
	residualCountProjectTombs = `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`
	residualCountActivityOnP  = `SELECT count(*) FROM activity_log WHERE id = $1 AND project_id = $2`
	residualCountDecisionOnP  = `SELECT count(*) FROM decisions WHERE id = $1 AND project_id = $2`
	residualCountTask         = `SELECT count(*) FROM tasks WHERE id = $1`
	residualCountTaskTombs    = `SELECT count(*) FROM deletion_tombstones WHERE entity_kind = 'task' AND entity_id = $1`
)

// residualIDs are the rows of the cycle. P holds T0, K is a task of another project; S, C and A are parked
// on, W and X are the rows that commit after the pre-locks.
type residualIDs struct {
	p, k, sess, cand, act, w, x uuid.UUID
	preview                     string
}

func seedResidual(t *testing.T, env *e979Env) *residualIDs {
	t.Helper()
	suffix := env.ws.String()
	p, q := mustProjectID(t, env.store, "residual-p-"+suffix), mustProjectID(t, env.store, "residual-q-"+suffix)
	mustTaskID(t, env.store, p, "T0")
	ids := &residualIDs{
		p: p, k: mustTaskID(t, env.store, q, "K"),
		sess: uuid.New(), cand: uuid.New(), act: uuid.New(), w: uuid.New(), x: uuid.New(),
	}
	e979Exec(t, env, "work_session_tasks S", residualSeedSessionTask, ids.sess, ids.k)
	e979Exec(t, env, "completion_candidates C", e979SeedCandidate, ids.cand, env.ws, ids.k)
	e979Exec(t, env, "activity_log A", e979SeedActivity, ids.act, env.ws, ids.p)
	ids.preview = previewDigest(t, env.store, p)
	return ids
}

// residualSteps are the two deletes, with the arguments they are run with again after a deadlock.
func residualSteps(env *e979Env, ids *residualIDs) (taskDelete, projectDelete e979Step) {
	taskDelete = e979Step{e979TaskDelete, func(ctx context.Context) e979Outcome {
		return e979Outcome{err: env.store.DeleteTask(ctx, ids.k, testerActor)}
	}}
	projectDelete = e979Step{e979ProjectDelete, func(ctx context.Context) e979Outcome {
		n, err := env.store.DeleteProjectExpecting(ctx, ids.p, testerActor, ids.preview)
		return e979Outcome{n: n, err: err, withCount: true}
	}}
	return taskDelete, projectDelete
}

// residualRing walks the two deletes into the cycle. A step that does not happen releases every holder, lets
// every started delete finish, logs what each returned, and only then fails.
type residualRing struct {
	t     *testing.T
	env   *e979Env
	ids   *residualIDs
	holds []*f202Holder
	runs  []*e979Run
}

func (r *residualRing) broken(msg string) {
	r.t.Helper()
	for _, h := range r.holds {
		h.release(r.t)
	}
	for _, run := range r.runs {
		r.t.Logf("%s ended with: %s", run.step.name, run.collect(r.t))
	}
	r.t.Fatalf("probe broken: %s", msg)
}

func (r *residualRing) hold(lock string, id uuid.UUID) *f202Holder {
	h := e979Hold(r.t, r.env, lock, id)
	r.holds = append(r.holds, h)
	return h
}

func (r *residualRing) start(s e979Step) *e979Run {
	run := e979Start(r.env.ctx, s)
	r.runs = append(r.runs, run)
	return run
}

// waiterOn returns the pid of a backend waiting on pid, or fails the probe with what.
func (r *residualRing) waiterOn(pid int32, what string) int32 {
	r.t.Helper()
	got, err := f202WaitFor(r.env.probe, f202WaitsOnPID, pid)
	if err != nil {
		r.broken(fmt.Sprintf("%s: %v", what, err))
	}
	return got
}

func (r *residualRing) seed(what, q string, args ...any) {
	r.t.Helper()
	if _, err := r.env.pool.Exec(context.Background(), q, args...); err != nil {
		r.broken(fmt.Sprintf("seed %s: %v", what, err))
	}
}

func (r *residualRing) run(taskDelete, projectDelete e979Step) (tOut, dOut e979Outcome) {
	r.t.Helper()
	ids := r.ids
	holdS := r.hold(residualLockSessionTask, ids.sess)
	holdC := r.hold(e979LockCandidate, ids.cand)
	holdA := r.hold(e979LockActivity, ids.act)

	tr := r.start(taskDelete)
	tPID := r.waiterOn(holdS.pid, "delete_task never parked on work_session_tasks")
	dr := r.start(projectDelete)
	dPID := r.waiterOn(holdA.pid, "delete_project never parked on activity_log")

	// Both pre-locks are done. W and X commit now, as a concurrent start_work_session and log_decision would.
	r.seed("work_sessions W", e979SeedWorkSession, ids.w, r.env.ws, "residual-"+r.env.ws.String(), ids.p, ids.k)
	r.seed("decisions X", residualSeedDecision, ids.x, r.env.ws, ids.p, ids.k)

	holdS.release(r.t)
	if got := r.waiterOn(holdC.pid, "delete_task did not re-park on completion_candidates"); got != tPID {
		r.broken(fmt.Sprintf("completion_candidates is awaited by pid %d, want delete_task's pid %d", got, tPID))
	}
	holdA.release(r.t)
	if got := r.waiterOn(tPID, "delete_project did not queue behind delete_task"); got != dPID {
		r.broken(fmt.Sprintf("delete_task is awaited by pid %d, want delete_project's pid %d", got, dPID))
	}
	holdC.release(r.t)
	return tr.collect(r.t), dr.collect(r.t)
}

// residualVictim names the side PostgreSQL aborted with 40P01 and checks that the other side finished.
func residualVictim(t *testing.T, tOut, dOut e979Outcome) string {
	t.Helper()
	tDead, dDead := f202IsDeadlock(tOut.err), f202IsDeadlock(dOut.err)
	if tDead == dDead {
		t.Fatalf("probe broken: residual ring did not form: exactly one side should be the 40P01 victim, got T=%s D=%s. "+
			"If the Store has closed this window, rewrite this probe", tOut, dOut)
	}
	victim, other := e979TaskDelete, dOut
	if dDead {
		victim, other = e979ProjectDelete, tOut
	}
	if other.err != nil || (other.withCount && other.n != 1) {
		t.Errorf("the side that was not the victim returned %s, want success", other)
	}
	return victim
}

// residualClaim is one count the database is asked for.
type residualClaim struct {
	what string
	q    string
	args []any
	want int
}

// residualWant picks the expected count: before is for a victim whose delete rolled back whole, after for the
// same delete run again.
func residualWant(rerun bool, before, after int) int {
	if rerun {
		return after
	}
	return before
}

func residualClaims(victim string, ids *residualIDs, rerun bool) []residualClaim {
	if victim == e979TaskDelete {
		return []residualClaim{
			{"tasks K", residualCountTask, []any{ids.k}, residualWant(rerun, 1, 0)},
			{"task tombstones of K", residualCountTaskTombs, []any{ids.k}, residualWant(rerun, 0, 1)},
		}
	}
	return []residualClaim{
		{"projects P", residualCountProjects, []any{ids.p}, residualWant(rerun, 1, 0)},
		{"tasks of P", residualCountProjectTasks, []any{ids.p}, residualWant(rerun, 1, 0)},
		{"tombstones of P", residualCountProjectTombs, []any{ids.p}, residualWant(rerun, 0, 2)},
		{"activity_log A pointing at P", residualCountActivityOnP, []any{ids.act, ids.p}, residualWant(rerun, 1, 0)},
		{"decisions X pointing at P", residualCountDecisionOnP, []any{ids.x, ids.p}, residualWant(rerun, 1, 0)},
	}
}

// residualCheck asks the database every claim. A query that fails fails the probe: a read that returned
// nothing would otherwise leave a count of 0 that satisfies "no tombstone".
func residualCheck(t *testing.T, env *e979Env, phase string, claims []residualClaim) {
	t.Helper()
	for _, c := range claims {
		var n int
		if err := env.pool.QueryRow(context.Background(), c.q, c.args...).Scan(&n); err != nil {
			t.Fatalf("probe broken: read %s: %v", c.what, err)
		}
		if n != c.want {
			t.Errorf("%s: %s = %d, want %d", phase, c.what, n, c.want)
		}
	}
}

func TestPGDeleteLocks_ResidualRingVictimRollsBackWhole(t *testing.T) {
	env := newE979Env(t)
	ids := seedResidual(t, env)
	taskDelete, projectDelete := residualSteps(env, ids)

	tOut, dOut := (&residualRing{t: t, env: env, ids: ids}).run(taskDelete, projectDelete)
	victim := residualVictim(t, tOut, dOut)
	residualCheck(t, env, "victim, before the rerun", residualClaims(victim, ids, false))

	rerunStep, want := taskDelete, e979Outcome{}
	if victim == e979ProjectDelete {
		rerunStep, want = projectDelete, e979Outcome{n: 1, withCount: true}
	}
	rerun := rerunStep.run(env.ctx)
	if rerun.err != nil || rerun.n != want.n {
		t.Errorf("%s run again with the same arguments returned %s, want %s", victim, rerun, want)
	}
	residualCheck(t, env, "victim, after the rerun", residualClaims(victim, ids, true))
	t.Logf("SE203-R result: victim=%s rerun=%s", victim, rerun)
}
