package gtd_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [F191-02, Postgres half] mirrors the SQLite half's design
// (internal/storage/sqlite/gtd_deleteproject_test.go's
// TestDeleteProject_ClearsEveryProjectIDTableFromMigratedSchema and its doc
// comments) with information_schema.columns as the schema-introspection
// source instead of pragma_table_info, and $N placeholders instead of ?N.
// This is what the dispatch ticket calls out as the actual production gap:
// the SQLite side alone cannot catch a PG-specific adapter method that was
// written as a no-op, only a real query against the real backend can.

// projectIDCheckQueries and projectIDSeedStatements share their exact shape
// with the SQLite twins on purpose (same table set, same 4-argument seed
// order: id, workspace_id, project_id, created_at) so the two files read as
// one design read twice, not two designs that happen to overlap. tasks is
// absent from projectIDSeedStatements for the same reason as the SQLite
// side: it needs store.CreateTask's defaults, not a bare INSERT.
var projectIDCheckQueries = map[string]string{
	"activity_log":        `SELECT count(*) FROM activity_log WHERE project_id = $1`,
	"decisions":           `SELECT count(*) FROM decisions WHERE project_id = $1`,
	"knowledge_items":     `SELECT count(*) FROM knowledge_items WHERE project_id = $1`,
	"procedural_memories": `SELECT count(*) FROM procedural_memories WHERE project_id = $1`,
	"session_handoffs":    `SELECT count(*) FROM session_handoffs WHERE project_id = $1`,
	"tasks":               `SELECT count(*) FROM tasks WHERE project_id = $1`,
	"vision_items":        `SELECT count(*) FROM vision_items WHERE project_id = $1`,
	"work_sessions":       `SELECT count(*) FROM work_sessions WHERE project_id = $1`,
}

var projectIDSeedStatements = map[string]string{
	"activity_log": `INSERT INTO activity_log (id, workspace_id, actor, action, project_id, notes, created_at)
		VALUES ($1,$2,'tester','deleted',$3,'note',$4)`,
	"decisions": `INSERT INTO decisions (id, workspace_id, title, context, decision, rationale, project_id, created_at)
		VALUES ($1,$2,'d','ctx','chose X','because',$3,$4)`,
	"knowledge_items": `INSERT INTO knowledge_items (id, workspace_id, type, title, content, project_id, created_at, updated_at)
		VALUES ($1,$2,'til','k','body',$3,$4,$4)`,
	"procedural_memories": `INSERT INTO procedural_memories (id, workspace_id, project_id, title, created_at)
		VALUES ($1,$2,$3,'pm',$4)`,
	"session_handoffs": `INSERT INTO session_handoffs (id, workspace_id, repo_name, project_id, intent, created_at)
		VALUES ($1,$2,'demo-repo',$3,'wrap up',$4)`,
	"vision_items": `INSERT INTO vision_items (id, workspace_id, project_id, title, why_blocked, created_at)
		VALUES ($1,$2,$3,'v','blocked',$4)`,
	"work_sessions": `INSERT INTO work_sessions
		(id, workspace_id, repo_name, project_id, title, goal, status, source,
		 confirmed_plan_id, current_task_id, started_at, created_at, updated_at)
		VALUES ($1,$2,'demo-repo',$3,'s','g','in_progress','manual',NULL,NULL,$4,$4,$4)`,
}

