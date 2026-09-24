package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// [F191-04/05/06/09] the write side of the soft-delete contract
// (PR #191): delete_project / delete_task now snapshot every row they are
// about to remove into deletion_tombstones and write one activity_log audit
// row, both inside the SAME transaction as the delete itself. package sqlite
// (white-box, not sqlite_test) on
// purpose: TestSnapshotColumnList_MatchesPragmaTableInfo reads the actual
// production json_object() expressions (sqliteTaskSnapshotJSON /
// sqliteProjectSnapshotJSON), not a hand-copied duplicate of their column
// list — a duplicate would drift exactly the way this test exists to catch.

// openSoftDeleteTestDB is a small helper — no other file in this package's
// white-box (package sqlite) half already exposes an in-memory Open()
// wrapper with t.Cleanup wired in.
func openSoftDeleteTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// scanTombstonePayload reads one deletion_tombstones row's payload as a
// generic map, so tests can assert on individual fields (e.g. "area") the
// way a real restore path would, without depending on db.Task's shape (which
// deliberately omits some columns — the whole reason the snapshot is built
// in SQL rather than from that struct, design 2).
func scanTombstonePayload(t *testing.T, payload string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		t.Fatalf("unmarshal tombstone payload %q: %v", payload, err)
	}
	return m
}

// testerActor is the deletedBy/actor value this file's tests use.
const testerActor = "tester"

// createTasksWithArea creates n tasks under projectID, all with the given
// area, and returns their ids — factored out of
// TestDeleteProject_SnapshotsProjectAndTasksWithArea to keep that test's
// cyclomatic complexity under the project's gocyclo threshold.
func createTasksWithArea(t *testing.T, store *GTDStore, ctx context.Context, projectID uuid.UUID, n int, area string) map[string]bool {
	t.Helper()
	ids := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		tk, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &projectID, Title: "t", Area: area})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		ids[tk.ID.String()] = true
	}
	return ids
}

