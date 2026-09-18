package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/google/uuid"
)

const dpTS = "2026-09-18T00:00:00.000Z"

// countRows is the oracle for every assertion below: it reads the database
// directly rather than through a store method, so a store that silently
// stopped returning a row cannot make a leftover row look deleted.
func countRows(t *testing.T, d *sqlite.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	return n
}

// The queries below are written out in full rather than assembled from table
// and column names: unqueryvet rejects SQL built by concatenation, and the
// rule is right even here — a literal query is the only kind a reader can
// check against the schema without running the code.
var (
	taskRefQueries = map[string]string{
		"work_session_tasks.task_id": `SELECT count(*) FROM work_session_tasks
			 WHERE task_id IN (?1,?2,?3)`,
		"work_sessions.current_task_id": `SELECT count(*) FROM work_sessions
			 WHERE current_task_id IN (?1,?2,?3)`,
		"decisions.task_id": `SELECT count(*) FROM decisions
			 WHERE task_id IN (?1,?2,?3)`,
		"knowledge_items.task_id": `SELECT count(*) FROM knowledge_items
			 WHERE task_id IN (?1,?2,?3)`,
	}
	projectRefQueries = map[string]string{
		"activity_log":     `SELECT count(*) FROM activity_log WHERE project_id = ?1`,
		"decisions":        `SELECT count(*) FROM decisions WHERE project_id = ?1`,
		"knowledge_items":  `SELECT count(*) FROM knowledge_items WHERE project_id = ?1`,
		"session_handoffs": `SELECT count(*) FROM session_handoffs WHERE project_id = ?1`,
		"work_sessions":    `SELECT count(*) FROM work_sessions WHERE project_id = ?1`,
	}
	rowCountQueries = map[string]string{
		"decisions":        `SELECT count(*) FROM decisions`,
		"activity_log":     `SELECT count(*) FROM activity_log`,
		"session_handoffs": `SELECT count(*) FROM session_handoffs`,
		"work_sessions":    `SELECT count(*) FROM work_sessions`,
	}
)

// mustExec runs one INSERT and names the table if it fails.
func mustExec(t *testing.T, d *sqlite.DB, what, q string, args ...any) {
	t.Helper()
	if err := d.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("insert %s: %v", what, err)
	}
}

// seedProjectReferences populates every table that carries a project_id or a
// task_id pointing into the given project, so the delete has something to
// clean in each one.
func seedProjectReferences(t *testing.T, d *sqlite.DB, projectID uuid.UUID, taskIDs []uuid.UUID) {
	t.Helper()

	sessionID := uuid.New().String()
	mustExec(t, d, "work_session",
		`INSERT INTO work_sessions
			(id, workspace_id, repo_name, project_id, title, goal, status, source,
			 confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		 VALUES (?1,?2,?3,?4,?5,?6,'in_progress','manual',NULL,?7,?8,?8,?8)`,
		sessionID, uuid.New().String(), "demo-repo", projectID.String(),
		"linked-session", "test cascade", taskIDs[0].String(), dpTS)

	mustExec(t, d, "work_session_tasks",
		`INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		 VALUES (?1,?2,'primary',?3)`,
		sessionID, taskIDs[1].String(), dpTS)

	mustExec(t, d, "activity_log",
		`INSERT INTO activity_log (id, workspace_id, actor, action, project_id, notes, created_at)
		 VALUES (?1,?2,'tester','deleted',?3,'note',?4)`,
		uuid.New().String(), uuid.New().String(), projectID.String(), dpTS)

	mustExec(t, d, "decision",
		`INSERT INTO decisions
			(id, workspace_id, title, context, decision, rationale, project_id, task_id, created_at)
		 VALUES (?1,?2,'d','ctx','chose X','because',?3,?4,?5)`,
		uuid.New().String(), uuid.New().String(), projectID.String(), taskIDs[2].String(), dpTS)

	mustExec(t, d, "knowledge_item",
		`INSERT INTO knowledge_items
			(id, workspace_id, type, title, content, project_id, task_id, created_at, updated_at)
		 VALUES (?1,?2,'til','k','body',?3,?4,?5,?5)`,
		uuid.New().String(), uuid.New().String(), projectID.String(), taskIDs[2].String(), dpTS)

	mustExec(t, d, "session_handoff",
		`INSERT INTO session_handoffs (id, workspace_id, repo_name, project_id, intent, created_at)
		 VALUES (?1,?2,'demo-repo',?3,'wrap up',?4)`,
		uuid.New().String(), uuid.New().String(), projectID.String(), dpTS)
}