// derivePGProjectIDTables mechanically walks the migrated schema via
// information_schema.columns — NEVER golden_schema_test.go's SQLite-only
// snapshot or schema.sql, both of which are stale by construction and PG-
// blind besides — and returns every table with a project_id column, minus
// gtd.ProjectIDCleanupExemptions (design 8), sorted.
func derivePGProjectIDTables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx,
		`SELECT table_name FROM information_schema.columns
		  WHERE table_schema = 'public' AND column_name = 'project_id'`)
	if err != nil {
		t.Fatalf("query information_schema.columns: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table_name: %v", err)
		}
		if gtd.ProjectIDCleanupExemptions[name] {
			continue
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate information_schema.columns rows: %v", err)
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
// but-not-derived — same shape as the SQLite twin in
// internal/storage/sqlite/gtd_deleteproject_test.go, deliberately kept
// side-by-side rather than shared across the package boundary.
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

// TestPGDeleteProject_ClearsEveryProjectIDTableFromMigratedSchema is F191-02's
// Postgres half — the gap three prior review rounds (SV r1-r3 per the
// dispatch ticket) flagged: a SQLite-only check cannot catch a PG adapter
// method written as a no-op, because SQLite and Postgres each implement
// their own cleanup SQL independently. Only a real query against a real,
// migrated Postgres schema proves this backend's cleanup is complete too.
func TestPGDeleteProject_ClearsEveryProjectIDTableFromMigratedSchema(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	derived := derivePGProjectIDTables(t, pool)

	if diff := tableListDiff(derived, sortedKeys(projectIDCheckQueries)); diff != "" {
		t.Fatalf("projectIDCheckQueries is out of sync with the migrated PG schema "+
			"(mechanically derived, minus gtd.ProjectIDCleanupExemptions): %s — register a literal "+
			"query for the missing table, or add it to gtd.ProjectIDCleanupExemptions with a reason", diff)
	}
	seedRegistered := append(sortedKeys(projectIDSeedStatements), "tasks") // tasks is special-cased below
	sort.Strings(seedRegistered)
	if diff := tableListDiff(derived, seedRegistered); diff != "" {
		t.Fatalf("projectIDSeedStatements is out of sync with the migrated PG schema: %s", diff)
	}

	doomed, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("f191-02-pg-doomed-%s", uuid.New()), Title: "Doomed",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	seedTS := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
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
		if _, err := pool.Exec(ctx, q, uuid.New(), wsID, doomed.ID, seedTS); err != nil {
			t.Fatalf("seed %s: %v", tbl, err)
		}
	}

	if _, err := store.DeleteProject(ctx, doomed.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	for _, tbl := range derived {
		var n int
		if err := pool.QueryRow(ctx, projectIDCheckQueries[tbl], doomed.ID).Scan(&n); err != nil {
			t.Fatalf("check %s: %v", tbl, err)
		}
		if n != 0 {
			t.Errorf("%s.project_id: %d dangling reference(s) to the deleted project", tbl, n)
		}
	}

	// reverse_control_skipped_table_turns_red: same proof-of-teeth as the
	// SQLite twin — a registered-table list one entry short of the real,
	// schema-derived list must make the completeness check fail, not pass.
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

// scanPGTombstonePayload reads one deletion_tombstones.payload (JSONB) as a
// generic map, casting to text in SQL so the driver never has to guess a Go
// destination type for a jsonb column.
func scanPGTombstonePayload(t *testing.T, payloadText string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(payloadText), &m); err != nil {
		t.Fatalf("unmarshal tombstone payload %q: %v", payloadText, err)
	}
	return m
}

// TestPGSoftDelete_DeleteProjectSnapshotsWithArea is F191-04's Postgres half
// — the gap the dispatch ticket calls out explicitly: db.Task never carries
// `area`, `checklist`, or `commit_shas` faithfully enough to prove a Go-
// struct-based snapshot would be correct, so this reads to_jsonb()'s actual
// output straight from the table, the same way restore_project eventually
// will.
// createPGTaskWithChecklistAndSHA creates one task under projectID with a
// non-default area, one checklist item, and one commit sha — the three
// columns TestPGSoftDelete_DeleteProjectSnapshotsWithArea/DeleteTaskSnapshots
// WithArea need present in the snapshot payload to prove it is built from
// to_jsonb(), not from db.Task (which carries none of the three faithfully).
// Factored out to keep those tests' cyclomatic complexity under the
// project's gocyclo threshold.
func createPGTaskWithChecklistAndSHA(t *testing.T, store *gtd.Store, ctx context.Context, wsID, projectID uuid.UUID) uuid.UUID {
	t.Helper()
	tk, err := store.CreateTask(ctx, gtd.CreateTaskParams{ProjectID: &projectID, Title: "t", Area: "wbt"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := store.AddChecklistItem(ctx, tk.ID, wsID, gtd.ChecklistItem{
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

// assertPGTaskTombstones is the Postgres twin of the SQLite package's
// assertTaskTombstones (internal/storage/sqlite/gtd_softdelete_test.go):
// reads every task tombstone row in deletionID and checks it against
// taskIDs/projectID/wantDeletedAt, plus that payload carries a non-empty
// area/checklist/commit_shas (design 2's to_jsonb() promise). Returns how
// many rows it saw.
func assertPGTaskTombstones(
	t *testing.T, pool *pgxpool.Pool, deletionID string, taskIDs map[string]bool, projectID uuid.UUID, wantDeletedAt string,
) int {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT entity_id, project_id, payload::text, deleted_at::text
		   FROM deletion_tombstones WHERE deletion_id = $1 AND entity_kind = 'task'`,
		deletionID)
	if err != nil {
		t.Fatalf("query task tombstones: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var entityID, tsProjID, payloadText, taskDeletedAt string
		if err := rows.Scan(&entityID, &tsProjID, &payloadText, &taskDeletedAt); err != nil {
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
			t.Errorf("task tombstone %s deleted_at = %q, want the SAME value as the project tombstone %q",
				entityID, taskDeletedAt, wantDeletedAt)
		}
		assertPGTaskPayload(t, entityID, scanPGTombstonePayload(t, payloadText))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate task tombstones: %v", err)
	}
	return seen
}

// assertPGTaskPayload checks the three columns db.Task never carries
// faithfully: area, checklist, commit_shas.
func assertPGTaskPayload(t *testing.T, entityID string, p map[string]any) {
	t.Helper()
	if p["area"] != "wbt" {
		t.Errorf("task tombstone %s payload[\"area\"] = %v, want \"wbt\"", entityID, p["area"])
	}
	if checklist, ok := p["checklist"].([]any); !ok || len(checklist) != 1 {
		t.Errorf("task tombstone %s payload[\"checklist\"] = %v, want a 1-item array", entityID, p["checklist"])
	}
	if shas, ok := p["commit_shas"].([]any); !ok || len(shas) != 1 {
		t.Errorf("task tombstone %s payload[\"commit_shas\"] = %v, want a 1-item array", entityID, p["commit_shas"])
	}
}

func TestPGSoftDelete_DeleteProjectSnapshotsWithArea(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("f191-04-pg-doomed-%s", uuid.New()), Title: "Doomed",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	const wantTasks = 2
	taskIDs := make(map[string]bool, wantTasks)
	for i := 0; i < wantTasks; i++ {
		taskIDs[createPGTaskWithChecklistAndSHA(t, store, ctx, wsID, proj.ID).String()] = true
	}

	deleted, err := store.DeleteProject(ctx, proj.ID, testerActor)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if deleted != wantTasks {
		t.Fatalf("DeleteProject reported %d tasks removed, want %d", deleted, wantTasks)
	}

	var deletionID, tsProjectID, projectPayloadText, deletedBy, projectDeletedAt string
	if err := pool.QueryRow(
		ctx,
		`SELECT deletion_id, project_id, payload::text, deleted_by, deleted_at::text
		   FROM deletion_tombstones WHERE entity_kind = 'project' AND entity_id = $1`,
		proj.ID,
	).Scan(&deletionID, &tsProjectID, &projectPayloadText, &deletedBy, &projectDeletedAt); err != nil {
		t.Fatalf("read project tombstone: %v", err)
	}
	if tsProjectID != proj.ID.String() {
		t.Errorf("project tombstone's project_id = %s, want %s (design 8 exemption)", tsProjectID, proj.ID)
	}
	if deletedBy != testerActor {
		t.Errorf("deleted_by = %q, want %q", deletedBy, testerActor)
	}

	seen := assertPGTaskTombstones(t, pool, deletionID, taskIDs, proj.ID, projectDeletedAt)
	if seen != wantTasks {
		t.Errorf("got %d task tombstone rows, want %d", seen, wantTasks)
	}
}

// TestPGSoftDelete_DeleteTaskSnapshotsWithArea is F191-05's Postgres half:
// a single task tombstone whose project_id is the task's own original
// project, payload carrying area/checklist/commit_shas.
func TestPGSoftDelete_DeleteTaskSnapshotsWithArea(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("f191-05-pg-proj-%s", uuid.New()), Title: "P",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	taskID := createPGTaskWithChecklistAndSHA(t, store, ctx, wsID, proj.ID)

	if err := store.DeleteTask(ctx, taskID, testerActor); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	var tsProjectID, payloadText, deletedBy string
	if err := pool.QueryRow(
		ctx,
		`SELECT project_id, payload::text, deleted_by
		   FROM deletion_tombstones WHERE entity_kind = 'task' AND entity_id = $1`,
		taskID,
	).Scan(&tsProjectID, &payloadText, &deletedBy); err != nil {
		t.Fatalf("read task tombstone: %v", err)
	}
	if tsProjectID != proj.ID.String() {
		t.Errorf("tombstone project_id = %s, want the task's original project %s", tsProjectID, proj.ID)
	}
	if deletedBy != testerActor {
		t.Errorf("deleted_by = %q, want %q", deletedBy, testerActor)
	}
	assertPGTaskPayload(t, taskID.String(), scanPGTombstonePayload(t, payloadText))
}

// TestPGSoftDelete_AuditRowWritten is F191-06's Postgres half: both delete
// paths leave exactly one new activity_log row, notes never carrying the
// deleted entity's stored name/title text.
func TestPGSoftDelete_AuditRowWritten(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	countActivityLog := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM activity_log WHERE workspace_id = $1`, wsID).Scan(&n); err != nil {
			t.Fatalf("count activity_log: %v", err)
		}
		return n
	}

	t.Run("project_deleted", func(t *testing.T) {
		secretName := "Secret PG Project " + uuid.New().String()[:8]
		proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
			Name: fmt.Sprintf("f191-06-pg-proj-%s", uuid.New()), Title: secretName,
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		before := countActivityLog()

		if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
			t.Fatalf("DeleteProject: %v", err)
		}
		if got := countActivityLog(); got != before+1 {
			t.Fatalf("activity_log grew by %d, want exactly 1", got-before)
		}

		var action, notes string
		if err := pool.QueryRow(
			ctx,
			`SELECT action, notes FROM activity_log
			  WHERE workspace_id = $1 AND action = 'project_deleted' ORDER BY created_at DESC LIMIT 1`,
			wsID,
		).Scan(&action, &notes); err != nil {
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
			Name: fmt.Sprintf("f191-06-pg-proj2-%s", uuid.New()), Title: "P2",
		})
		if err != nil {
			t.Fatalf("CreateProject: %v", err)
		}
		secretTitle := "Secret PG Task " + uuid.New().String()[:8]
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
		if err := pool.QueryRow(
			ctx,
			`SELECT notes FROM activity_log
			  WHERE workspace_id = $1 AND action = 'task_deleted' ORDER BY created_at DESC LIMIT 1`,
			wsID,
		).Scan(&notes); err != nil {
			t.Fatalf("read audit row: %v", err)
		}
		if strings.Contains(notes, secretTitle) {
			t.Errorf("audit notes leaked the task's stored title: %q", notes)
		}
	})
}

// ----- F191-11/13: RestoreProject / PruneDeletionTombstones, Postgres half -----

// readRawPGRow reads every column of one row via SELECT * — generic on
// purpose, same rationale as the SQLite twin's readRawRow
// (internal/storage/sqlite/gtd_softdelete_test.go): a hand-picked column
// list here would be exactly the kind of silent omission the round-trip
// check exists to catch. pgx's Values() decodes each column with the
// driver's default Go type; two calls through this same helper are safe to
// compare with reflect.DeepEqual.
func readRawPGRow(t *testing.T, pool *pgxpool.Pool, table, id string) map[string]any {
	t.Helper()
	//nolint:unqueryvet // table is always a hardcoded literal ("projects" or
	// "tasks") passed by this same test file's call sites — never external
	// input. SELECT * is intentional: this helper's whole purpose (F191-11)
	// is catching a column restore_project forgot, which an explicit column
	// list would itself risk omitting.
	rows, err := pool.Query(context.Background(), `SELECT * FROM `+table+` WHERE id = $1`, id)
	if err != nil {
		t.Fatalf("query all columns of %s: %v", table, err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("no row for id %s in %s", id, table)
	}
	fields := rows.FieldDescriptions()
	vals, err := rows.Values()
	if err != nil {
		t.Fatalf("read values for %s: %v", table, err)
	}
	out := make(map[string]any, len(fields))
	for i, f := range fields {
		out[f.Name] = vals[i]
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s row: %v", table, err)
	}
	return out
}

// pgRawRowDiff is the Postgres twin of the SQLite package's rawRowDiff.
func pgRawRowDiff(before, after map[string]any) string {
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

// countPGRows runs a COUNT(*)-shaped query and returns the scalar result.
func countPGRows(t *testing.T, pool *pgxpool.Pool, q string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", q, err)
	}
	return n
}

// TestPGSoftDelete_RestoreRoundTripsEveryColumn is F191-11's Postgres half —
// the exact assumption the dispatch ticket flags as unverified:
// jsonb_populate_record(NULL::tasks/projects, payload) must reproduce every
// column (including checklist jsonb and commit_shas TEXT[]) byte-for-byte,
// and the write-back must consume the tombstone group it used.
func TestPGSoftDelete_RestoreRoundTripsEveryColumn(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("f191-11-pg-doomed-%s", uuid.New()), Title: "Doomed", Area: "wbt", RepoName: "wbt-demo",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	const wantTasks = 2
	taskIDs := make([]uuid.UUID, 0, wantTasks)
	for i := 0; i < wantTasks; i++ {
		taskIDs = append(taskIDs, createPGTaskWithChecklistAndSHA(t, store, ctx, wsID, proj.ID))
	}

	beforeProject := readRawPGRow(t, pool, "projects", proj.ID.String())
	beforeTasks := make(map[string]map[string]any, wantTasks)
	for _, id := range taskIDs {
		beforeTasks[id.String()] = readRawPGRow(t, pool, "tasks", id.String())
	}

	deleted, err := store.DeleteProject(ctx, proj.ID, testerActor)
	if err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if deleted != wantTasks {
		t.Fatalf("DeleteProject reported %d tasks removed, want %d", deleted, wantTasks)
	}

	var deletionID uuid.UUID
	if err := pool.QueryRow(
		ctx,
		`SELECT deletion_id FROM deletion_tombstones WHERE entity_kind = 'project' AND entity_id = $1`,
		proj.ID,
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

	if diff := pgRawRowDiff(beforeProject, readRawPGRow(t, pool, "projects", proj.ID.String())); diff != "" {
		t.Errorf("project row after restore differs from before delete: %s", diff)
	}
	for _, id := range taskIDs {
		if diff := pgRawRowDiff(beforeTasks[id.String()], readRawPGRow(t, pool, "tasks", id.String())); diff != "" {
			t.Errorf("task %s row after restore differs from before delete: %s", id, diff)
		}
	}

	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE deletion_id = $1`, deletionID); got != 0 {
		t.Errorf("deletion_tombstones still has %d row(s) for deletion_id %s after restore, want 0", got, deletionID)
	}
}

// TestPGSoftDelete_RestoreLeavesEarlierDeleteTaskTombstoneIntact is
// F191-11's core regression guard, Postgres half — mirrors the SQLite
// twin's TestRestoreProject_LeavesEarlierDeleteTaskTombstoneIntact: an
// earlier, unrelated delete_task group under the same project must survive
// restore_project untouched.
func TestPGSoftDelete_RestoreLeavesEarlierDeleteTaskTombstoneIntact(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("f191-11-pg-earlier-%s", uuid.New()), Title: "P",
	})
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
	var earlierDeletionID uuid.UUID
	if err := pool.QueryRow(
		ctx,
		`SELECT deletion_id FROM deletion_tombstones WHERE entity_kind = 'task' AND entity_id = $1`,
		t1.ID,
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

	if got := countPGRows(t, pool, `SELECT count(*) FROM tasks WHERE id = $1`, t2.ID); got != 1 {
		t.Errorf("t2 not present in tasks after restore, want 1 row")
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM tasks WHERE id = $1`, t1.ID); got != 0 {
		t.Errorf("t1 present in tasks after restore, want 0 — t1 belongs to an earlier, separate deletion_id group")
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE deletion_id = $1`, earlierDeletionID); got != 1 {
		t.Errorf("earlier delete_task tombstone group has %d row(s) after restore_project, want 1 (untouched)", got)
	}
}

// TestPGSoftDelete_RestoreRefusesWhenNameTaken is F191-11's precheck path,
// Postgres half: a different, live project now holds the deleted project's
// name. Nothing may be written, and the tombstone group must survive.
func TestPGSoftDelete_RestoreRefusesWhenNameTaken(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	sharedName := fmt.Sprintf("f191-11-pg-nametaken-%s", uuid.New())
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

	before := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE workspace_id = $1`, wsID)

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrConflict) {
		t.Fatalf("RestoreProject error = %v, want gtd.ErrConflict", err)
	}

	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE workspace_id = $1`, wsID); got != before {
		t.Errorf("deletion_tombstones row count changed from %d to %d — a refused restore must not consume the group", before, got)
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM projects WHERE id = $1`, proj.ID); got != 0 {
		t.Errorf("a refused restore wrote the project row anyway (found %d)", got)
	}
}

// TestPGSoftDelete_RestoreRefusesWhenTaskIDTaken is F191-11's write-time
// collision path, Postgres half (distinct from the precheck test above):
// the project's own id/name are free, so the precheck passes, and only the
// INSERT INTO tasks write-back collides on the task's primary key. The
// whole tx — including the project row that INSERT already wrote — must
// roll back.
func TestPGSoftDelete_RestoreRefusesWhenTaskIDTaken(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("f191-11-pg-taskidtaken-%s", uuid.New()), Title: "P",
	})
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

	otherProj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("f191-11-pg-taskidtaken-other-%s", uuid.New()), Title: "Other",
	})
	if err != nil {
		t.Fatalf("CreateProject (other): %v", err)
	}
	// Raw INSERT (not store.CreateTask, which always generates its own id):
	// a different, unrelated task now occupies the deleted task's id, so the
	// projects-only precheck passes and only the tasks write-back collides.
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO tasks (id, workspace_id, project_id, title) VALUES ($1, $2, $3, 'squatter')`,
		tk.ID, wsID, otherProj.ID,
	); err != nil {
		t.Fatalf("seed id-colliding task: %v", err)
	}

	before := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE workspace_id = $1`, wsID)

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrConflict) {
		t.Fatalf("RestoreProject error = %v, want gtd.ErrConflict", err)
	}

	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE workspace_id = $1`, wsID); got != before {
		t.Errorf("deletion_tombstones row count changed from %d to %d — a refused restore must not consume the group", before, got)
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM projects WHERE id = $1`, proj.ID); got != 0 {
		t.Errorf("project row left behind after a task-id-collision rollback, want 0 (got %d)", got)
	}
	var squatterTitle string
	if err := pool.QueryRow(ctx, `SELECT title FROM tasks WHERE id = $1`, tk.ID).Scan(&squatterTitle); err != nil {
		t.Fatalf("read squatter task: %v", err)
	}
	if squatterTitle != "squatter" {
		t.Errorf("squatter task row changed, want untouched (got title=%q)", squatterTitle)
	}
}

