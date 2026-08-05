package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
)

// latestSQLiteSchemaVersion MUST match the highest numbered migration file
// embedded in migrations/sqlite/. Used by readonly.go to assert a DB is
// fully up to date, and as the terminal version m.Up() must reach (via
// frozenSnapshotVersion + replay, see runMigrations) on the adoption path.
// Update this constant whenever a new migrations/sqlite/NNNNNN_*.up.sql
// file is added.
const latestSQLiteSchemaVersion = 74

// frozenSnapshotVersion is the highest migration number whose schema changes
// are ALREADY reflected in internal/storage/sqlite/schema.sql (the retired
// idempotent-apply mechanism's last hand-maintained state, frozen at E1
// 2026-07-18 per schema.sql's own header comment) and therefore already
// present in any real pre-existing DB that only ever went through that
// mechanism. Empirically derived, not guessed: golden_schema_test.go's
// TestGoldenSchemaEquivalence replays every migrations/sqlite/*.sql file
// against a fresh DB and diffs the result against schema.sql's frozen
// snapshot (testdata/schema_golden.sql); its expectedNewEntries map lists
// every schema object present in the replay-built schema but absent from
// the frozen snapshot. As of migration 000074, that map contains exactly
// idx_outcomes_supersedes_id and idx_outcomes_one_open_draft (both added by
// 000074_outcomes_supersession) plus the unrelated historical idx_tasks_due_date
// gap (000041, a documented numbering artifact, not an E1-boundary case) —
// i.e. every migration up to and including 000073 is already accounted for
// in the frozen snapshot, and 000074 is the first one that is not.
//
// On the adoption path (see runMigrations), Force-stamping a pre-existing DB
// at this version — rather than at latestSQLiteSchemaVersion — lets m.Up()
// correctly replay every migration numbered above it (currently just 000074)
// against that DB, instead of skipping them.
//
// MUST be re-verified (not just bumped) whenever a new migration is added:
// re-run TestGoldenSchemaEquivalence and confirm expectedNewEntries only
// contains objects from migrations strictly above the new frozenSnapshotVersion
// candidate. If schema.sql itself is hand-updated to absorb a new migration's
// columns (as happened for 000073, see migrations/HISTORICAL_EXCEPTIONS.md
// E1/E2), bump frozenSnapshotVersion to match; otherwise leave it and let
// the new migration replay normally on the adoption path.
const frozenSnapshotVersion = 73

// runMigrations applies migrations/sqlite/*.sql to conn via golang-migrate,
// reusing the SAME *sql.DB connection Open() already established — never a
// second DSN connection, since SQLite's single-writer model means a second
// connection would compete for the write lock (see db.go SetMaxOpenConns(1)).
//
// Adoption path: a pre-existing DB created by the retired schema.sql-applied-
// idempotently mechanism has no schema_migrations table. Naively calling
// m.Up() on such a DB would replay the entire migration history from
// 000012 onward, which fails: SQLite's ALTER TABLE ADD COLUMN is not
// idempotent (unlike the CREATE TABLE IF NOT EXISTS statements), so ALTER
// statements against columns the DB already has error "duplicate column
// name". We detect this (no schema_migrations + tasks table already present
// = pre-existing full-snapshot DB, since `tasks` is a baseline table) and
// Force-stamp the DB at frozenSnapshotVersion — the version whose changes
// are already baked into that legacy schema — instead of replaying from
// scratch. Force does NOT return early: any migration numbered above
// frozenSnapshotVersion was added after the legacy schema was frozen and is
// genuinely absent from the DB, so control falls through to the m.Up() call
// below, which replays exactly those migrations forward from the
// Force-stamped version. This is deliberately conservative: adoption
// requires BOTH conditions, so a truly fresh DB (no schema_migrations, no
// tasks) always takes the normal replay path untouched.
func runMigrations(ctx context.Context, conn *sql.DB) error {
	hasVersionTable, err := sqliteTableExists(ctx, conn, "schema_migrations")
	if err != nil {
		return fmt.Errorf("check schema_migrations: %w", err)
	}

	src, err := iofs.New(sqlitemigrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load embedded sqlite migrations: %w", err)
	}

	// NoTxWrap: migration 000026_drop_fk_constraints.up.sql wraps its own
	// BEGIN TRANSACTION/COMMIT (a self-contained table-rebuild dance);
	// golang-migrate's default implicit per-file transaction wrap would
	// nest a second BEGIN inside it and fail ("cannot start a transaction
	// within a transaction"). NoTxWrap disables the implicit wrap for every
	// file; 000026 keeps its own atomicity via its explicit BEGIN/COMMIT,
	// and every other file is idempotent (CREATE TABLE/INDEX IF NOT EXISTS)
	// so it doesn't need one.
	driver, err := migratesqlite.WithInstance(conn, &migratesqlite.Config{NoTxWrap: true})
	if err != nil {
		return fmt.Errorf("init sqlite migrate driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return fmt.Errorf("init migrate instance: %w", err)
	}
	// Deliberately no m.Close() here: the sqlite database.Driver's Close()
	// closes the underlying *sql.DB (see golang-migrate database/sqlite
	// driver), which is the connection Open()'s caller keeps using for the
	// rest of the process lifetime — closing it here would break every
	// subsequent query. The iofs source has nothing to release.

	if !hasVersionTable {
		hasTasksTable, err := sqliteTableExists(ctx, conn, "tasks")
		if err != nil {
			return fmt.Errorf("check tasks table (adoption probe): %w", err)
		}
		if hasTasksTable {
			if err := m.Force(frozenSnapshotVersion); err != nil {
				return fmt.Errorf("adopt pre-existing db: force version %d: %w", frozenSnapshotVersion, err)
			}
			// Deliberately no return here: fall through to m.Up() below so
			// any migration numbered above frozenSnapshotVersion (added
			// after the legacy schema was frozen) replays against this
			// now-Force-stamped DB instead of being silently skipped.
		}
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply sqlite migrations: %w", err)
	}
	return nil
}

// sqliteTableExists reports whether a table with the given name exists in
// sqlite_master.
func sqliteTableExists(ctx context.Context, conn *sql.DB, name string) (bool, error) {
	var found string
	err := conn.QueryRowContext(
		ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, name,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query sqlite_master for %q: %w", name, err)
	}
	return true, nil
}
