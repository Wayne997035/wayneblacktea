package sqlite

import (
	"context"
	"database/sql"
	"testing"

	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

// [F0925-11] Migration 000081 adds 8 of the 10 Postgres existing-debt
// indexes (PR #193 full-repo scan, decision e0bfb453) to the SQLite twin —
// see migrations/sqlite/000081_query_indexes.up.sql's header comment for
// which 2 of the 10 are skipped and why.

// openMigratorAt80 mirrors openMigratorAt79 (deletion_tombstones_migration_test.go):
// a raw *sql.DB stepped to the version immediately before the migration
// under test, so DB.Open's full-chain apply (which would run 000081 too)
// never gets a chance to run before the test asserts the pre-migration
// state.
func openMigratorAt80(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(80); err != nil {
		t.Fatalf("migrate to version 80: %v", err)
	}
	return conn, m
}

// queryIndexesExpectedSQL maps each of the 8 SQLite indexes migration 000081
// creates to its expected full CREATE INDEX text (table, column order, DESC,
// and partial WHERE clause), canonicalized the same way
// TestGoldenSchemaEquivalence compares INDEX statements (see canonicalizeSQL).
// This is an exact-text comparison, not just an existence check: it catches
// a wrong column order or a dropped WHERE clause that a bare
// sqlite_master-name lookup would miss.
var queryIndexesExpectedSQL = map[string]string{
	"idx_session_handoffs_project_id": "CREATE INDEX idx_session_handoffs_project_id " +
		"ON session_handoffs(project_id) WHERE project_id IS NOT NULL",
	"idx_work_sessions_project_id": "CREATE INDEX idx_work_sessions_project_id " +
		"ON work_sessions(project_id) WHERE project_id IS NOT NULL",
	"idx_work_sessions_current_task_id": "CREATE INDEX idx_work_sessions_current_task_id " +
		"ON work_sessions(current_task_id) WHERE current_task_id IS NOT NULL",
	"idx_vision_items_workspace_created_at": "CREATE INDEX idx_vision_items_workspace_created_at " +
		"ON vision_items(workspace_id, created_at DESC)",
	"idx_vision_items_project_id": "CREATE INDEX idx_vision_items_project_id " +
		"ON vision_items(project_id) WHERE project_id IS NOT NULL",
	"idx_vision_items_promoted_task_id": "CREATE INDEX idx_vision_items_promoted_task_id " +
		"ON vision_items(promoted_task_id) WHERE promoted_task_id IS NOT NULL",
	"idx_procedural_memories_project_id": "CREATE INDEX idx_procedural_memories_project_id " +
		"ON procedural_memories(project_id) WHERE project_id IS NOT NULL",
	"idx_memory_atoms_created_at": "CREATE INDEX idx_memory_atoms_created_at " +
		"ON memory_atoms(created_at DESC)",
}

// sqliteIndexSQL returns the normalized sqlite_master.sql text for the named
// index, or "" if it doesn't exist.
func sqliteIndexSQL(t *testing.T, conn *sql.DB, name string) string {
	t.Helper()
	var raw string
	err := conn.QueryRowContext(
		context.Background(),
		`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, name,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return ""
	}
	if err != nil {
		t.Fatalf("query sqlite_master for index %q: %v", name, err)
	}
	return canonicalizeSQL(raw)
}

// TestMigration000081_QueryIndexesExist proves migration 000081 creates
// exactly the 8 expected SQLite indexes with the exact expected shape
// (table, column order, DESC, partial WHERE), skips the 2 PG-only indexes
// (project_status_snapshots, guard_bypasses), and that the migration is
// reversible: up to 81, down to 80, and back up to 81 again without error.
func TestMigration000081_QueryIndexesExist(t *testing.T) {
	t.Parallel() // [F0925-11]
	conn, m := openMigratorAt80(t)

	for name := range queryIndexesExpectedSQL {
		if got := sqliteIndexSQL(t, conn, name); got != "" {
			t.Fatalf("index %s exists before migration 000081 has run (got %q)", name, got)
		}
	}

	if err := m.Migrate(81); err != nil {
		t.Fatalf("migrate to 81: %v", err)
	}

	for name, want := range queryIndexesExpectedSQL {
		got := sqliteIndexSQL(t, conn, name)
		if got == "" {
			t.Errorf("index %s does not exist after migration 000081", name)
			continue
		}
		if got != canonicalizeSQL(want) {
			t.Errorf("index %s shape mismatch:\n got:  %s\n want: %s", name, got, want)
		}
	}

	// The 2 PG-only indexes MUST NOT appear on the SQLite side: no SQLite
	// store queries project_status_snapshots, and guard_bypasses has no
	// SQLite twin table at all.
	for _, name := range []string{"idx_project_status_snapshots_workspace_slug", "idx_guard_bypasses_created_at"} {
		if got := sqliteIndexSQL(t, conn, name); got != "" {
			t.Errorf("PG-only index %s unexpectedly exists on SQLite (got %q)", name, got)
		}
	}

	// Reversibility: down to 80 removes all 8, then back up to 81 recreates
	// them without error.
	if err := m.Migrate(80); err != nil {
		t.Fatalf("migrate down to 80: %v", err)
	}
	for name := range queryIndexesExpectedSQL {
		if got := sqliteIndexSQL(t, conn, name); got != "" {
			t.Errorf("index %s still exists after the down migration (got %q)", name, got)
		}
	}

	if err := m.Migrate(81); err != nil {
		t.Fatalf("re-migrate to 81 after down: %v", err)
	}
	for name, want := range queryIndexesExpectedSQL {
		got := sqliteIndexSQL(t, conn, name)
		if got != canonicalizeSQL(want) {
			t.Errorf("index %s missing or wrong shape after re-up:\n got:  %s\n want: %s", name, got, want)
		}
	}
}