// assertTaskTombstones reads every task tombstone row belonging to
// deletionID and checks it against taskIDs/projectID/wantDeletedAt/wantArea
// — design 1's "one deletion_id, one shared deleted_at" and design 2's "the
// snapshot carries area" requirements. Returns how many rows it saw, so the
// caller can assert the count separately. Factored out for the same
// gocyclo reason as createTasksWithArea.
func assertTaskTombstones(
	t *testing.T, d *DB, deletionID string, taskIDs map[string]bool, projectID uuid.UUID, wantDeletedAt, wantArea string,
) int {
	t.Helper()
	rows, err := d.SqlConn().QueryContext(context.Background(),
		`SELECT entity_id, project_id, payload, deleted_at
		   FROM deletion_tombstones WHERE deletion_id = ?1 AND entity_kind = 'task'`,
		deletionID)
	if err != nil {
		t.Fatalf("query task tombstones: %v", err)
	}
	defer func() { _ = rows.Close() }()

	seen := 0
	for rows.Next() {
		var entityID, tsProjID, payloadStr, taskDeletedAt string
		if err := rows.Scan(&entityID, &tsProjID, &payloadStr, &taskDeletedAt); err != nil {
			t.Fatalf("scan task tombstone: %v", err)
		}
		seen++
		if !taskIDs[entityID] {
			t.Errorf("tombstone for unexpected task id %s", entityID)
		}
		if tsProjID != projectID.String() {
			t.Errorf("task tombstone %s project_id = %s, want %s", entityID, tsProjID, projectID)
		}
		if taskDeletedAt != wantDeletedAt {
			t.Errorf("task tombstone %s deleted_at = %q, want the SAME value as the project tombstone %q "+
				"(design 1: one deletion_id must share one deleted_at, or the pruner could delete half a group)",
				entityID, taskDeletedAt, wantDeletedAt)
		}
		p := scanTombstonePayload(t, payloadStr)
		if p["area"] != wantArea {
			t.Errorf("task tombstone %s payload[\"area\"] = %v, want %q", entityID, p["area"], wantArea)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task tombstones: %v", err)
	}
	return seen
}

// TestDeleteProject_SnapshotsProjectAndTasksWithArea is F191-04: one project
// tombstone row plus one task tombstone row per task under it, sharing a
// single deletion_id and deleted_at (design 1) — and critically, payload
// carries `area`, which db.Task/db.Project never do (#187's struct
// deliberately omits it), proving the snapshot really is built in SQL and
// not silently reconstructed from the Go type.
func TestDeleteProject_SnapshotsProjectAndTasksWithArea(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-04-doomed", Title: "Doomed"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Non-default area ("wbt") — "unsorted" would hide a Go-struct fallback.
	const wantTasks = 3
	taskIDs := createTasksWithArea(t, store, ctx, proj.ID, wantTasks, "wbt")

	deleted, err := store.DeleteProject(ctx, proj.ID, testerActor)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if deleted != wantTasks {
		t.Fatalf("DeleteProject reported %d tasks removed, want %d", deleted, wantTasks)
	}

	// Project tombstone row.
	var deletionID, tsProjectID, payloadStr, deletedBy, projectDeletedAt string
	row := d.QueryRowContext(ctx,
		`SELECT deletion_id, project_id, payload, deleted_by, deleted_at
		   FROM deletion_tombstones WHERE entity_kind = 'project' AND entity_id = ?1`,
		proj.ID.String())
	if err := row.Scan(&deletionID, &tsProjectID, &payloadStr, &deletedBy, &projectDeletedAt); err != nil {
		t.Fatalf("read project tombstone: %v", err)
	}
	if tsProjectID != proj.ID.String() {
		t.Errorf("project tombstone's project_id = %s, want the deleted project's own id %s "+
			"(design 8: this column is exempt from delete_project's own cleanup sweep so restore_project can find it)",
			tsProjectID, proj.ID)
	}
	if deletedBy != testerActor {
		t.Errorf("deleted_by = %q, want %q", deletedBy, testerActor)
	}
	projectPayload := scanTombstonePayload(t, payloadStr)
	if projectPayload["id"] != proj.ID.String() {
		t.Errorf("project payload id = %v, want %s", projectPayload["id"], proj.ID)
	}

	// Task tombstone rows: same deletion_id, same deleted_at, one per task,
	// each carrying the non-default area.
	seen := assertTaskTombstones(t, d, deletionID, taskIDs, proj.ID, projectDeletedAt, "wbt")
	if seen != wantTasks {
		t.Errorf("got %d task tombstone rows, want %d (== the task count deleted before this delete touched anything)",
			seen, wantTasks)
	}
}

// TestDeleteTask_SnapshotsTaskWithArea is F191-05: a single task tombstone
// row whose project_id is the task's OWN original project (not NULL, not
// some other value) and whose payload carries the non-default area.
func TestDeleteTask_SnapshotsTaskWithArea(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-05-proj", Title: "P"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tk, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: "t", Area: "wbt"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := store.DeleteTask(ctx, tk.ID, testerActor); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	var tsProjectID, payloadStr, deletedBy string
	row := d.QueryRowContext(ctx,
		`SELECT project_id, payload, deleted_by
		   FROM deletion_tombstones WHERE entity_kind = 'task' AND entity_id = ?1`,
		tk.ID.String())
	if err := row.Scan(&tsProjectID, &payloadStr, &deletedBy); err != nil {
		t.Fatalf("read task tombstone: %v", err)
	}
	if tsProjectID != proj.ID.String() {
		t.Errorf("tombstone project_id = %s, want the task's original project %s", tsProjectID, proj.ID)
	}
	if deletedBy != testerActor {
		t.Errorf("deleted_by = %q, want %q", deletedBy, testerActor)
	}
	p := scanTombstonePayload(t, payloadStr)
	if p["area"] != "wbt" {
		t.Errorf(`payload["area"] = %v, want "wbt"`, p["area"])
	}
	if p["id"] != tk.ID.String() {
		t.Errorf(`payload["id"] = %v, want %s`, p["id"], tk.ID)
	}
}

// TestSoftDelete_ActivityLogWrittenInSameTx is F191-06: both delete paths
// leave exactly one new activity_log row, and its notes column never carries
// the deleted entity's stored name/title text, so the audit trail cannot
// leak stored user content back out through a log line.
func TestSoftDelete_ActivityLogWrittenInSameTx(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	countActivityLog := func() int {
		var n int
		if err := d.QueryRowContext(ctx, `SELECT count(*) FROM activity_log`).Scan(&n); err != nil {
			t.Fatalf("count activity_log: %v", err)
		}
		return n
	}

	t.Run("project_deleted", func(t *testing.T) {
		secretName := "Secret Project Codename " + uuid.New().String()[:8]
		proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
			Name: "f191-06-proj-" + uuid.New().String(), Title: secretName,
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		before := countActivityLog()

		if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
			t.Fatalf("DeleteProject: %v", err)
		}
		if got := countActivityLog(); got != before+1 {
			t.Fatalf("activity_log grew by %d, want exactly 1 (the delete's own audit row)", got-before)
		}

		var action, notes string
		row := d.QueryRowContext(ctx,
			`SELECT action, notes FROM activity_log WHERE action = 'project_deleted' ORDER BY created_at DESC LIMIT 1`)
		if err := row.Scan(&action, &notes); err != nil {
			t.Fatalf("read audit row: %v", err)
		}
		if action != "project_deleted" {
			t.Errorf("action = %q, want %q", action, "project_deleted")
		}
		if strings.Contains(notes, secretName) {
			t.Errorf("audit notes leaked the project's stored title: %q", notes)
		}
	})

	t.Run("task_deleted", func(t *testing.T) {
		proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
			Name: "f191-06-proj2-" + uuid.New().String(), Title: "P2",
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		secretTitle := "Secret Task Title " + uuid.New().String()[:8]
		tk, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: secretTitle})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		before := countActivityLog()

		if err := store.DeleteTask(ctx, tk.ID, testerActor); err != nil {
			t.Fatalf("DeleteTask: %v", err)
		}
		if got := countActivityLog(); got != before+1 {
			t.Fatalf("activity_log grew by %d, want exactly 1", got-before)
		}

		var notes string
		row := d.QueryRowContext(ctx,
			`SELECT notes FROM activity_log WHERE action = 'task_deleted' ORDER BY created_at DESC LIMIT 1`)
		if err := row.Scan(&notes); err != nil {
			t.Fatalf("read audit row: %v", err)
		}
		if strings.Contains(notes, secretTitle) {
			t.Errorf("audit notes leaked the task's stored title: %q", notes)
		}
	})
}

