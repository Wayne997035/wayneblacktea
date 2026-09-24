package sqlite

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
)

// [F191-04/05/06/09] the write side of the soft-delete contract
// (PR #191): delete_project / delete_task now snapshot every row they are
// about to remove into deletion_tombstones and write one activity_log audit
// row, both inside the SAME transaction as the delete itself (design 1-3 of
// the soft-delete dispatch). package sqlite (white-box, not sqlite_test) on
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
// the deleted entity's stored name/title text (design 3's redaction rule,
// backend-security-design.md §3.1).
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