// TestGTDStore_DeleteProject_RemovesTasksAndClearsEveryReference is the
// SQL-level guard the orchestration unit tests structurally cannot be: those
// run against a fake and prove only the ORDER of the calls. This one proves
// each statement's predicate actually selects what it claims to.
//
// Red line #9 forbids foreign keys, so nothing in the database will complain
// about a reference this code forgets to clear — the row simply keeps
// pointing at an id that no longer exists, and nothing ever reports it. Every
// table that carries a project_id or a task_id is therefore populated here
// and asserted individually.
func TestGTDStore_DeleteProject_RemovesTasksAndClearsEveryReference(t *testing.T) {
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := sqlite.NewGTDStore(d)
	ctx := context.Background()

	doomed, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "doomed", Title: "Doomed"})
	if err != nil {
		t.Fatalf("CreateProject(doomed): %v", err)
	}
	keeper, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "keeper", Title: "Keeper"})
	if err != nil {
		t.Fatalf("CreateProject(keeper): %v", err)
	}

	doomedTasks := make([]uuid.UUID, 0, 3)
	for _, title := range []string{"a", "b", "c"} {
		tk, cErr := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &doomed.ID, Title: title})
		if cErr != nil {
			t.Fatalf("CreateTask(%s): %v", title, cErr)
		}
		doomedTasks = append(doomedTasks, tk.ID)
	}
	// The control: a task under a DIFFERENT project. A delete that dropped
	// the project_id predicate would take this one too, and every assertion
	// about the doomed rows would still pass.
	survivor, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &keeper.ID, Title: "survivor"})
	if err != nil {
		t.Fatalf("CreateTask(survivor): %v", err)
	}

	seedProjectReferences(t, d, doomed.ID, doomedTasks)

	deleted, err := store.DeleteProject(ctx, doomed.ID)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if deleted != len(doomedTasks) {
		t.Errorf("DeleteProject reported %d tasks removed, want %d", deleted, len(doomedTasks))
	}

	assertDoomedRowsGone(t, d, doomed.ID, keeper.ID, survivor.ID)
	assertNoDanglingRefs(t, d, doomed.ID, doomedTasks)
	assertReferencingRowsSurvive(t, d)
}

