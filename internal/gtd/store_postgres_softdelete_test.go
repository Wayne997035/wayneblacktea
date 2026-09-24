package gtd_test

import (
	"context"
	"encoding/json"
	"fmt"
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
	if err := pool.QueryRow(ctx,
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
	if err := pool.QueryRow(ctx,
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
		if err := pool.QueryRow(ctx,
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
		if err := pool.QueryRow(ctx,
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
