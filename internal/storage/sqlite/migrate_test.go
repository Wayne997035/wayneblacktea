package sqlite

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// buildPreExistingDB simulates a DB created by the retired schema.sql-applied-
// idempotently mechanism: no schema_migrations table, full accumulated
// schema (including the tasks table), and one seeded row — so the adoption
// test can assert data survives Force-stamping.
func buildPreExistingDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if _, err := conn.ExecContext(ctx, schemaSQL); err != nil {
		t.Fatalf("apply legacy schema.sql: %v", err)
	}
	if _, err := conn.ExecContext(
		ctx,
		`INSERT INTO tasks (id, title, status, priority) VALUES ('t-1', 'pre-existing task', 'pending', 3)`,
	); err != nil {
		t.Fatalf("seed fixture row: %v", err)
	}
	return conn
}

// TestRunMigrations_AdoptsPreExistingDB verifies the conservative adoption
// path: a DB with no schema_migrations table but WITH a tasks table (the
// hallmark of a pre-existing full-snapshot install) gets Force-stamped at
// latestSQLiteSchemaVersion instead of replayed, data is preserved, and no
// error is raised — even though a full replay against this DB's
// already-evolved schema would fail on non-idempotent ALTER statements.
func TestRunMigrations_AdoptsPreExistingDB(t *testing.T) {
	ctx := context.Background()
	conn := buildPreExistingDB(t)

	if err := runMigrations(ctx, conn); err != nil {
		t.Fatalf("runMigrations on pre-existing db: %v", err)
	}

	// Force-stamped at the latest version, not dirty.
	var version int
	var dirty bool
	if err := conn.QueryRowContext(
		ctx,
		`SELECT version, dirty FROM schema_migrations`,
	).Scan(&version, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != latestSQLiteSchemaVersion {
		t.Errorf("version = %d, want %d", version, latestSQLiteSchemaVersion)
	}
	if dirty {
		t.Error("dirty = true, want false (Force must not leave the DB marked dirty)")
	}

	// Pre-existing data survived (Force does not replay/rebuild tables).
	var title string
	if err := conn.QueryRowContext(
		ctx,
		`SELECT title FROM tasks WHERE id = 't-1'`,
	).Scan(&title); err != nil {
		t.Fatalf("read seeded row: %v", err)
	}
	if title != "pre-existing task" {
		t.Errorf("title = %q, want %q", title, "pre-existing task")
	}
}

// TestRunMigrations_FreshDBHasNoAdoptionSignal verifies the negative case:
// a genuinely fresh :memory: DB (no schema_migrations, no tasks table) never
// takes the Force-stamp shortcut — sqliteTableExists must report both
// conditions independently so a fresh DB always goes through full replay.
func TestRunMigrations_FreshDBHasNoAdoptionSignal(t *testing.T) {
	ctx := context.Background()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer func() { _ = conn.Close() }()

	hasVersion, err := sqliteTableExists(ctx, conn, "schema_migrations")
	if err != nil {
		t.Fatalf("sqliteTableExists(schema_migrations): %v", err)
	}
	if hasVersion {
		t.Fatal("fresh :memory: DB unexpectedly already has schema_migrations")
	}
	hasTasks, err := sqliteTableExists(ctx, conn, "tasks")
	if err != nil {
		t.Fatalf("sqliteTableExists(tasks): %v", err)
	}
	if hasTasks {
		t.Fatal("fresh :memory: DB unexpectedly already has tasks")
	}
}