// TestLogActivity_RejectsReservedAuditActions is SEC-PR191-02: LogActivity —
// the sole entry point every external caller (MCP log_activity, POST
// /api/activity, PostToolUse, autolog middleware, scheduler, closeout)
// funnels through — must refuse to write a row whose action names one of
// the three values only the delete/restore transactions' own in-tx audit
// writes (this file's TestSoftDelete_ActivityLogWrittenInSameTx, above) may
// produce, including case/whitespace variants, and must not write any row
// when it refuses.
func TestLogActivity_RejectsReservedAuditActions(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	countActivityLog := func() int {
		var n int
		if err := d.QueryRowContext(ctx, `SELECT count(*) FROM activity_log`).Scan(&n); err != nil {
			t.Fatalf("count activity_log: %v", err)
		}
		return n
	}

	cases := []string{
		"project_deleted", "task_deleted", "project_restored",
		"Project_Deleted", " project_deleted ", "TASK_DELETED",
	}
	before := countActivityLog()
	for _, action := range cases {
		t.Run(action, func(t *testing.T) {
			err := store.LogActivity(ctx, testerActor, action, nil, "forged audit row")
			if !errors.Is(err, gtd.ErrReservedAction) {
				t.Fatalf("LogActivity(%q) error = %v, want gtd.ErrReservedAction", action, err)
			}
		})
	}
	if got := countActivityLog(); got != before {
		t.Errorf("activity_log grew by %d row(s) after %d rejected LogActivity calls, want 0", got-before, len(cases))
	}
}

// jsonObjectKeyRe extracts a json_object() call's column-name keys from its
// literal SQL text. Every value in sqliteTaskSnapshotJSON / sqliteProject
// SnapshotJSON is a bare column reference or a json(...) wrapper — never a
// quoted string literal — so every single-quoted token in the expression IS
// a key, in order, with no ambiguity to resolve.
var jsonObjectKeyRe = regexp.MustCompile(`'([a-zA-Z_][a-zA-Z0-9_]*)'`)

