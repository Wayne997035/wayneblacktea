package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

// [F1003-08] SQLite twin of
// internal/storage/migration_000086_index_integration_test.go — see
// migrations/sqlite/000086_goals_active_due_date_idx.up.sql for the index
// shape and rationale. package sqlite (not sqlite_test): mirrors
// migration_000085_index_test.go's convention for this migration-DDL test
// family.

// openMigratorAt85 mirrors openMigratorAt84 (migration_000085_index_test.go):
// a raw *sql.DB stepped to the version immediately before the migration
// under test, so DB.Open's full-chain apply (which would run 000086 too)
// never gets a chance to run before the test asserts the pre-migration
// state.
func openMigratorAt85(t *testing.T) (*sql.DB, *migrate.Migrate) {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	src, err := iofs.New(sqlitemigrations.FS, ".")
	if err != nil {
		t.Fatalf("load embedded sqlite migrations: %v", err)
	}
	driver, err := migratesqlite.WithInstance(conn, &migratesqlite.Config{NoTxWrap: true})
	if err != nil {
		t.Fatalf("init sqlite migrate driver: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		t.Fatalf("init migrate instance: %v", err)
	}
	if err := m.Migrate(85); err != nil {
		t.Fatalf("migrate to version 85: %v", err)
	}
	return conn, m
}

// wantIdx000086SQLite is the expected full CREATE INDEX text — exact-text
// comparison, not just an existence check, so a wrong column order or a
// dropped WHERE clause would still pass a bare sqlite_master-name lookup.
const wantIdx000086SQLite = "CREATE INDEX idx_goals_active_due_date " +
	"ON goals(workspace_id,due_date,id) WHERE status = 'active'"

// TestMigration000086_GoalsActiveDueDateIndexExists proves migration 000086
// creates idx_goals_active_due_date in SQLite with the exact expected
// shape, and that the migration is reversible: up to 86, down to 85, and
// back up to 86 again without error (mirrors TestMigration000085_ProposalDedupIndexExists).
func TestMigration000086_GoalsActiveDueDateIndexExists(t *testing.T) {
	t.Parallel() // [F1003-08]
	conn, m := openMigratorAt85(t)

	if got := sqliteIndexSQL(t, conn, "idx_goals_active_due_date"); got != "" {
		t.Fatalf("index exists before migration 000086 has run (got %q)", got)
	}

	if err := m.Migrate(86); err != nil {
		t.Fatalf("migrate to 86: %v", err)
	}
	got := sqliteIndexSQL(t, conn, "idx_goals_active_due_date")
	if got == "" {
		t.Fatal("index does not exist after migration 000086")
	}
	if got != canonicalizeSQL(wantIdx000086SQLite) {
		t.Errorf("index shape mismatch:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdx000086SQLite))
	}

	if err := m.Migrate(85); err != nil {
		t.Fatalf("migrate down to 85: %v", err)
	}
	if got := sqliteIndexSQL(t, conn, "idx_goals_active_due_date"); got != "" {
		t.Errorf("index still exists after the down migration (got %q)", got)
	}

	if err := m.Migrate(86); err != nil {
		t.Fatalf("re-migrate to 86 after down: %v", err)
	}
	got = sqliteIndexSQL(t, conn, "idx_goals_active_due_date")
	if got != canonicalizeSQL(wantIdx000086SQLite) {
		t.Errorf("index missing or wrong shape after re-up:\n got:  %s\n want: %s", got, canonicalizeSQL(wantIdx000086SQLite))
	}
}

// seedGoalsForExplain inserts n active goals (half with a non-NULL
// due_date) into the given workspace, mirroring the shape
// ActiveGoalsPage's WHERE/ORDER BY actually filters/sorts on.
func seedGoalsForExplain(t *testing.T, conn *sql.DB, workspaceID string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		var due any
		if i%2 == 0 {
			due = "2026-01-01T00:00:00.000Z"
		}
		id := workspaceID + "-goal-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		if _, err := conn.ExecContext(
			ctx,
			`INSERT INTO goals (id, workspace_id, title, status, due_date) VALUES (?, ?, 'g', 'active', ?)`,
			id, workspaceID, due,
		); err != nil {
			t.Fatalf("seed goal %d: %v", i, err)
		}
	}
}

// explainGoalsQueryPlan runs ActiveGoalsPage's exact query shape (mirrored,
// since the production query is a function-local const — see
// internal/storage/sqlite/gtd.go's ActiveGoalsPage) through EXPLAIN QUERY
// PLAN, binding workspaceArg either to a concrete value or nil.
func explainGoalsQueryPlan(t *testing.T, conn *sql.DB, workspaceArg any) string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `EXPLAIN QUERY PLAN
		SELECT id FROM goals
		WHERE status = 'active'
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY due_date ASC NULLS LAST, id ASC
		LIMIT 20 OFFSET 0`, workspaceArg)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var plan string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan EXPLAIN QUERY PLAN row: %v", err)
		}
		plan += detail + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate EXPLAIN QUERY PLAN rows: %v", err)
	}
	return plan
}

