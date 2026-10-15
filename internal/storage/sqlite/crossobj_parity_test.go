package sqlite_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

// xoIterations is how many times each ring is run; a single run proves little for a race.
const xoIterations = 20

// xoSeed is one of the four tables that carry both a project_id and a task reference (the tables the
// Postgres adapters pre-lock, gtd.PGDeleteLockTables). seed takes ?1 id, ?2 project_id, ?3 the task
// reference, ?4 a unique name, ?5 workspace, ?6 timestamp; cleared counts the row only while both of
// its references are NULL, so a row that was deleted and a row that kept a reference both count 0.
type xoSeed struct {
	table   string
	seed    string
	cleared string
}

var xoSeeds = []xoSeed{
	{
		"work_sessions",
		`INSERT INTO work_sessions
			(id, workspace_id, repo_name, project_id, title, goal, status, source,
			 confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		 VALUES (?1,?5,?4,?2,'s','g','in_progress','manual',NULL,?3,?6,?6,?6)`,
		`SELECT count(*) FROM work_sessions WHERE id = ?1 AND project_id IS NULL AND current_task_id IS NULL`,
	},
	{
		"vision_items",
		`INSERT INTO vision_items (id, workspace_id, project_id, title, why_blocked, status, promoted_task_id, created_at)
		 VALUES (?1,?5,?2,?4,'b','promoted',?3,?6)`,
		`SELECT count(*) FROM vision_items WHERE id = ?1 AND project_id IS NULL AND promoted_task_id IS NULL`,
	},
	{
		"decisions",
		`INSERT INTO decisions (id, workspace_id, title, context, decision, rationale, project_id, task_id, created_at)
		 VALUES (?1,?5,?4,'ctx','x','y',?2,?3,?6)`,
		`SELECT count(*) FROM decisions WHERE id = ?1 AND project_id IS NULL AND task_id IS NULL`,
	},
	{
		"knowledge_items",
		`INSERT INTO knowledge_items (id, workspace_id, type, title, content, project_id, task_id, created_at, updated_at)
		 VALUES (?1,?5,'til',?4,'b',?2,?3,?6,?6)`,
		`SELECT count(*) FROM knowledge_items WHERE id = ?1 AND project_id IS NULL AND task_id IS NULL`,
	},
}

// xoRows are the ids of the rows one ring seeded, one per table in xoSeeds.
type xoRows []string

// xoSeedRows plants one row per contested table with project_id = project and the task reference = task.
func xoSeedRows(t *testing.T, d *sqlite.DB, label string, project, task uuid.UUID) xoRows {
	t.Helper()
	ids := make(xoRows, 0, len(xoSeeds))
	for _, s := range xoSeeds {
		id := uuid.New().String()
		mustExec(t, d, s.table+" "+label, s.seed, id, project.String(), task.String(), "xo-"+label+"-"+id, uuid.New().String(), dpTS)
		ids = append(ids, id)
	}
	return ids
}

// xoAssertCleared fails for every seeded row that was deleted or still carries a reference.
func xoAssertCleared(t *testing.T, d *sqlite.DB, ring string, rows xoRows) {
	t.Helper()
	for i, s := range xoSeeds {
		if n := countRows(t, d, s.cleared, rows[i]); n != 1 {
			t.Errorf("%s: %s row %s: found %d rows with both references NULL, want 1 (a cleanup was skipped or the row was deleted)",
				ring, s.table, rows[i], n)
		}
	}
}

// xoAssertGone fails for every id still present in table.
func xoAssertGone(t *testing.T, d *sqlite.DB, ring, what, q string, ids ...uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		if n := countRows(t, d, q, id.String()); n != 0 {
			t.Errorf("%s: %s %s survived the delete", ring, what, id)
		}
	}
}

// xoConcurrently runs a and b in two goroutines and returns both results.
func xoConcurrently[A, B any](a func() A, b func() B) (A, B) {
	var ra A
	var rb B
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); ra = a() }()
	go func() { defer wg.Done(); rb = b() }()
	wg.Wait()
	return ra, rb
}

func xoTask(t *testing.T, store *sqlite.GTDStore, project uuid.UUID, title string) uuid.UUID {
	t.Helper()
	tk, err := store.CreateTask(context.Background(), gtd.CreateTaskParams{ProjectID: &project, Title: title})
	if err != nil {
		t.Fatalf("CreateTask %s: %v", title, err)
	}
	return tk.ID
}

func xoProject(t *testing.T, store *sqlite.GTDStore, name string) uuid.UUID {
	t.Helper()
	p, err := store.CreateProject(context.Background(), gtd.CreateProjectParams{Name: name, Title: name})
	if err != nil {
		t.Fatalf("CreateProject %s: %v", name, err)
	}
	return p.ID
}

