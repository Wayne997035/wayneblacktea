package gtd_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/gtd"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [F1124-03, Postgres half] mirrors TestDeleteTask_ClearsEveryTaskIDColumnFromMigratedSchema
// (internal/storage/sqlite/gtd_deleteproject_test.go) with
// information_schema.columns as the schema source and $N placeholders. The
// two files read as one design read twice: a future *task_id column that
// delete_task does not clean fails both.

// pgTaskIDCheckQueries is written out literally per column (unqueryvet).
var pgTaskIDCheckQueries = map[string]string{
	"completion_candidates.task_id": `SELECT count(*) FROM completion_candidates WHERE task_id = $1`,
	"decisions.task_id":             `SELECT count(*) FROM decisions WHERE task_id = $1`,
	"knowledge_items.task_id":       `SELECT count(*) FROM knowledge_items WHERE task_id = $1`,
	"vision_items.promoted_task_id": `SELECT count(*) FROM vision_items WHERE promoted_task_id = $1`,
	"work_session_tasks.task_id":    `SELECT count(*) FROM work_session_tasks WHERE task_id = $1`,
	"work_sessions.current_task_id": `SELECT count(*) FROM work_sessions WHERE current_task_id = $1`,
}

// pgTaskIDSeedStatements shares one argument shape: $1 = row id (session id
// for work_session_tasks), $2 = workspace id, $3 = task id, $4 = timestamp.
// Every placeholder is used in every statement and cast so Postgres can
// infer its type. work_sessions is seeded 'planned' (not 'in_progress') so two
// rows in one workspace do not trip idx_work_sessions_one_active.
var pgTaskIDSeedStatements = map[string]string{
	"completion_candidates.task_id": `INSERT INTO completion_candidates (id, workspace_id, task_id, reason, confidence, detected_at)
		VALUES ($1::uuid,$2,$3,'r-'||($1::uuid)::text,'high',$4)`,
	"decisions.task_id": `INSERT INTO decisions (id, workspace_id, title, context, decision, rationale, task_id, created_at)
		VALUES ($1,$2,'d','ctx','chose X','because',$3,$4)`,
	"knowledge_items.task_id": `INSERT INTO knowledge_items (id, workspace_id, type, title, content, task_id, created_at, updated_at)
		VALUES ($1,$2,'til','k','body',$3,$4,$4)`,
	"vision_items.promoted_task_id": `INSERT INTO vision_items (id, workspace_id, title, why_blocked, status, promoted_task_id, created_at)
		VALUES ($1,$2,'v','blocked','promoted',$3,$4)`,
	"work_session_tasks.task_id": `INSERT INTO work_session_tasks (session_id, task_id, role, created_at)
		SELECT $1::uuid, $3::uuid, 'primary', $4::timestamptz WHERE $2::uuid IS NOT NULL`,
	"work_sessions.current_task_id": `INSERT INTO work_sessions
		(id, workspace_id, repo_name, title, goal, status, source, current_task_id, started_at, created_at, updated_at)
		VALUES ($1,$2,'demo-repo','s','g','planned','manual',$3,$4,$4,$4)`,
}