// TestPGSoftDelete_PruneDropsOlderThanRetention is F191-13's Postgres half:
// a group entirely before cutoff is dropped in full; a group entirely after
// cutoff is left in full.
func TestPGSoftDelete_PruneDropsOlderThanRetention(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	oldProj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: fmt.Sprintf("f191-13-pg-old-%s", uuid.New()), Title: "Old"})
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
	newProj, err := store.CreateProject(ctx, gtd.CreateProjectParams{Name: fmt.Sprintf("f191-13-pg-new-%s", uuid.New()), Title: "New"})
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
	if _, err := pool.Exec(
		ctx,
		`UPDATE deletion_tombstones SET deleted_at = $1 WHERE project_id = $2`,
		cutoff.Add(-24*time.Hour), oldProj.ID, // 31 days ago
	); err != nil {
		t.Fatalf("backdate old group: %v", err)
	}

	oldRows := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`, oldProj.ID)
	newRows := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`, newProj.ID)
	if oldRows != 2 || newRows != 2 {
		t.Fatalf("seed mismatch: oldRows=%d newRows=%d, want 2 and 2", oldRows, newRows)
	}

	deleted, err := store.PruneDeletionTombstones(ctx, cutoff)
	if err != nil {
		t.Fatalf("PruneDeletionTombstones: %v", err)
	}
	if deleted < int64(oldRows) {
		t.Errorf("PruneDeletionTombstones returned %d, want at least %d (the old group's full row count — "+
			"global cleanup, other tests' leftover rows may add more)", deleted, oldRows)
	}

	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`, oldProj.ID); got != 0 {
		t.Errorf("old group still has %d row(s) after prune, want 0 (entire group must be dropped)", got)
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`, newProj.ID); got != 2 {
		t.Errorf("new group has %d row(s) after prune, want 2 (untouched)", got)
	}
}