func snapshotJSONColumns(expr string) []string {
	matches := jsonObjectKeyRe.FindAllStringSubmatch(expr, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// pragmaTableColumns is the machine-derived source of truth
// TestSnapshotColumnList_MatchesPragmaTableInfo checks the hand-maintained
// json_object() column lists against.
func pragmaTableColumns(t *testing.T, d *DB, table string) []string {
	t.Helper()
	rows, err := d.SqlConn().QueryContext(context.Background(), `SELECT name FROM pragma_table_info(?1)`, table)
	if err != nil {
		t.Fatalf("pragma_table_info(%s): %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan column name: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate pragma_table_info(%s): %v", table, err)
	}
	sort.Strings(out)
	return out
}

// columnListDiff compares two SORTED string slices, reporting entries
// missing from `got` (a column the snapshot expression forgot) AND entries
// extra in `got` (a stale key with no backing column) — same symmetric shape
// as the F191-02 completeness checks (gtd_deleteproject_test.go's
// tableListDiff), rewritten locally because that helper lives in the
// external sqlite_test package this file (package sqlite, for unexported
// const access) cannot reach.
func columnListDiff(got, want []string) string {
	gotSet := make(map[string]bool, len(got))
	for _, c := range got {
		gotSet[c] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, c := range want {
		wantSet[c] = true
	}
	var missing, extra []string
	for _, c := range want {
		if !gotSet[c] {
			missing = append(missing, c)
		}
	}
	for _, c := range got {
		if !wantSet[c] {
			extra = append(extra, c)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	return "missing=" + strings.Join(missing, ",") + " extra=" + strings.Join(extra, ",")
}

// TestSnapshotColumnList_MatchesPragmaTableInfo is F191-09: SQLite has no
// to_jsonb() equivalent, so sqliteTaskSnapshotJSON / sqliteProjectSnapshot
// JSON must name every column by hand — this test is the guard that keeps
// that hand-maintained list honest against the real, migrated schema.
func TestSnapshotColumnList_MatchesPragmaTableInfo(t *testing.T) {
	d := openSoftDeleteTestDB(t)

	t.Run("tasks", func(t *testing.T) {
		want := pragmaTableColumns(t, d, "tasks")
		got := snapshotJSONColumns(sqliteTaskSnapshotJSON)
		if diff := columnListDiff(got, want); diff != "" {
			t.Fatalf("sqliteTaskSnapshotJSON is out of sync with tasks' migrated schema (%s) — "+
				"a column was added to the table without a matching 'name', t.name entry in the snapshot", diff)
		}

		t.Run("reverse_control_missing_column_turns_red", func(t *testing.T) {
			if len(got) == 0 {
				t.Fatal("empty column list — nothing to drop, probe is meaningless")
			}
			short := append([]string(nil), got[1:]...)
			if diff := columnListDiff(short, want); diff == "" {
				t.Fatal("dropping a column from the produced list produced no diff — " +
					"the completeness check cannot catch a column the snapshot expression forgot")
			}
		})
	})

	t.Run("projects", func(t *testing.T) {
		want := pragmaTableColumns(t, d, "projects")
		got := snapshotJSONColumns(sqliteProjectSnapshotJSON)
		if diff := columnListDiff(got, want); diff != "" {
			t.Fatalf("sqliteProjectSnapshotJSON is out of sync with projects' migrated schema (%s)", diff)
		}

		t.Run("reverse_control_missing_column_turns_red", func(t *testing.T) {
			if len(got) == 0 {
				t.Fatal("empty column list — nothing to drop, probe is meaningless")
			}
			short := append([]string(nil), got[1:]...)
			if diff := columnListDiff(short, want); diff == "" {
				t.Fatal("dropping a column from the produced list produced no diff — " +
					"the completeness check cannot catch a column the snapshot expression forgot")
			}
		})
	})
}

// ----- F191-10/11/12/13: RestoreProject / PruneDeletionTombstones -----

// createFullTask creates one task under projectID with a non-default area,
// one checklist item, and one commit sha — the three columns db.Task never
// carries faithfully (design 2), needed so
// TestRestoreProject_RoundTripsEveryColumn actually exercises them.
func createFullTask(t *testing.T, store *GTDStore, ctx context.Context, projectID uuid.UUID, title string) uuid.UUID {
	t.Helper()
	tk, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &projectID, Title: title, Area: "wbt"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.AddChecklistItem(ctx, tk.ID, uuid.UUID{}, gtd.ChecklistItem{
		ID: uuid.New(), Title: "verify it works", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AddChecklistItem: %v", err)
	}
	sha := "deadbeef" + uuid.New().String()[:8]
	if _, err := store.UpdateTask(ctx, tk.ID, gtd.UpdateTaskParams{AppendCommitSHA: &sha}); err != nil {
		t.Fatalf("UpdateTask (AppendCommitSHA): %v", err)
	}
	return tk.ID
}

// readRawRow reads every column of one row via SELECT * — generic on
// purpose: a hand-picked column list here would be exactly the kind of
// silent omission F191-11's round-trip check exists to catch. Values come
// back as the driver's native Go types (int64/string/nil/[]byte); two calls
// through this same helper are safe to compare with reflect.DeepEqual.
func readRawRow(t *testing.T, d *DB, table, id string) map[string]any {
	t.Helper()
	//nolint:unqueryvet // table is always a hardcoded literal ("projects" or
	// "tasks") passed by this same test file's call sites — never external
	// input. SELECT * is intentional: this helper's whole purpose (F191-11)
	// is catching a column restore_project forgot, which an explicit column
	// list would itself risk omitting.
	rows, err := d.SqlConn().QueryContext(context.Background(), `SELECT * FROM `+table+` WHERE id = ?1`, id)
	if err != nil {
		t.Fatalf("query all columns of %s: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns for %s: %v", table, err)
	}
	if !rows.Next() {
		t.Fatalf("no row for id %s in %s", id, table)
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		t.Fatalf("scan %s row: %v", table, err)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s row: %v", table, err)
	}
	out := make(map[string]any, len(cols))
	for i, c := range cols {
		out[c] = vals[i]
	}
	return out
}

// rawRowDiff compares two readRawRow results column-by-column, returning a
// human-readable description of every mismatch or "" if they are identical.
func rawRowDiff(before, after map[string]any) string {
	keys := make([]string, 0, len(before))
	for k := range before {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var diffs []string
	for _, k := range keys {
		b, a := before[k], after[k]
		if !reflect.DeepEqual(b, a) {
			diffs = append(diffs, fmt.Sprintf("%s: before=%v after=%v", k, b, a))
		}
	}
	return strings.Join(diffs, "; ")
}

// TestRestoreProject_RoundTripsEveryColumn is F191-11: delete → restore
// must reproduce the project row and every task row exactly, including the
// columns db.Task/db.Project never carry faithfully (area, checklist,
// commit_shas — design 2), and must consume the tombstone group it used.
func TestRestoreProject_RoundTripsEveryColumn(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: "f191-11-doomed-" + uuid.New().String(), Title: "Doomed", Area: "wbt", RepoName: "wbt-demo",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	const wantTasks = 2
	taskIDs := make([]uuid.UUID, 0, wantTasks)
	for i := 0; i < wantTasks; i++ {
		taskIDs = append(taskIDs, createFullTask(t, store, ctx, proj.ID, fmt.Sprintf("t%d", i)))
	}

	beforeProject := readRawRow(t, d, "projects", proj.ID.String())
	beforeTasks := make(map[string]map[string]any, wantTasks)
	for _, id := range taskIDs {
		beforeTasks[id.String()] = readRawRow(t, d, "tasks", id.String())
	}

	deleted, err := store.DeleteProject(ctx, proj.ID, testerActor)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if deleted != wantTasks {
		t.Fatalf("DeleteProject reported %d tasks removed, want %d", deleted, wantTasks)
	}

	var deletionID string
	if err := d.QueryRowContext(
		ctx,
		`SELECT deletion_id FROM deletion_tombstones WHERE entity_kind = 'project' AND entity_id = ?1`,
		proj.ID.String(),
	).Scan(&deletionID); err != nil {
		t.Fatalf("read deletion_id: %v", err)
	}

	restored, tasksRestored, err := store.RestoreProject(ctx, proj.ID, testerActor)
	if err != nil {
		t.Fatalf("RestoreProject: %v", err)
	}
	if tasksRestored != wantTasks {
		t.Fatalf("tasksRestored = %d, want %d", tasksRestored, wantTasks)
	}
	if restored.ID != proj.ID {
		t.Errorf("restored.ID = %s, want %s", restored.ID, proj.ID)
	}

	if diff := rawRowDiff(beforeProject, readRawRow(t, d, "projects", proj.ID.String())); diff != "" {
		t.Errorf("project row after restore differs from before delete: %s", diff)
	}
	for _, id := range taskIDs {
		if diff := rawRowDiff(beforeTasks[id.String()], readRawRow(t, d, "tasks", id.String())); diff != "" {
			t.Errorf("task %s row after restore differs from before delete: %s", id, diff)
		}
	}

	var remaining int
	if err := d.QueryRowContext(
		ctx,
		`SELECT count(*) FROM deletion_tombstones WHERE deletion_id = ?1`, deletionID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count remaining tombstones: %v", err)
	}
	if remaining != 0 {
		t.Errorf("deletion_tombstones still has %d row(s) for deletion_id %s after restore, want 0", remaining, deletionID)
	}
}

// TestRestoreProject_LeavesEarlierDeleteTaskTombstoneIntact is F191-11's
// core regression guard (three prior review rounds' central concern):
// delete_task(T1) then delete_project(P) (which now only holds T2) produces
// TWO separate deletion_id groups under the same project_id. restore_project
// must write back only T2's group and must not touch T1's earlier,
// unrelated group at all.
func TestRestoreProject_LeavesEarlierDeleteTaskTombstoneIntact(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-11-earlier-" + uuid.New().String(), Title: "P"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t1, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: "t1"})
	if err != nil {
		t.Fatalf("CreateTask t1: %v", err)
	}
	t2, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: "t2"})
	if err != nil {
		t.Fatalf("CreateTask t2: %v", err)
	}

	if err := store.DeleteTask(ctx, t1.ID, testerActor); err != nil {
		t.Fatalf("DeleteTask t1: %v", err)
	}
	var earlierDeletionID string
	if err := d.QueryRowContext(
		ctx,
		`SELECT deletion_id FROM deletion_tombstones WHERE entity_kind = 'task' AND entity_id = ?1`,
		t1.ID.String(),
	).Scan(&earlierDeletionID); err != nil {
		t.Fatalf("read earlier deletion_id: %v", err)
	}

	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	restored, tasksRestored, err := store.RestoreProject(ctx, proj.ID, testerActor)
	if err != nil {
		t.Fatalf("RestoreProject: %v", err)
	}
	if tasksRestored != 1 {
		t.Fatalf("tasksRestored = %d, want 1 (only t2 — t1 was already gone before delete_project ran)", tasksRestored)
	}
	if restored.ID != proj.ID {
		t.Errorf("restored.ID = %s, want %s", restored.ID, proj.ID)
	}

	if got := countRows(t, d, `SELECT count(*) FROM tasks WHERE id = ?1`, t2.ID.String()); got != 1 {
		t.Errorf("t2 not present in tasks after restore, want 1 row")
	}
	if got := countRows(t, d, `SELECT count(*) FROM tasks WHERE id = ?1`, t1.ID.String()); got != 0 {
		t.Errorf("t1 present in tasks after restore, want 0 — t1 belongs to an earlier, separate deletion_id group")
	}
	if got := countRows(t, d, `SELECT count(*) FROM deletion_tombstones WHERE deletion_id = ?1`, earlierDeletionID); got != 1 {
		t.Errorf("earlier delete_task tombstone group has %d row(s) after restore_project, want 1 (untouched)", got)
	}
}

// countRows runs a COUNT(*)-shaped query and returns the scalar result.
func countRows(t *testing.T, d *DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", q, err)
	}
	return n
}

// TestRestoreProject_RefusesWhenIDTaken is F191-11's precheck path: a live
// project already occupies the deleted project's id. Nothing may be
// written, and the tombstone group must survive untouched.
func TestRestoreProject_RefusesWhenIDTaken(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-11-idtaken-" + uuid.New().String(), Title: "P"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	now := nowRFC3339()
	collidingName := "someone-elses-project-" + uuid.New().String()
	if _, err := d.SqlConn().ExecContext(
		ctx,
		`INSERT INTO projects (id, workspace_id, goal_id, name, title, description, status, area, priority, repo_name, created_at, updated_at)
		 VALUES (?1, NULL, NULL, ?2, ?3, NULL, 'active', 'projects', 3, NULL, ?4, ?4)`,
		proj.ID.String(), collidingName, "Someone Else", now,
	); err != nil {
		t.Fatalf("seed id-colliding project: %v", err)
	}

	before := countRows(t, d, `SELECT count(*) FROM deletion_tombstones`)

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrConflict) {
		t.Fatalf("RestoreProject error = %v, want gtd.ErrConflict", err)
	}

	if got := countRows(t, d, `SELECT count(*) FROM deletion_tombstones`); got != before {
		t.Errorf("deletion_tombstones row count changed from %d to %d — a refused restore must not consume the group", before, got)
	}
	var name string
	if err := d.QueryRowContext(ctx, `SELECT name FROM projects WHERE id = ?1`, proj.ID.String()).Scan(&name); err != nil {
		t.Fatalf("read colliding project name: %v", err)
	}
	if name != collidingName {
		t.Errorf("projects.id=%s name = %q, want unchanged %q (a refused restore must not overwrite the row)", proj.ID, name, collidingName)
	}
}

// TestRestoreProject_RefusesWhenNameTaken is F191-11's precheck path via the
// name axis: a different, live project now holds the deleted project's
// name. Nothing may be written.
func TestRestoreProject_RefusesWhenNameTaken(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	sharedName := "f191-11-nametaken-" + uuid.New().String()
	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: sharedName, Title: "P"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: sharedName, Title: "Other"}); err != nil {
		t.Fatalf("CreateProject (name collision): %v", err)
	}

	before := countRows(t, d, `SELECT count(*) FROM deletion_tombstones`)

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrConflict) {
		t.Fatalf("RestoreProject error = %v, want gtd.ErrConflict", err)
	}

	if got := countRows(t, d, `SELECT count(*) FROM deletion_tombstones`); got != before {
		t.Errorf("deletion_tombstones row count changed from %d to %d — a refused restore must not consume the group", before, got)
	}
	if got := countRows(t, d, `SELECT count(*) FROM projects WHERE id = ?1`, proj.ID.String()); got != 0 {
		t.Errorf("a refused restore wrote the project row anyway (found %d)", got)
	}
}