// TestMigration000086_GoalsActiveDueDateIndexUsedByProdPathQuery is the
// EXPLAIN QUERY PLAN proof that idx_goals_active_due_date is actually used
// by the production-path query shape (workspace_id bound to a concrete
// value).
//
// EMPIRICAL CORRECTION to spec 594d8917's acceptance table (verified, not
// guessed — see the literal `go test -v` output cited in the done record):
// the spec predicted this path would show a plain
// "SEARCH ... (workspace_id=? AND status=?)" with no sort. That prediction
// implicitly assumed a query shape with a bare `workspace_id = ?1` equality.
// The REAL production query (ActiveGoalsPage, internal/storage/sqlite/gtd.go)
// always sends `(?1 IS NULL OR workspace_id = ?1)` — the same SQL text for
// both the scoped and legacy-NULL calling conventions. Unlike Postgres
// (whose custom-plan mode re-plans using the actual bound value on every one
// of its first 5 executions, constant-folding away the IS NULL branch —
// see internal/db's EXPLAIN tests), SQLite compiles ONE bytecode program per
// prepared statement text that is value-independent: it cannot drop the
// `?1 IS NULL` branch just because this particular call happens to bind a
// non-NULL value, so it produces the identical
// "SCAN ... USING COVERING INDEX" + "USE TEMP B-TREE FOR ORDER BY" plan
// for both the prod-shaped and legacy-NULL-shaped calls (confirmed by
// comparing this test's plan against
// TestMigration000086_GoalsActiveDueDateIndexUsedByLegacyNullPathQuery's —
// byte-identical). The index still delivers a real, if partial, benefit on
// SQLite: a covering-index scan restricted to status='active' rows instead
// of a full table scan; it just cannot also eliminate the sort step the way
// it does on Postgres. This is a genuine, documented PG/SQLite divergence,
// not a bug in the migration or a reason to change the index shape — the
// index column order was chosen to serve the PG production path (fail-closed
// WORKSPACE_ID, per Target behaviour), where it IS fully effective.
func TestMigration000086_GoalsActiveDueDateIndexUsedByProdPathQuery(t *testing.T) {
	t.Parallel() // [F1003-08]
	conn, m := openMigratorAt85(t)
	if err := m.Migrate(86); err != nil {
		t.Fatalf("migrate to 86: %v", err)
	}
	seedGoalsForExplain(t, conn, "ws-prod", 20)

	plan := explainGoalsQueryPlan(t, conn, "ws-prod")
	t.Logf("prod-path EXPLAIN QUERY PLAN:\n%s", plan)
	if !strings.Contains(plan, "idx_goals_active_due_date") {
		t.Fatalf("expected idx_goals_active_due_date referenced in the plan, got a bare scan with no index reference:\n%s", plan)
	}
	if strings.Contains(plan, "SCAN goals\n") || strings.HasPrefix(plan, "SCAN goals\n") {
		t.Fatalf("plan fell back to a bare table scan (no index at all):\n%s", plan)
	}
	if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
		t.Logf("prod path (SQLite) needs USE TEMP B-TREE FOR ORDER BY — expected: the shared (?1 IS NULL OR workspace_id = ?1) query text compiles one value-independent bytecode plan for both the scoped and legacy-NULL callers, so SQLite cannot specialize away the sort the way Postgres's custom plan does. See this test's doc comment.")
	}
}

// TestMigration000086_GoalsActiveDueDateIndexUsedByLegacyNullPathQuery
// documents (does not gate on, per Lead's supplement 1) the SQLite-only
// legacy/no-filter path: workspace_id bound to NULL. The index must still
// be referenced for the status='active' predicate at minimum; a
// "USE TEMP B-TREE FOR ORDER BY" line here is expected and merely logged.
func TestMigration000086_GoalsActiveDueDateIndexUsedByLegacyNullPathQuery(t *testing.T) {
	t.Parallel() // [F1003-08]
	conn, m := openMigratorAt85(t)
	if err := m.Migrate(86); err != nil {
		t.Fatalf("migrate to 86: %v", err)
	}
	seedGoalsForExplain(t, conn, "ws-legacy", 20)

	plan := explainGoalsQueryPlan(t, conn, nil)
	t.Logf("legacy-NULL-path EXPLAIN QUERY PLAN:\n%s", plan)
	if !strings.Contains(plan, "idx_goals_active_due_date") {
		t.Fatalf("expected idx_goals_active_due_date referenced for the status='active' filter, got a bare scan with no index reference:\n%s", plan)
	}
	if strings.Contains(plan, "USE TEMP B-TREE FOR ORDER BY") {
		t.Logf("legacy-NULL path needed USE TEMP B-TREE FOR ORDER BY — accepted trade-off, this path is SQLite-dev-only (see migration file header)")
	}
}