// derivePGTaskIDColumns returns every base-table "table.column" in public
// whose column name ends in task_id, minus gtd.TaskIDCleanupExemptions,
// sorted; plus the raw (pre-exemption) set for the stale-exemption check.
func derivePGTaskIDColumns(t *testing.T, pool *pgxpool.Pool) (derived, raw []string) {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT k.table_name, k.column_name
		   FROM information_schema.columns k
		   JOIN information_schema.tables tb
		     ON tb.table_schema = k.table_schema AND tb.table_name = k.table_name AND tb.table_type = 'BASE TABLE'
		  WHERE k.table_schema = 'public' AND k.column_name LIKE '%task\_id'
		  ORDER BY k.table_name, k.column_name`)
	if err != nil {
		t.Fatalf("query information_schema.columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatalf("scan table_name, column_name: %v", err)
		}
		key := table + "." + column
		raw = append(raw, key)
		if !gtd.TaskIDCleanupExemptions[key] {
			derived = append(derived, key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate information_schema.columns rows: %v", err)
	}
	sort.Strings(derived)
	sort.Strings(raw)
	return derived, raw
}

// TestPGDeleteTask_ClearsEveryTaskIDColumnFromMigratedSchema [F1124-03] is
// the Postgres half of the schema-derived delete_task guard.
func TestPGDeleteTask_ClearsEveryTaskIDColumnFromMigratedSchema(t *testing.T) {
	pool := openTestPgPool(t)
	wsID := uuid.New()
	store := newPgGTDStore(pool, &wsID)
	ctx := context.Background()

	derived, raw := derivePGTaskIDColumns(t, pool)
	t.Logf("derived task_id columns (%d): %v", len(derived), derived)

	rawSet := make(map[string]bool, len(raw))
	for _, k := range raw {
		rawSet[k] = true
	}
	for k := range gtd.TaskIDCleanupExemptions {
		if !rawSet[k] {
			t.Errorf("stale exemption %q: no such column in the migrated PG schema", k)
		}
	}

	if diff := tableListDiff(derived, sortedKeys(pgTaskIDCheckQueries)); diff != "" {
		t.Fatalf("pgTaskIDCheckQueries is out of sync with the migrated PG schema "+
			"(derived, minus gtd.TaskIDCleanupExemptions): %s — register a check+seed pair and make "+
			"delete_task clean it, or add an exemption with a reason", diff)
	}
	if diff := tableListDiff(derived, sortedKeys(pgTaskIDSeedStatements)); diff != "" {
		t.Fatalf("pgTaskIDSeedStatements is out of sync with the migrated PG schema: %s", diff)
	}

	doomed, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "f1124-03-pg-doomed"})
	if err != nil {
		t.Fatalf("CreateTask doomed: %v", err)
	}
	bystander, err := store.CreateTask(ctx, gtd.CreateTaskParams{Title: "f1124-03-pg-bystander"})
	if err != nil {
		t.Fatalf("CreateTask bystander: %v", err)
	}

	seedTS := time.Date(2026, 9, 18, 0, 0, 0, 0, time.UTC)
	count := func(key string, task uuid.UUID) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, pgTaskIDCheckQueries[key], task).Scan(&n); err != nil {
			t.Fatalf("check %s: %v", key, err)
		}
		return n
	}
	for _, key := range derived {
		for _, task := range []uuid.UUID{doomed.ID, bystander.ID} {
			if _, err := pool.Exec(ctx, pgTaskIDSeedStatements[key], uuid.New(), wsID, task, seedTS); err != nil {
				t.Fatalf("seed %s: %v", key, err)
			}
		}
		if n := count(key, doomed.ID); n != 1 {
			t.Fatalf("%s: seeding produced %d rows for the doomed task, want 1", key, n)
		}
	}

	if err := store.DeleteTask(ctx, doomed.ID, testerActor); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	for _, key := range derived {
		if n := count(key, doomed.ID); n != 0 {
			t.Errorf("%s: %d dangling reference(s) to the deleted task", key, n)
		}
		// A different task's row must survive: guards "cleanup deleted everything".
		if n := count(key, bystander.ID); n != 1 {
			t.Errorf("%s: %d rows left for the bystander task, want 1", key, n)
		}
	}

	t.Run("reverse_control_skipped_column_turns_red", func(t *testing.T) {
		if len(derived) == 0 {
			t.Fatal("derived list is empty — nothing to drop, probe is meaningless")
		}
		short := append([]string(nil), derived[1:]...)
		if diff := tableListDiff(derived, short); diff == "" {
			t.Fatal("dropping a column from the registered list produced no diff — " +
				"the completeness check cannot catch a column a real implementation forgot to register")
		}
	})
}
