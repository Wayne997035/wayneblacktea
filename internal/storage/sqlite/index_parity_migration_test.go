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

// [F0925-15] Migration 000082 is SQLite-only (Postgres is already the target
// shape; see migrations/sqlite/000082_index_parity.up.sql's header comment):
// it realigns 3 SQLite indexes whose shape drifted from their Postgres
// counterpart — idx_decisions_task_id (add the partial WHERE),
// idx_work_sessions_workspace_id (drop the partial WHERE, PG's is not
// partial), idx_work_sessions_repo_name (add DESC on created_at) — to be
// textually identical to the PG definitions in migrations/000048_* and
// migrations/000021_*.

// openMigratorAt81 mirrors openMigratorAt80 (query_indexes_migration_test.go):
// a raw *sql.DB stepped to the version immediately before the migration
// under test, so DB.Open's full-chain apply (which would run 000082 too)
// never gets a chance to run before the test asserts the pre-migration
// state.
func openMigratorAt81(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(81); err != nil {
		t.Fatalf("migrate to version 81: %v", err)
	}
	return conn, m
}

// indexParityOldSQLiteSQL is the SQLite shape BEFORE migration 000082 (the
// pre-existing drift from PG), keyed by index name.
var indexParityOldSQLiteSQL = map[string]string{
	"idx_decisions_task_id": "CREATE INDEX idx_decisions_task_id ON decisions(task_id)",
	"idx_work_sessions_repo_name": "CREATE INDEX idx_work_sessions_repo_name " +
		"ON work_sessions(workspace_id, repo_name, created_at)",
	"idx_work_sessions_workspace_id": "CREATE INDEX idx_work_sessions_workspace_id " +
		"ON work_sessions(workspace_id) WHERE workspace_id IS NOT NULL",
}

// indexParityExpectedSQL is the SQLite shape AFTER migration 000082, textually
// identical to the Postgres definitions in migrations/000048_decisions_task_id.up.sql
// and migrations/000021_work_sessions.up.sql. Canonicalized the same way
// TestGoldenSchemaEquivalence compares INDEX statements (see canonicalizeSQL)
// — this is an exact-text comparison, not just an existence check, so it
// catches a wrong column order or a missing DESC/WHERE that a bare
// sqlite_master-name lookup would miss.
var indexParityExpectedSQL = map[string]string{
	"idx_decisions_task_id": "CREATE INDEX idx_decisions_task_id " +
		"ON decisions(task_id) WHERE task_id IS NOT NULL",
	"idx_work_sessions_repo_name": "CREATE INDEX idx_work_sessions_repo_name " +
		"ON work_sessions(workspace_id, repo_name, created_at DESC)",
	"idx_work_sessions_workspace_id": "CREATE INDEX idx_work_sessions_workspace_id " +
		"ON work_sessions(workspace_id)",
}

// indexParitySQL returns the normalized sqlite_master.sql text for the named
// index, or "" if it doesn't exist.
func indexParitySQL(t *testing.T, conn *sql.DB, name string) string {
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

// TestMigration000082_IndexParityWithPG proves migration 000082 realigns all
// 3 drifted SQLite indexes to be textually identical to their Postgres
// counterpart, and that the migration is reversible: at version 81 the old
// (drifted) shape is present, up to 82 gives the PG-aligned shape, down to
// 81 reverts to the old shape, and back up to 82 gives the PG-aligned shape
// again — all without error.
func TestMigration000082_IndexParityWithPG(t *testing.T) {
	t.Parallel() // [F0925-15]
	conn, m := openMigratorAt81(t)

	for name, want := range indexParityOldSQLiteSQL {
		got := indexParitySQL(t, conn, name)
		if got != canonicalizeSQL(want) {
			t.Fatalf("index %s at v81 (pre-000082) shape mismatch:\n got:  %s\n want: %s", name, got, want)
		}
	}

	if err := m.Migrate(82); err != nil {
		t.Fatalf("migrate to 82: %v", err)
	}
	for name, want := range indexParityExpectedSQL {
		got := indexParitySQL(t, conn, name)
		if got != canonicalizeSQL(want) {
			t.Errorf("index %s shape mismatch after 000082:\n got:  %s\n want: %s", name, got, want)
		}
	}

	// Reversibility: down to 81 reverts to the old (drifted) shape, then back
	// up to 82 gives the PG-aligned shape again, without error.
	if err := m.Migrate(81); err != nil {
		t.Fatalf("migrate down to 81: %v", err)
	}
	for name, want := range indexParityOldSQLiteSQL {
		got := indexParitySQL(t, conn, name)
		if got != canonicalizeSQL(want) {
			t.Errorf("index %s did not revert to old shape after down migration:\n got:  %s\n want: %s", name, got, want)
		}
	}

	if err := m.Migrate(82); err != nil {
		t.Fatalf("re-migrate to 82 after down: %v", err)
	}
	for name, want := range indexParityExpectedSQL {
		got := indexParitySQL(t, conn, name)
		if got != canonicalizeSQL(want) {
			t.Errorf("index %s missing or wrong shape after re-up:\n got:  %s\n want: %s", name, got, want)
		}
	}
}