// TestRestoreProject_RefusesWhenTaskIDTaken is F191-11's write-time
// collision path (distinct from the two precheck tests above): the
// project's own id/name are free, so the precheck passes, and only the
// INSERT INTO tasks write-back collides. The whole tx — including the
// project row that INSERT already wrote — must roll back.
func TestRestoreProject_RefusesWhenTaskIDTaken(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-11-taskidtaken-" + uuid.New().String(), Title: "P"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tk, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: "t"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	otherName := "f191-11-taskidtaken-other-" + uuid.New().String()
	otherProj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: otherName, Title: "Other"})
	if err != nil {
		t.Fatalf("CreateProject (other): %v", err)
	}
	now := nowRFC3339()
	if _, err := d.SqlConn().ExecContext(
		ctx,
		`INSERT INTO tasks (id, workspace_id, project_id, title, description, status, priority, importance, context,
		  assignee, due_date, artifact, checklist, kind, branch_name, pr_url, commit_shas, vision_item_id, created_at,
		  updated_at, area)
		 VALUES (?1, NULL, ?2, 'squatter', NULL, 'pending', 3, NULL, NULL, NULL, NULL, NULL, '[]', 'general', NULL,
		  NULL, '[]', NULL, ?3, ?3, 'unsorted')`,
		tk.ID.String(), otherProj.ID.String(), now,
	); err != nil {
		t.Fatalf("seed id-colliding task: %v", err)
	}

	before := countRows(t, d, `SELECT count(*) FROM deletion_tombstones`)

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrConflict) {
		t.Fatalf("RestoreProject error = %v, want gtd.ErrConflict", err)
	}

	if got := countRows(t, d, `SELECT count(*) FROM deletion_tombstones`); got != before {
		t.Errorf("deletion_tombstones row count changed from %d to %d — a refused restore must not consume the group", before, got)
	}
	if got := countRows(t, d, `SELECT count(*) FROM projects WHERE id = ?1`, proj.ID.String()); got != 0 {
		t.Errorf("project row left behind after a task-id-collision rollback, want 0 (got %d)", got)
	}
	var squatterTitle string
	if err := d.QueryRowContext(ctx, `SELECT title FROM tasks WHERE id = ?1`, tk.ID.String()).Scan(&squatterTitle); err != nil {
		t.Fatalf("read squatter task: %v", err)
	}
	if squatterTitle != "squatter" {
		t.Errorf("squatter task row changed, want untouched (got title=%q)", squatterTitle)
	}
}