// TestPGSoftDelete_RestoreWritesAuditRow is F191-12's Postgres half —
// mirrors the SQLite twin (TestRestoreProject_WritesAuditRow,
// internal/storage/sqlite/gtd_softdelete_test.go): exactly one new
// activity_log row, action='project_restored', actor = the actor passed
// to RestoreProject, project_id populated with the restored project's own
// id, notes never carrying the project's stored title.
func TestPGSoftDelete_RestoreWritesAuditRow(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	countActivityLog := func() int {
		return countPGRows(t, pool, `SELECT count(*) FROM activity_log WHERE workspace_id = $1`, wsID)
	}

	secretTitle := fmt.Sprintf("Secret PG Restore Project %s", uuid.New().String()[:8])
	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("f191-12-pg-proj-%s", uuid.New()), Title: secretTitle,
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	before := countActivityLog()

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("RestoreProject: %v", err)
	}

	if got := countActivityLog(); got != before+1 {
		t.Fatalf("activity_log grew by %d, want exactly 1", got-before)
	}

	var action, actor, notes string
	var projectIDCol uuid.UUID
	if err := pool.QueryRow(
		ctx,
		`SELECT action, actor, notes, project_id FROM activity_log
		  WHERE workspace_id = $1 AND action = 'project_restored' ORDER BY created_at DESC LIMIT 1`,
		wsID,
	).Scan(&action, &actor, &notes, &projectIDCol); err != nil {
		t.Fatalf("read audit row: %v", err)
	}
	if action != "project_restored" {
		t.Errorf("action = %q, want %q", action, "project_restored")
	}
	if actor != testerActor {
		t.Errorf("actor = %q, want %q", actor, testerActor)
	}
	if projectIDCol != proj.ID {
		t.Errorf("activity_log.project_id = %s, want %s", projectIDCol, proj.ID)
	}
	if strings.Contains(notes, secretTitle) {
		t.Errorf("audit notes leaked the project's stored title: %q", notes)
	}
}