// xoActor is the actor every delete in this file is attributed to.
const xoActor = "tester"

// xoDeleteProject is a delete_project call reduced to (task count, error).
type xoDeleteProject struct {
	n   int
	err error
}

// xoDeleteProjectCall is store.DeleteProject(id) as a function xoConcurrently can run.
func xoDeleteProjectCall(store *sqlite.GTDStore, id uuid.UUID) func() xoDeleteProject {
	return func() xoDeleteProject {
		n, err := store.DeleteProject(context.Background(), id, xoActor)
		return xoDeleteProject{n: n, err: err}
	}
}

// xoRingA: delete_task(K) and delete_project(P) overlap, K is not in P, and rows in all four contested
// tables carry project_id = P and a reference to K.
func xoRingA(t *testing.T, d *sqlite.DB, store *sqlite.GTDStore, i int) {
	t.Helper()
	ctx := context.Background()
	ring := fmt.Sprintf("ring A #%d", i)
	p, q := xoProject(t, store, fmt.Sprintf("xo-a-p-%d", i)), xoProject(t, store, fmt.Sprintf("xo-a-q-%d", i))
	t0, k := xoTask(t, store, p, "T0"), xoTask(t, store, q, "K")
	rows := xoSeedRows(t, d, "a", p, k)

	tErr, dRes := xoConcurrently(
		func() error { return store.DeleteTask(ctx, k, xoActor) },
		xoDeleteProjectCall(store, p),
	)
	if tErr != nil {
		t.Errorf("%s: delete_task = %v, want nil", ring, tErr)
	}
	if dRes.err != nil || dRes.n != 1 {
		t.Errorf("%s: delete_project = (%d, %v), want (1, nil)", ring, dRes.n, dRes.err)
	}
	xoAssertGone(t, d, ring, "task", `SELECT count(*) FROM tasks WHERE id = ?1`, k, t0)
	xoAssertGone(t, d, ring, "project", `SELECT count(*) FROM projects WHERE id = ?1`, p)
	xoAssertCleared(t, d, ring, rows)
}

// xoRingB: delete_project(P1) and delete_project(P2) overlap, and rows in all four contested tables name
// the other project's task: X = (project_id P1, task T2), Y = (project_id P2, task T1).
func xoRingB(t *testing.T, d *sqlite.DB, store *sqlite.GTDStore, i int) {
	t.Helper()
	ring := fmt.Sprintf("ring B #%d", i)
	p1, p2 := xoProject(t, store, fmt.Sprintf("xo-b-p1-%d", i)), xoProject(t, store, fmt.Sprintf("xo-b-p2-%d", i))
	t1, t2 := xoTask(t, store, p1, "T1"), xoTask(t, store, p2, "T2")
	x, y := xoSeedRows(t, d, "bx", p1, t2), xoSeedRows(t, d, "by", p2, t1)

	r1, r2 := xoConcurrently(xoDeleteProjectCall(store, p1), xoDeleteProjectCall(store, p2))
	for name, r := range map[string]xoDeleteProject{"delete_project(P1)": r1, "delete_project(P2)": r2} {
		if r.err != nil || r.n != 1 {
			t.Errorf("%s: %s = (%d, %v), want (1, nil)", ring, name, r.n, r.err)
		}
	}
	xoAssertGone(t, d, ring, "task", `SELECT count(*) FROM tasks WHERE id = ?1`, t1, t2)
	xoAssertGone(t, d, ring, "project", `SELECT count(*) FROM projects WHERE id = ?1`, p1, p2)
	xoAssertCleared(t, d, ring, x)
	xoAssertCleared(t, d, ring, y)
}

// [F1013-25] The two cross-object cycles that deadlock on Postgres (crossobj_deadlock_pg_test.go in
// internal/gtd) cannot happen on SQLite: db.go:166 SetMaxOpenConns(1) lets a delete transaction hold the
// only connection, so two deletes in one process never interleave. Nothing in SQLite is reordered; this
// pins that both backends end in the same state. The row oracle is the database itself (countRows), and
// a row only counts while it survives with both references NULL, so a skipped cleanup and a wrongly
// deleted row both fail.
func TestSQLiteCrossObjectDeletes_ConcurrentEndState(t *testing.T) {
	d, err := sqlite.OpenTemplated(t, context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := sqlite.NewGTDStore(d)

	for i := range xoIterations {
		xoRingA(t, d, store, i)
		xoRingB(t, d, store, i)
	}
}