// TestRestoreProject_OtherWorkspaceIsInvisible is the workspace-scoping
// guard: a store scoped to workspace B must not be able to find (let alone
// restore) a deletion made under workspace A, even over the same database
// file. File-backed, not ":memory:", so the two workspace-scoped handles
// see the same rows.
func TestRestoreProject_OtherWorkspaceIsInvisible(t *testing.T) {
	wsA := uuid.New().String()
	wsB := uuid.New().String()
	path := filepath.Join(t.TempDir(), "restore-ws.db")
	ctx := context.Background()

	dA, err := Open(ctx, path, wsA)
	if err != nil {
		t.Fatalf("Open(A): %v", err)
	}
	t.Cleanup(func() { _ = dA.Close() })
	dB, err := Open(ctx, path, wsB)
	if err != nil {
		t.Fatalf("Open(B): %v", err)
	}
	t.Cleanup(func() { _ = dB.Close() })

	storeA := NewGTDStore(dA)
	storeB := NewGTDStore(dB)

	proj, err := storeA.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-11-wsA-" + uuid.New().String(), Title: "A"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := storeA.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if _, _, err := storeB.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrNotFound) {
		t.Fatalf("RestoreProject from another workspace error = %v, want gtd.ErrNotFound", err)
	}

	// Positive control: the owning workspace must still be able to restore
	// it, proving the ErrNotFound above is workspace-scoping and not some
	// other bug hiding the tombstone from everyone.
	restored, tasksRestored, err := storeA.RestoreProject(ctx, proj.ID, testerActor)
	if err != nil {
		t.Fatalf("owning-workspace RestoreProject: %v", err)
	}
	if restored.ID != proj.ID {
		t.Errorf("restored.ID = %s, want %s", restored.ID, proj.ID)
	}
	if tasksRestored != 0 {
		t.Errorf("tasksRestored = %d, want 0", tasksRestored)
	}
}

// TestRestoreProject_NotFound: no tombstone group exists for this id at all.
func TestRestoreProject_NotFound(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	if _, _, err := store.RestoreProject(ctx, uuid.New(), testerActor); !errors.Is(err, gtd.ErrNotFound) {
		t.Fatalf("RestoreProject error = %v, want gtd.ErrNotFound", err)
	}
}

// TestRestoreProject_WritesAuditRow is F191-12: exactly one new
// activity_log row, action='project_restored', project_id populated with
// the restored project's own id, notes carrying only the deletion id and
// the write-back count — never the project's stored title.
func TestRestoreProject_WritesAuditRow(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	secretTitle := "Secret Restore Project " + uuid.New().String()[:8]
	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-12-proj-" + uuid.New().String(), Title: secretTitle})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	before := countRows(t, d, `SELECT count(*) FROM activity_log`)

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("RestoreProject: %v", err)
	}

	if got := countRows(t, d, `SELECT count(*) FROM activity_log`); got != before+1 {
		t.Fatalf("activity_log grew by %d, want exactly 1", got-before)
	}

	var action, notes, projectIDCol string
	row := d.QueryRowContext(ctx,
		`SELECT action, notes, project_id FROM activity_log WHERE action = 'project_restored' ORDER BY created_at DESC LIMIT 1`)
	if err := row.Scan(&action, &notes, &projectIDCol); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if action != "project_restored" {
		t.Errorf("action = %q, want %q", action, "project_restored")
	}
	if projectIDCol != proj.ID.String() {
		t.Errorf("activity_log.project_id = %s, want %s", projectIDCol, proj.ID)
	}
	if strings.Contains(notes, secretTitle) {
		t.Errorf("audit notes leaked the project's stored title: %q", notes)
	}
	if !strings.Contains(notes, "tasks=0") {
		t.Errorf("notes = %q, want it to carry the write-back count", notes)
	}
}