// ----- SEC-PR191-01: 30-day retention bound on restore lookup + write-path prune, Postgres half -----

// TestPGSoftDelete_RestoreRefusesOutsideRetention is SEC-PR191-01's
// Postgres half — mirrors the SQLite twin
// (TestRestoreProject_RefusesOutsideRetention): a tombstone group whose
// deleted_at is older than gtd.DeletionTombstoneRetention (30 days) must
// not be found by the restore lookup — same ErrNotFound response as "never
// deleted" — and nothing gets written or consumed.
func TestPGSoftDelete_RestoreRefusesOutsideRetention(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("sec01-pg-outside-%s", uuid.New()), Title: "P",
	})
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

	if _, err := pool.Exec(
		ctx,
		`UPDATE deletion_tombstones SET deleted_at = $1 WHERE project_id = $2`,
		time.Now().UTC().Add(-31*24*time.Hour), proj.ID,
	); err != nil {
		t.Fatalf("backdate group: %v", err)
	}

	beforeTombstones := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`, proj.ID)

	if _, _, err := store.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrNotFound) {
		t.Fatalf("RestoreProject error = %v, want gtd.ErrNotFound (group is 31 days old, outside the 30-day retention window)", err)
	}

	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`, proj.ID); got != beforeTombstones {
		t.Errorf("deletion_tombstones row count changed from %d to %d — "+
			"a rejected-as-expired restore must not consume the group", beforeTombstones, got)
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM projects WHERE id = $1`, proj.ID); got != 0 {
		t.Errorf("project row was written even though the group is outside the retention window (found %d)", got)
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM tasks WHERE id = $1`, tk.ID); got != 0 {
		t.Errorf("task row was written even though the group is outside the retention window (found %d)", got)
	}
}

