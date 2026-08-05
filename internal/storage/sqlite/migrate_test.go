package sqlite

import (
	"context"
	"database/sql"
	"errors"
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
// frozenSnapshotVersion, data is preserved, no error is raised — even though
// a full replay from scratch against this DB's already-evolved schema would
// fail on non-idempotent ALTER statements — AND, critically, every migration
// numbered above frozenSnapshotVersion (currently just 000074) actually
// replays afterward: this is the regression test for GTD a4fa8f58 / PR #152
// Critical, where the adoption branch used to Force-stamp straight to
// latestSQLiteSchemaVersion and `return` before m.Up() ever ran, silently
// skipping every migration added after the legacy schema was frozen.
//
// buildPreExistingDB's fixture is built from the real, unmodified schemaSQL
// (schema.sql), which was last hand-updated through migration 000073 (see
// frozenSnapshotVersion's doc comment) and genuinely lacks 000074's
// outcomes.supersedes_id column and both of its new indexes — so this test
// exercises the exact real-world gap, not a synthetic one.
func TestRunMigrations_AdoptsPreExistingDB(t *testing.T) {
	ctx := context.Background()
	conn := buildPreExistingDB(t)

	if err := runMigrations(ctx, conn); err != nil {
		t.Fatalf("runMigrations on pre-existing db: %v", err)
	}

	// Force-stamped at frozenSnapshotVersion, then replayed forward by
	// m.Up() to the true latest version — NOT left stranded at
	// frozenSnapshotVersion, and NOT dirty.
	var version int
	var dirty bool
	if err := conn.QueryRowContext(
		ctx,
		`SELECT version, dirty FROM schema_migrations`,
	).Scan(&version, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != latestSQLiteSchemaVersion {
		t.Errorf("version = %d, want %d (adoption branch must fall through to m.Up() "+
			"and replay every migration above frozenSnapshotVersion, not stop short)",
			version, latestSQLiteSchemaVersion)
	}
	if dirty {
		t.Error("dirty = true, want false (Force+replay must not leave the DB marked dirty)")
	}

	// Core acceptance assertion: migration 000074_outcomes_supersession
	// (numbered above frozenSnapshotVersion) actually executed against this
	// legacy DB. Querying supersedes_id directly (rather than only checking
	// schema_migrations.version) proves the column really exists — a DB
	// left at the pre-fix behavior (Force to head + return) would report
	// version=74 as a bare stamp while `no such column: supersedes_id`
	// on any real read/write, exactly the failure mode from the dispatch.
	var supersedesID sql.NullString
	if err := conn.QueryRowContext(
		ctx,
		`SELECT supersedes_id FROM outcomes WHERE 1 = 0`,
	).Scan(&supersedesID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("outcomes.supersedes_id column missing after adoption + replay: %v", err)
	}

	// Both new 000074 indexes must exist too (Force alone would never have
	// created them; only a real m.Up() replay of 000074's CREATE INDEX
	// statements does).
	for _, idx := range []string{"idx_outcomes_supersedes_id", "idx_outcomes_one_open_draft"} {
		var name string
		if err := conn.QueryRowContext(
			ctx,
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx,
		).Scan(&name); err != nil {
			t.Errorf("index %s missing after adoption + replay: %v", idx, err)
		}
	}

	// Pre-existing data survived (Force does not replay/rebuild tables, and
	// 000074's ALTER TABLE ADD COLUMN / CREATE INDEX are additive-only).
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
