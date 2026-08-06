package sqlite_test

import (
	"context"
	"database/sql"
	"testing"

	sqlitemigrations "github.com/Wayne997035/wayneblacktea/migrations/sqlite"
	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/google/uuid"

	_ "modernc.org/sqlite"
)

// openOutcomeMigratorAt73 returns a raw *sql.DB migrated up to schema
// version 73 (immediately before migration 000074 adds supersedes_id +
// idx_outcomes_one_open_draft) plus the *migrate.Migrate handle, so the test
// can seed pre-existing duplicate draft rows before stepping to 74.
//
// Mirrors internal/storage/sqlite's openMigratorAt72
// (decision_source_backfill_test.go) — duplicated locally rather than
// shared, because that helper lives in the internal `package sqlite` test
// file and is unexported: an external `_test` package (this file is
// `package sqlite_test`, matching outcome_test.go) cannot import unexported
// identifiers from its sibling internal test package.
func openOutcomeMigratorAt73(t *testing.T) (*sql.DB, *migrate.Migrate) {
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
	if err := m.Migrate(73); err != nil {
		t.Fatalf("migrate to version 73: %v", err)
	}
	return conn, m
}

// insertLegacyUnknownOutcome inserts a pre-000074-shaped outcomes row (no
// supersedes_id column yet at schema version 73) directly, with an explicit
// created_at so duplicate-group ordering is deterministic regardless of
// wall-clock test time. Returns the generated row id.
func insertLegacyUnknownOutcome(t *testing.T, conn *sql.DB, wsID, entityType, entityID, createdAt, notes string) string {
	t.Helper()
	id := uuid.New().String()
	_, err := conn.ExecContext(
		context.Background(),
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, notes, created_at)
			VALUES (?, ?, ?, ?, 'unknown', ?, ?)`,
		id, wsID, entityType, entityID, notes, createdAt,
	)
	if err != nil {
		t.Fatalf("insert legacy outcome: %v", err)
	}
	return id
}

// TestMigration000074_Dedup_SQLite reproduces PR #152 second army finding
// M-2 on the real SQLite twin and proves the fix: an install with
// pre-existing duplicate result='unknown' rows for the same (workspace,
// entity_type, entity_id) — reachable before this migration, since the old
// code path (pre-decision-80c1e8ae) unconditionally INSERTed with no
// uniqueness rule at all — must not abort CREATE UNIQUE INDEX. Mirrors the
// Postgres coverage (internal/outcome's testcontainers test) —
// backend-security-design.md §6.5 dual-backend parity requires the same
// dedup SQL to be independently verified on both engines, since the two
// migration files use different window-function/COALESCE dialect syntax
// even though the semantics are meant to be identical.
func TestMigration000074_Dedup_SQLite(t *testing.T) {
	ctx := context.Background()
	conn, m := openOutcomeMigratorAt73(t)

	wsID := uuid.New().String()
	entityID := uuid.New().String()
	otherEntityID := uuid.New().String()

	older := insertLegacyUnknownOutcome(t, conn, wsID, "task", entityID, "2026-01-01T00:00:00.000Z", "older empty draft")
	newer := insertLegacyUnknownOutcome(t, conn, wsID, "task", entityID, "2026-01-02T00:00:00.000Z", "newer draft with content")
	single := insertLegacyUnknownOutcome(t, conn, wsID, "task", otherEntityID, "2026-01-01T00:00:00.000Z", "no dup")

	evalID := uuid.New().String()
	if _, err := conn.ExecContext(ctx,
		`INSERT INTO evaluations (id, workspace_id, outcome_id, analysis) VALUES (?, ?, ?, ?)`,
		evalID, wsID, older, "stale eval on the older dup",
	); err != nil {
		t.Fatalf("insert legacy evaluation: %v", err)
	}

	// Applying 000074 without the dedup step would fail here with a UNIQUE
	// constraint violation — the exact M-2 production symptom (migration
	// aborts mid-way, version 74 marked dirty, server can't start). This
	// step exercises the FIXED migration body, which dedups first.
	if err := m.Steps(1); err != nil {
		t.Fatalf("apply migration 000074 (expected to succeed after dedup): %v", err)
	}

	var remaining string
	err := conn.QueryRowContext(ctx,
		`SELECT id FROM outcomes WHERE entity_id = ? AND result = 'unknown'`, entityID,
	).Scan(&remaining)
	if err != nil {
		t.Fatalf("expected exactly one surviving 'unknown' row for the duplicate entity: %v", err)
	}
	if remaining != newer {
		t.Errorf("expected the newer duplicate %s to survive, got %s (older %s should have been deleted)", newer, remaining, older)
	}

	var count int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM outcomes WHERE id = ?`, older).Scan(&count); err != nil {
		t.Fatalf("count older duplicate: %v", err)
	}
	if count != 0 {
		t.Error("expected the older duplicate draft to be deleted by the dedup step")
	}

	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM evaluations WHERE outcome_id = ?`, older).Scan(&count); err != nil {
		t.Fatalf("count stale evaluation: %v", err)
	}
	if count != 0 {
		t.Error("expected the stale evaluation on the deleted duplicate to be cleaned up too")
	}

	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM outcomes WHERE id = ?`, single).Scan(&count); err != nil {
		t.Fatalf("count non-duplicate control row: %v", err)
	}
	if count != 1 {
		t.Error("expected the non-duplicate control row to survive untouched")
	}

	// The unique index now actually enforces uniqueness going forward.
	_, err = conn.ExecContext(ctx,
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result) VALUES (?, ?, 'task', ?, 'unknown')`,
		uuid.New().String(), wsID, entityID,
	)
	if err == nil {
		t.Error("expected the unique index to reject a second 'unknown' row post-migration")
	}
}

// insertLegacyUnknownOutcomeWithID is insertLegacyUnknownOutcome's twin for
// tests that need to control the row id explicitly instead of accepting
// uuid.New()'s random id — required for the created_at tie-break test below,
// where the expected survivor must be knowable ahead of time from the
// migration's ORDER BY created_at DESC, id DESC contract itself, not from
// observing which row a run happens to keep.
func insertLegacyUnknownOutcomeWithID(t *testing.T, conn *sql.DB, id, wsID, entityType, entityID, createdAt, notes string) {
	t.Helper()
	_, err := conn.ExecContext(
		context.Background(),
		`INSERT INTO outcomes (id, workspace_id, entity_type, entity_id, result, notes, created_at)
			VALUES (?, ?, ?, ?, 'unknown', ?, ?)`,
		id, wsID, entityType, entityID, notes, createdAt,
	)
	if err != nil {
		t.Fatalf("insert legacy outcome with explicit id: %v", err)
	}
}

// TestMigration000074_Dedup_SQLite_CreatedAtTieBreak covers the case
// TestMigration000074_Dedup_SQLite above does not: two duplicate 'unknown'
// drafts for the same (workspace, entity_type, entity_id) whose created_at
// values are bit-for-bit identical, rather than merely close. When
// created_at ties, ORDER BY created_at DESC alone cannot pick a winner —
// migrations/sqlite/000074_outcomes_supersession.up.sql's dedup step
// resolves this with a second ORDER BY key, id DESC, which this test pins
// down directly. The expected survivor (the row with the lexicographically
// GREATER id) is derived from that ORDER BY clause itself, not from
// observing what one run happens to produce.
func TestMigration000074_Dedup_SQLite_CreatedAtTieBreak(t *testing.T) {
	ctx := context.Background()
	conn, m := openOutcomeMigratorAt73(t)

	wsID := uuid.New().String()
	entityID := uuid.New().String()
	const sameCreatedAt = "2026-01-01T00:00:00.000Z"

	// Label the two ids by lexicographic order so the expected survivor
	// (idGreater) is known before the migration runs, per the id DESC
	// tie-break contract.
	idLesser, idGreater := uuid.New().String(), uuid.New().String()
	if idLesser > idGreater {
		idLesser, idGreater = idGreater, idLesser
	}

	insertLegacyUnknownOutcomeWithID(t, conn, idLesser, wsID, "task", entityID, sameCreatedAt, "tie-break loser")
	insertLegacyUnknownOutcomeWithID(t, conn, idGreater, wsID, "task", entityID, sameCreatedAt, "tie-break winner")

	if err := m.Steps(1); err != nil {
		t.Fatalf("apply migration 000074 (expected to succeed after dedup): %v", err)
	}

	var remaining string
	err := conn.QueryRowContext(ctx,
		`SELECT id FROM outcomes WHERE entity_id = ? AND result = 'unknown'`, entityID,
	).Scan(&remaining)
	if err != nil {
		t.Fatalf("expected exactly one surviving 'unknown' row for the tied-created_at duplicate: %v", err)
	}
	if remaining != idGreater {
		t.Errorf("expected the row with the greater id (%s) to survive the created_at tie via "+
			"ORDER BY created_at DESC, id DESC, got %s", idGreater, remaining)
	}

	var count int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM outcomes WHERE id = ?`, idLesser).Scan(&count); err != nil {
		t.Fatalf("count tie-break loser: %v", err)
	}
	if count != 0 {
		t.Error("expected the tie-break loser (lesser id, identical created_at) to be deleted by the dedup step")
	}
}