// TestPGSoftDelete_RestoreAllowsInsideRetention is SEC-PR191-01's positive
// control, Postgres half: a group 29 days old (inside the 30-day window)
// restores normally.
func TestPGSoftDelete_RestoreAllowsInsideRetention(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("sec01-pg-inside-%s", uuid.New()), Title: "P",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := store.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	if _, err := pool.Exec(
		ctx,
		`UPDATE deletion_tombstones SET deleted_at = $1 WHERE project_id = $2`,
		time.Now().UTC().Add(-29*24*time.Hour), proj.ID,
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

// TestPGSoftDelete_DeletePrunesExpiredTombstones is SEC-PR191-01's
// write-path prune, Postgres half: a delete's snapshot method removes
// tombstone rows that already fell out of the retention window before
// writing its own snapshot.
func TestPGSoftDelete_DeletePrunesExpiredTombstones(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	oldDeletionID := uuid.New()
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO deletion_tombstones
		   (id, workspace_id, deletion_id, entity_kind, entity_id, project_id, payload, deleted_by, deleted_at)
		 VALUES ($1, $2, $3, 'task', $4, NULL, '{}'::jsonb, 'tester', $5)`,
		uuid.New(), wsID, oldDeletionID, uuid.New(), time.Now().UTC().Add(-31*24*time.Hour),
	); err != nil {
		t.Fatalf("seed 31-day-old tombstone: %v", err)
	}

	proj, err := store.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("sec01-pg-prune-%s", uuid.New()), Title: "P",
	})
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

	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE deletion_id = $1`, oldDeletionID); got != 0 {
		t.Errorf("31-day-old tombstone group still has %d row(s) after a later delete, want 0 (write-path prune should have removed it)", got)
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE entity_kind = 'task' AND entity_id = $1`, tk.ID); got != 1 {
		t.Errorf("the just-written tombstone for the deleted task is missing, want 1 row")
	}
}

// ----- SEC-PR191-03: cross-workspace restore isolation, Postgres half -----

// TestPGSoftDelete_RestoreOtherWorkspaceIsInvisible: a store scoped to
// workspace B must not be able to find (let alone restore) a deletion made
// under workspace A. Positive control at the end proves the tombstone
// really exists and is restorable by its owning workspace, so the
// ErrNotFound above is workspace-scoping and not some other bug hiding the
// tombstone from everyone.
func TestPGSoftDelete_RestoreOtherWorkspaceIsInvisible(t *testing.T) {
	pool := openTestPgPool(t)
	wsA := uuid.New()
	wsB := uuid.New()
	storeA := newPgGTDStore(pool, &wsA)
	storeB := newPgGTDStore(pool, &wsB)
	ctx := context.Background()

	proj, err := storeA.CreateProject(ctx, gtd.CreateProjectParams{
		Name: fmt.Sprintf("sec03-pg-wsA-%s", uuid.New()), Title: "A",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := storeA.DeleteProject(ctx, proj.ID, testerActor); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	beforeTombstones := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`, proj.ID)

	if _, _, err := storeB.RestoreProject(ctx, proj.ID, testerActor); !errors.Is(err, gtd.ErrNotFound) {
		t.Fatalf("RestoreProject from another workspace error = %v, want gtd.ErrNotFound", err)
	}
	if got := countPGRows(t, pool, `SELECT count(*) FROM deletion_tombstones WHERE project_id = $1`, proj.ID); got != beforeTombstones {
		t.Errorf("deletion_tombstones row count changed from %d to %d — "+
			"a cross-workspace restore must not consume the group", beforeTombstones, got)
	}

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