// TestPruneDeletionTombstones_DropsOlderThanRetention is F191-13: a group
// entirely before cutoff is dropped in full; a group entirely after cutoff
// is left in full. Both groups are 2 rows (one project + one task), proving
// the deleted count is per-row, not per-group, and that a range DELETE
// cannot split a group (design 1: one deletion_id shares one deleted_at).
func TestPruneDeletionTombstones_DropsOlderThanRetention(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	oldProj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-13-old-" + uuid.New().String(), Title: "Old"})
	if err != nil {
		t.Fatalf("CreateProject (old): %v", err)
	}
	if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &oldProj.ID, Title: "old-t"}); err != nil {
		t.Fatalf("CreateTask (old): %v", err)
	}
	if _, err := store.DeleteProject(ctx, oldProj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject (old): %v", err)
	}

	// The "new" group is created/deleted here — BEFORE backdating "old" —
	// so no store.DeleteProject/DeleteTask call happens after the backdate
	// below. SEC-PR191-01's write-path prune (every snapshot method sweeps
	// expired tombstone rows before writing its own) would otherwise treat
	// the backdated "old" group as expired the moment any later delete ran,
	// removing it before this test's own seed-count and explicit-prune
	// assertions get a chance to run.
	newProj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "f191-13-new-" + uuid.New().String(), Title: "New"})
	if err != nil {
		t.Fatalf("CreateProject (new): %v", err)
	}
	if _, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &newProj.ID, Title: "new-t"}); err != nil {
		t.Fatalf("CreateTask (new): %v", err)
	}
	if _, err := store.DeleteProject(ctx, newProj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject (new): %v", err)
	}

	// cutoff sits strictly between the backdated "old" group and the real,
	// just-written "new" group. Backdating "old" is the LAST write this
	// test performs before the explicit PruneDeletionTombstones call.
	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	oldDeletedAt := cutoff.Add(-24 * time.Hour).Format(sqliteTimestampLayout) // 31 days ago
	if _, err := d.SqlConn().ExecContext(
		ctx,
		`UPDATE deletion_tombstones SET deleted_at = ?1 WHERE project_id = ?2`,
		oldDeletedAt, oldProj.ID.String(),
	); err != nil {
		t.Fatalf("backdate old group: %v", err)
	}

	oldRows := countRows(t, d, `SELECT count(*) FROM deletion_tombstones WHERE project_id = ?1`, oldProj.ID.String())
	newRows := countRows(t, d, `SELECT count(*) FROM deletion_tombstones WHERE project_id = ?1`, newProj.ID.String())
	if oldRows != 2 || newRows != 2 {
		t.Fatalf("seed mismatch: oldRows=%d newRows=%d, want 2 and 2", oldRows, newRows)
	}

	deleted, err := store.PruneDeletionTombstones(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneDeletionTombstones: %v", err)
	}
	if deleted != int64(oldRows) {
		t.Errorf("PruneDeletionTombstones returned %d, want %d (the old group's full row count)", deleted, oldRows)
	}

	if got := countRows(t, d, `SELECT count(*) FROM deletion_tombstones WHERE project_id = ?1`, oldProj.ID.String()); got != 0 {
		t.Errorf("old group still has %d row(s) after prune, want 0 (entire group must be dropped)", got)
	}
	if got := countRows(t, d, `SELECT count(*) FROM deletion_tombstones WHERE project_id = ?1`, newProj.ID.String()); got != 2 {
		t.Errorf("new group has %d row(s) after prune, want 2 (untouched)", got)
	}
}

// TestRestoreColumnList_MatchesPragmaTableInfo is F191-10: the hand-
// maintained sqliteProjectRestoreColumns/sqliteTaskRestoreColumns lists
// RestoreProject's write-back query is built from must match the real,
// migrated schema — the SQLite twin of the Postgres backend's
// jsonb_populate_record(...).*  catch-all, which needs no such guard
// because it maps by column name automatically.
func TestRestoreColumnList_MatchesPragmaTableInfo(t *testing.T) {
	d := openSoftDeleteTestDB(t)

	t.Run("tasks", func(t *testing.T) {
		want := pragmaTableColumns(t, d, "tasks")
		got := append([]string(nil), sqliteTaskRestoreColumns...)
		sort.Strings(got)
		if diff := columnListDiff(got, want); diff != "" {
			t.Fatalf("sqliteTaskRestoreColumns is out of sync with tasks' migrated schema (%s) — "+
				"a column was added to the table without a matching entry in the restore write-back list", diff)
		}

		t.Run("reverse_control_missing_column_turns_red", func(t *testing.T) {
			if len(got) == 0 {
				t.Fatal("empty column list — nothing to drop, probe is meaningless")
			}
			short := append([]string(nil), got[1:]...)
			if diff := columnListDiff(short, want); diff == "" {
				t.Fatal("dropping a column from the produced list produced no diff — " +
					"the completeness check cannot catch a column the restore list forgot")
			}
		})
	})

	t.Run("projects", func(t *testing.T) {
		want := pragmaTableColumns(t, d, "projects")
		got := append([]string(nil), sqliteProjectRestoreColumns...)
		sort.Strings(got)
		if diff := columnListDiff(got, want); diff != "" {
			t.Fatalf("sqliteProjectRestoreColumns is out of sync with projects' migrated schema (%s)", diff)
		}

		t.Run("reverse_control_missing_column_turns_red", func(t *testing.T) {
			if len(got) == 0 {
				t.Fatal("empty column list — nothing to drop, probe is meaningless")
			}
			short := append([]string(nil), got[1:]...)
			if diff := columnListDiff(short, want); diff == "" {
				t.Fatal("dropping a column from the produced list produced no diff")
			}
		})
	})
}

