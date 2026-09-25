package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
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
		// [F191-01] vision_items.project_id and procedural_memories.project_id
		// were missed by DeleteProjectAdapter from the start — caught by a
		// full-repo security review (DBI-FULL0923-01), not by re-reading the
		// hand-written reference list above these two entries were added to.
		// F191-02's machine-derived table list exists so the next such gap
		// is caught mechanically instead.
		"vision_items":        `SELECT count(*) FROM vision_items WHERE project_id = ?1`,
		"procedural_memories": `SELECT count(*) FROM procedural_memories WHERE project_id = ?1`,
	}
	rowCountQueries = map[string]string{
		"decisions":           `SELECT count(*) FROM decisions`,
		"activity_log":        `SELECT count(*) FROM activity_log`,
		"session_handoffs":    `SELECT count(*) FROM session_handoffs`,
		"work_sessions":       `SELECT count(*) FROM work_sessions`,
		"vision_items":        `SELECT count(*) FROM vision_items`,
		"procedural_memories": `SELECT count(*) FROM procedural_memories`,
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

	// [F191-01] vision_items.project_id and procedural_memories.project_id:
	// the two references delete_project silently left dangling until this
	// fix. why_blocked is vision_items' own NOT NULL column, unrelated to
	// promoted_task_id (a separate reference, already cleaned by the
	// pre-existing ResetPromotedVisionItems and not exercised here).
	mustExec(t, d, "vision_item",
		`INSERT INTO vision_items (id, workspace_id, project_id, title, why_blocked, created_at)
		 VALUES (?1,?2,?3,'v','blocked on x',?4)`,
		uuid.New().String(), uuid.New().String(), projectID.String(), dpTS)

	mustExec(t, d, "procedural_memory",
		`INSERT INTO procedural_memories (id, workspace_id, project_id, title, created_at)
		 VALUES (?1,?2,?3,'pm',?4)`,
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
	t.Parallel()                                                            // [F0925-10]
	d, err := sqlite.OpenTemplated(t, context.Background(), ":memory:", "") // [F0925-09] semantics-preserving template helper
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

	deleted, err := store.DeleteProject(ctx, doomed.ID, "tester")
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
//
// activity_log wants 2, not 1: [F191-06] DeleteProject now writes its own
// deletion-audit row in the same tx as the delete (design 3), in addition to
// the pre-seeded row this test planted (whose project_id got NULLed, not
// removed) — so a correct delete leaves both behind.
func assertReferencingRowsSurvive(t *testing.T, d *sqlite.DB) {
	t.Helper()
	want := map[string]int{"activity_log": 2}
	for table, q := range rowCountQueries {
		w := want[table]
		if w == 0 {
			w = 1
		}
		if n := countRows(t, d, q); n != w {
			t.Errorf("%s row count = %d, want %d — the row was deleted instead of having its refs cleared (or, "+
				"for activity_log, the delete's own audit row is missing)", table, n, w)
		}
	}
}

// TestGTDStore_DeleteProject_UnknownProjectIsNoOp pins the same contract the
// orchestration test states, but through the real SQL: an id that is not
// there must leave the database untouched and report zero, so a repeated
// delete is quiet rather than destructive or noisy.
func TestGTDStore_DeleteProject_UnknownProjectIsNoOp(t *testing.T) {
	t.Parallel()                                                            // [F0925-10]
	d, err := sqlite.OpenTemplated(t, context.Background(), ":memory:", "") // [F0925-09] semantics-preserving template helper
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

	n, err := store.DeleteProject(ctx, uuid.New(), "tester")
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
	t.Parallel() // [F0925-10]
	wsA := uuid.New().String()
	wsB := uuid.New().String()

	// File-backed, not ":memory:" — the two workspace-scoped handles must see
	// the SAME rows, and every ":memory:" open is a separate empty database.
	path := filepath.Join(t.TempDir(), "ws.db")
	ctx := context.Background()

	dA, err := sqlite.OpenTemplated(t, ctx, path, wsA) // [F0925-09] semantics-preserving template helper
	if err != nil {
		t.Fatalf("sqlite.Open(A): %v", err)
	}
	t.Cleanup(func() { _ = dA.Close() })
	dB, err := sqlite.OpenTemplated(t, ctx, path, wsB) // [F0925-09] semantics-preserving template helper
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

	n, err := storeB.DeleteProject(ctx, p.ID, "tester")
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
	n, err = storeA.DeleteProject(ctx, p.ID, "tester")
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

// projectIDCheckQueries [F191-02] extends projectRefQueries (above, used by
// the F191-01 test) with the one project_id-bearing table it doesn't cover:
// tasks itself. tasks.project_id is never NULLed — the whole row is removed
// by DeleteTaskRows — but "count of rows with project_id = the deleted id"
// is 0 either way, so the same check applies uniformly. Built from
// projectRefQueries rather than duplicating its literals, so this list is
// stored once.
var projectIDCheckQueries = func() map[string]string {
	m := map[string]string{"tasks": `SELECT count(*) FROM tasks WHERE project_id = ?1`}
	for table, q := range projectRefQueries {
		m[table] = q
	}
	return m
}()

// projectIDSeedStatements [F191-02] inserts one row into the given table
// with project_id = the doomed project's id. tasks is deliberately absent —
// it needs store.CreateTask's defaults (status, priority) rather than a bare
// INSERT, so the test loop special-cases it instead of duplicating that
// machinery here. Every statement below shares the same 4-argument shape
// (id, workspace_id, project_id, created_at) so the test loop can call them
// uniformly; written out per table (not built by concatenation) for the same
// unqueryvet reason projectRefQueries is.
var projectIDSeedStatements = map[string]string{
	"activity_log": `INSERT INTO activity_log (id, workspace_id, actor, action, project_id, notes, created_at)
		VALUES (?1,?2,'tester','deleted',?3,'note',?4)`,
	"decisions": `INSERT INTO decisions (id, workspace_id, title, context, decision, rationale, project_id, created_at)
		VALUES (?1,?2,'d','ctx','chose X','because',?3,?4)`,
	"knowledge_items": `INSERT INTO knowledge_items (id, workspace_id, type, title, content, project_id, created_at, updated_at)
		VALUES (?1,?2,'til','k','body',?3,?4,?4)`,
	"procedural_memories": `INSERT INTO procedural_memories (id, workspace_id, project_id, title, created_at)
		VALUES (?1,?2,?3,'pm',?4)`,
	"session_handoffs": `INSERT INTO session_handoffs (id, workspace_id, repo_name, project_id, intent, created_at)
		VALUES (?1,?2,'demo-repo',?3,'wrap up',?4)`,
	"vision_items": `INSERT INTO vision_items (id, workspace_id, project_id, title, why_blocked, created_at)
		VALUES (?1,?2,?3,'v','blocked',?4)`,
	"work_sessions": `INSERT INTO work_sessions
		(id, workspace_id, repo_name, project_id, title, goal, status, source,
		 confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		VALUES (?1,?2,'demo-repo',?3,'s','g','in_progress','manual',NULL,NULL,?4,?4,?4)`,
}

// deriveProjectIDTables [F191-02] mechanically walks the migrated schema —
// sqlite_master for the table list, pragma_table_info(?1) (a table-valued
// function; accepts a bound parameter, so no SQL is ever built by string
// concatenation) for each table's columns — and returns every table with a
// project_id column, minus gtd.ProjectIDCleanupExemptions (design 8), sorted.
// This is deliberately NOT read from golden_schema_test.go's frozen snapshot
// or schema.sql: both are stale by construction (see the dispatch ticket's
// own note that schema.sql lists three tables that don't actually have a
// project_id column in the real migrated schema).
func deriveProjectIDTables(t *testing.T, d *sqlite.DB) []string {
	t.Helper()
	ctx := context.Background()
	conn := d.SqlConn()

	rows, err := conn.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name != 'schema_migrations'`)
	if err != nil {
		t.Fatalf("list tables from sqlite_master: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_master rows: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close sqlite_master rows: %v", err)
	}

	var out []string
	for _, tbl := range tables {
		if gtd.ProjectIDCleanupExemptions[tbl] {
			continue
		}
		var n int
		if err := conn.QueryRowContext(
			ctx,
			`SELECT count(*) FROM pragma_table_info(?1) WHERE name = 'project_id'`, tbl,
		).Scan(&n); err != nil {
			t.Fatalf("pragma_table_info(%s): %v", tbl, err)
		}
		if n > 0 {
			out = append(out, tbl)
		}
	}
	sort.Strings(out)
	return out
}

// sortedKeys returns the sorted keys of a map[string]string.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tableListDiff compares two SORTED string slices and returns a human-
// readable description of any difference, or "" if they match exactly.
// Symmetric: reports entries in derived-but-not-registered AND registered-
// but-not-derived, since both are bugs — a table gone unchecked, or a check
// for a table that no longer has a project_id column.
func tableListDiff(derived, registered []string) string {
	derivedSet := make(map[string]bool, len(derived))
	for _, d := range derived {
		derivedSet[d] = true
	}
	registeredSet := make(map[string]bool, len(registered))
	for _, r := range registered {
		registeredSet[r] = true
	}

	var missing, extra []string
	for _, d := range derived {
		if !registeredSet[d] {
			missing = append(missing, d)
		}
	}
	for _, r := range registered {
		if !derivedSet[r] {
			extra = append(extra, r)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	return fmt.Sprintf("missing from registered=%v extra in registered=%v", missing, extra)
}

// TestDeleteProject_ClearsEveryProjectIDTableFromMigratedSchema is F191-02's
// SQLite half. Unlike TestGTDStore_DeleteProject_RemovesTasksAndClearsEvery
// Reference above (which seeds a hand-picked list of tables), the table list
// this test checks comes from the migrated schema itself. The same gap class
// that let vision_items/procedural_memories go uncleaned until F191-01 (and
// task_id before that, #188) cannot recur here without this test itself
// going red, because the list it checks IS the schema — see the
// reverse_control subtest for proof that claim actually holds.
func TestDeleteProject_ClearsEveryProjectIDTableFromMigratedSchema(t *testing.T) {
	t.Parallel()                                                            // [F0925-10]
	d, err := sqlite.OpenTemplated(t, context.Background(), ":memory:", "") // [F0925-09] semantics-preserving template helper
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	store := sqlite.NewGTDStore(d)
	ctx := context.Background()

	derived := deriveProjectIDTables(t, d)

	// Completeness check: every derived table must have a literal check
	// query AND seed statement registered, or a table with a project_id
	// column could go entirely unchecked by this test without anyone
	// noticing — this is the property the reverse-control subtest exercises.
	if diff := tableListDiff(derived, sortedKeys(projectIDCheckQueries)); diff != "" {
		t.Fatalf("projectIDCheckQueries is out of sync with the migrated schema "+
			"(mechanically derived, minus gtd.ProjectIDCleanupExemptions): %s — register a literal "+
			"query for the missing table, or add it to gtd.ProjectIDCleanupExemptions with a reason", diff)
	}
	seedRegistered := append(sortedKeys(projectIDSeedStatements), "tasks") // tasks is special-cased below
	sort.Strings(seedRegistered)
	if diff := tableListDiff(derived, seedRegistered); diff != "" {
		t.Fatalf("projectIDSeedStatements is out of sync with the migrated schema: %s", diff)
	}

	doomed, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-02-doomed", Title: "Doomed"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for _, tbl := range derived {
		if tbl == "tasks" {
			if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &doomed.ID, Title: "t"}); err != nil {
				t.Fatalf("CreateTask (seeding tasks.project_id): %v", err)
			}
			continue
		}
		q, ok := projectIDSeedStatements[tbl]
		if !ok {
			t.Fatalf("no seed statement registered for table %q — the completeness check above should "+
				"have already caught this", tbl)
		}
		mustExec(t, d, tbl, q, uuid.New().String(), uuid.New().String(), doomed.ID.String(), dpTS)
	}

	if _, err := store.DeleteProject(ctx, doomed.ID, "tester"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	for _, tbl := range derived {
		if n := countRows(t, d, projectIDCheckQueries[tbl], doomed.ID.String()); n != 0 {
			t.Errorf("%s.project_id: %d dangling reference(s) to the deleted project", tbl, n)
		}
	}

	// reverse_control_skipped_table_turns_red proves the completeness check
	// above actually has teeth: simulate a registered-table list that is one
	// entry short of the real, schema-derived list (the exact failure mode
	// #188 and #189/F191-01 both were — a hand-maintained list silently
	// missing a table) and confirm tableListDiff reports it rather than
	// passing.
	t.Run("reverse_control_skipped_table_turns_red", func(t *testing.T) {
		if len(derived) == 0 {
			t.Fatal("derived list is empty — nothing to drop, probe is meaningless")
		}
		short := append([]string(nil), derived[1:]...)
		if diff := tableListDiff(derived, short); diff == "" {
			t.Fatal("dropping a table from the registered list produced no diff — " +
				"the completeness check cannot catch a table that a real implementation forgot to register")
		}
	})
}