// assertDoomedRowsGone checks the two DELETEs, plus the reverse control that
// they were not a blanket wipe.
func assertDoomedRowsGone(t *testing.T, d *sqlite.DB, doomedID, keeperID, survivorID uuid.UUID) {
	t.Helper()
	if n := countRows(t, d, `SELECT count(*) FROM projects WHERE id = ?1`, doomedID.String()); n != 0 {
		t.Errorf("project row survived the delete (%d rows)", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM tasks WHERE project_id = ?1`, doomedID.String()); n != 0 {
		t.Errorf("%d task(s) under the deleted project survived", n)
	}
	if n := countRows(t, d, `SELECT count(*) FROM tasks WHERE id = ?1`, survivorID.String()); n != 1 {
		t.Error("a task belonging to ANOTHER project was deleted — the project_id predicate is not doing its job")
	}
	if n := countRows(t, d, `SELECT count(*) FROM projects WHERE id = ?1`, keeperID.String()); n != 1 {
		t.Error("the other project was deleted too")
	}
}

// assertNoDanglingRefs is one assertion per referencing column — the same list
// DeleteProjectAdapter's method set enumerates, so a column added there
// without a cleanup shows up here as a surviving reference.
func assertNoDanglingRefs(t *testing.T, d *sqlite.DB, doomedID uuid.UUID, taskIDs []uuid.UUID) {
	t.Helper()
	for col, q := range taskRefQueries {
		n := countRows(t, d, q, taskIDs[0].String(), taskIDs[1].String(), taskIDs[2].String())
		if n != 0 {
			t.Errorf("%s: %d dangling reference(s) to a deleted task", col, n)
		}
	}
	for table, q := range projectRefQueries {
		if n := countRows(t, d, q, doomedID.String()); n != 0 {
			t.Errorf("%s.project_id: %d dangling reference(s) to the deleted project", table, n)
		}
	}
}

// assertReferencingRowsSurvive pins the other half of the contract: those rows
// are CLEARED, not deleted. A decision or a log entry outlives the project it
// was filed under.
func assertReferencingRowsSurvive(t *testing.T, d *sqlite.DB) {
	t.Helper()
	for table, q := range rowCountQueries {
		if n := countRows(t, d, q); n != 1 {
			t.Errorf("%s row count = %d, want 1 — the row was deleted instead of having its refs cleared",
				table, n)
		}
	}
}

// TestGTDStore_DeleteProject_UnknownProjectIsNoOp pins the same contract the
// orchestration test states, but through the real SQL: an id that is not
// there must leave the database untouched and report zero, so a repeated
// delete is quiet rather than destructive or noisy.
func TestGTDStore_DeleteProject_UnknownProjectIsNoOp(t *testing.T) {
	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := sqlite.NewGTDStore(d)
	ctx := context.Background()

	p, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "keeper", Title: "Keeper"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &p.ID, Title: "keep me"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	n, err := store.DeleteProject(ctx, uuid.New())
	if err != nil {
		t.Fatalf("deleting an unknown project must not error, got %v", err)
	}
	if n != 0 {
		t.Errorf("reported %d tasks deleted for a project that does not exist", n)
	}
	if got := countRows(t, d, `SELECT count(*) FROM tasks`); got != 1 {
		t.Errorf("tasks table has %d rows after a no-op delete, want 1", got)
	}
	if got := countRows(t, d, `SELECT count(*) FROM projects`); got != 1 {
		t.Errorf("projects table has %d rows after a no-op delete, want 1", got)
	}
}

// TestGTDStore_DeleteProject_OtherWorkspaceIsNoOp is the workspace-scoping
// guard. The pre-check is the only thing standing between a scoped store and
// another workspace's data.
func TestGTDStore_DeleteProject_OtherWorkspaceIsNoOp(t *testing.T) {
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// File-backed, not ":memory:" — the two workspace-scoped handles must see
	// the SAME rows, and every ":memory:" open is a separate empty database.
	path := filepath.Join(t.TempDir(), "ws.db")
	ctx := context.Background()

	dA, err := sqlite.Open(ctx, path, wsA)
	if err != nil {
		t.Fatalf("sqlite.Open(A): %v", err)
	}
	t.Cleanup(func() { _ = dA.Close() })
	dB, err := sqlite.Open(ctx, path, wsB)
	if err != nil {
		t.Fatalf("sqlite.Open(B): %v", err)
	}
	t.Cleanup(func() { _ = dB.Close() })

	storeA := sqlite.NewGTDStore(dA)
	p, err := storeA.CreateProject(ctx, gtd.CreateProjectParams{Name: "a-project", Title: "A"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := storeA.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &p.ID, Title: "a-task"}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// A decision filed under that project. This row is what makes the test
	// bite: the five project_id cleanups deliberately carry NO workspace
	// predicate (see execByProject), so the pre-check is the ONLY thing
	// stopping another workspace's delete from blanking this column. Every
	// other statement in the flow is workspace-scoped in its own right, so
	// asserting only on the surviving project and task passes even with the
	// pre-check's workspace condition removed — measured, not assumed.
	decisionID := uuid.New().String()
	mustExec(t, dA, "decision",
		`INSERT INTO decisions
			(id, workspace_id, title, context, decision, rationale, project_id, created_at)
		 VALUES (?1,?2,'d','ctx','chose X','because',?3,?4)`,
		decisionID, wsA, p.ID.String(), dpTS)

	// A store scoped to a different workspace, over the same database file.
	storeB := sqlite.NewGTDStore(dB)

	n, err := storeB.DeleteProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("cross-workspace delete must be a quiet no-op, got error %v", err)
	}
	if n != 0 {
		t.Errorf("a store scoped to another workspace reported %d tasks deleted", n)
	}
	if got := countRows(t, dA, `SELECT count(*) FROM projects WHERE id = ?1`, p.ID.String()); got != 1 {
		t.Error("a store scoped to another workspace deleted this workspace's project")
	}
	if got := countRows(t, dA, `SELECT count(*) FROM tasks`); got != 1 {
		t.Errorf("tasks table has %d rows, want 1 — another workspace's delete reached them", got)
	}
	if got := countRows(t, dA,
		`SELECT count(*) FROM decisions WHERE id = ?1 AND project_id = ?2`,
		decisionID, p.ID.String()); got != 1 {
		t.Error("another workspace's delete blanked this workspace's decisions.project_id — " +
			"the pre-check let the un-scoped project cleanups run")
	}

	// Positive control: the same call from the OWNING workspace must work,
	// or the assertions above would also pass with a DeleteProject that
	// never deletes anything at all.
	n, err = storeA.DeleteProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("owning workspace DeleteProject: %v", err)
	}
	if n != 1 {
		t.Errorf("owning workspace reported %d tasks deleted, want 1", n)
	}
	if got := countRows(t, dA, `SELECT count(*) FROM projects WHERE id = ?1`, p.ID.String()); got != 0 {
		t.Error("the owning workspace could not delete its own project — the probe above proves nothing")
	}
}