// ----- SEC-PR191-01: 30-day retention bound on restore lookup + write-path prune -----

// TestRestoreProject_RefusesOutsideRetention: a tombstone group whose
// deleted_at is older than gtd.DeletionTombstoneRetention (30 days) must
// not be found by restore_project's lookup — same ErrNotFound response as
// "never deleted" — and nothing gets written or consumed.
func TestRestoreProject_RefusesOutsideRetention(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "sec01-outside-" + uuid.New().String(), Title: "P"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tk, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: "t"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	backdated := time.Now().UTC().Add(-31 * 24 * time.Hour).Format(sqliteTimestampLayout)
	if _, err := d.SqlConn().ExecContext(
		ctx,
		`UPDATE deletion_tombstones SET deleted_at = ?1 WHERE project_id = ?2`,
		backdated, proj.ID.String(),
	); err != nil {
		t.Fatalf("backdate group: %v", err)
	}

	beforeTombstones := countRows(t, d, `SELECT count(*) FROM deletion_tombstones WHERE project_id = ?1`, proj.ID.String())

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrNotFound) {
		t.Fatalf("RestoreProject error = %v, want gtd.ErrNotFound (group is 31 days old, outside the 30-day retention window)", err)
	}

	if got := countRows(t, d, `SELECT count(*) FROM deletion_tombstones WHERE project_id = ?1`, proj.ID.String()); got != beforeTombstones {
		t.Errorf("deletion_tombstones row count changed from %d to %d — "+
			"a rejected-as-expired restore must not consume the group", beforeTombstones, got)
	}
	if got := countRows(t, d, `SELECT count(*) FROM projects WHERE id = ?1`, proj.ID.String()); got != 0 {
		t.Errorf("project row was written even though the group is outside the retention window (found %d)", got)
	}
	if got := countRows(t, d, `SELECT count(*) FROM tasks WHERE id = ?1`, tk.ID.String()); got != 0 {
		t.Errorf("task row was written even though the group is outside the retention window (found %d)", got)
	}
}

// TestRestoreProject_AllowsInsideRetention is SEC-PR191-01's positive
// control: a group 29 days old (inside the 30-day window) restores
// normally, proving the boundary isn't rejecting everything.
func TestRestoreProject_AllowsInsideRetention(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "sec01-inside-" + uuid.New().String(), Title: "P"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	backdated := time.Now().UTC().Add(-29 * 24 * time.Hour).Format(sqliteTimestampLayout)
	if _, err := d.SqlConn().ExecContext(
		ctx,
		`UPDATE deletion_tombstones SET deleted_at = ?1 WHERE project_id = ?2`,
		backdated, proj.ID.String(),
	); err != nil {
		t.Fatalf("backdate group: %v", err)
	}

	restored, _, err := store.RestoreProject(ctx, proj.ID, testerActor)
	if err != nil {
		t.Fatalf("RestoreProject: %v, want success (group is 29 days old, inside the 30-day retention window)", err)
	}
	if restored.ID != proj.ID {
		t.Errorf("restored.ID = %s, want %s", restored.ID, proj.ID)
	}
}

// TestSoftDelete_DeletePrunesExpiredTombstones: a delete's snapshot method
// removes tombstone rows that already fell out of the retention window
// before writing its own snapshot, so deletion_tombstones stays bounded
// even when the scheduled pruner hasn't run yet (e.g. the stdio transport,
// which has no pruner wired at all).
func TestSoftDelete_DeletePrunesExpiredTombstones(t *testing.T) {
	d := openSoftDeleteTestDB(t)
	store := NewGTDStore(d)
	ctx := context.Background()

	oldDeletionID := uuid.New().String()
	oldDeletedAt := time.Now().UTC().Add(-31 * 24 * time.Hour).Format(sqliteTimestampLayout)
	if _, err := d.SqlConn().ExecContext(
		ctx,
		`INSERT INTO deletion_tombstones
		   (id, workspace_id, deletion_id, entity_kind, entity_id, project_id, payload, deleted_by, deleted_at)
		 VALUES (?1, NULL, ?2, 'task', ?3, NULL, '{}', 'tester', ?4)`,
		uuid.New().String(), oldDeletionID, uuid.New().String(), oldDeletedAt,
	); err != nil {
		t.Fatalf("seed 31-day-old tombstone: %v", err)
	}

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: "sec01-prune-" + uuid.New().String(), Title: "P"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	tk, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &proj.ID, Title: "t"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := store.DeleteTask(ctx, tk.ID, testerActor); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	if got := countRows(t, d, `SELECT count(*) FROM deletion_tombstones WHERE deletion_id = ?1`, oldDeletionID); got != 0 {
		t.Errorf("31-day-old tombstone group still has %d row(s) after a later delete, want 0 (write-path prune should have removed it)", got)
	}
	if got := countRows(
		t, d,
		`SELECT count(*) FROM deletion_tombstones WHERE entity_kind = 'task' AND entity_id = ?1`, tk.ID.String(),
	); got != 1 {
		t.Errorf("the just-written tombstone for the deleted task is missing, want 1 row")
	}
}
