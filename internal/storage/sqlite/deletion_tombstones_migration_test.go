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

// [F191-03] Migration 000080 creates deletion_tombstones, the snapshot table
// the soft-delete contract (PR #191) writes into. This stage only creates
// the schema — nothing writes to it yet (see the migration file's own header
// comment).

// openMigratorAt79 mirrors openMigratorAt76 (duedate_layout_migration_test.go)
// / openMigratorAt72 (decision_source_backfill_test.go): a raw *sql.DB
// stepped to the version immediately before the migration under test, so
// DB.Open's full-chain apply (which would run 000080 too) never gets a
// chance to run before the test asserts the pre-migration state.
func openMigratorAt79(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(79); err != nil {
		t.Fatalf("migrate to version 79: %v", err)
	}
	return conn, m
}

// sqliteObjectExists reports whether sqlite_master carries an object of the
// given type ("table" / "index") and name.
func sqliteObjectExists(t *testing.T, conn *sql.DB, typ, name string) bool {
	t.Helper()
	var n int
	if err := conn.QueryRowContext(
		context.Background(),
		`SELECT count(*) FROM sqlite_master WHERE type = ?1 AND name = ?2`, typ, name,
	).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master for %s %q: %v", typ, name, err)
	}
	return n != 0
}

// deletionTombstonesIndexes lists the three indexes design 1 requires:
// (project_id, deleted_at DESC) for restore_project's most-recent lookup,
// (deleted_at) for the pruner's range scan, (deletion_id) for
// restore_project's group write-back/consume.
var deletionTombstonesIndexes = []string{
	"idx_deletion_tombstones_project_deleted_at",
	"idx_deletion_tombstones_deleted_at",
	"idx_deletion_tombstones_deletion_id",
}

// TestMigration000080_UpDown proves the table and its three indexes appear
// after up and disappear after down.
func TestMigration000080_UpDown(t *testing.T) {
	t.Parallel() // [F0925-10]
	conn, m := openMigratorAt79(t)

	if sqliteObjectExists(t, conn, "table", "deletion_tombstones") {
		t.Fatal("deletion_tombstones exists before migration 000080 has run")
	}

	if err := m.Migrate(80); err != nil {
		t.Fatalf("migrate to 80: %v", err)
	}

	if !sqliteObjectExists(t, conn, "table", "deletion_tombstones") {
		t.Fatal("deletion_tombstones does not exist after migration 000080")
	}
	for _, idx := range deletionTombstonesIndexes {
		if !sqliteObjectExists(t, conn, "index", idx) {
			t.Errorf("index %s does not exist after migration 000080", idx)
		}
	}

	if err := m.Migrate(79); err != nil {
		t.Fatalf("migrate down to 79: %v", err)
	}

	if sqliteObjectExists(t, conn, "table", "deletion_tombstones") {
		t.Error("deletion_tombstones still exists after the down migration")
	}
	for _, idx := range deletionTombstonesIndexes {
		if sqliteObjectExists(t, conn, "index", idx) {
			t.Errorf("index %s still exists after the down migration", idx)
		}
	}
}
