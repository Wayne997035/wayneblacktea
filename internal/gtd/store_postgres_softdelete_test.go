package gtd_test

import (
	"context"
	"fmt"
	"sort"
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

	if _, err := store.DeleteProject(ctx, doomed.ID, "tester"); err != nil {
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
