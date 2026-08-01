package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seedDirtyMigrationState pre-creates a SQLite file at path whose
// schema_migrations table (golang-migrate's sqlite driver schema — see
// github.com/golang-migrate/migrate/v4/database/sqlite.ensureVersionTable)
// already has a dirty row. This forces Open()'s runMigrations step to fail
// deterministically via golang-migrate's own dirty-database guard
// (Migrate.Up reads the stored dirty flag before attempting any migration
// and immediately returns ErrDirty — see migrate.v4/migrate.go Up()), with
// no test-only injection seam: the same real-error-path technique already
// used by TestOpen_RefusesSymlinks / TestOpen_TightensPreExistingMainAndSidecars
// in db_test.go.
func seedDirtyMigrationState(t *testing.T, path string) {
	t.Helper()
	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("seed sql.Open(%q): %v", path, err)
	}
	defer func() {
		if err := seed.Close(); err != nil {
			t.Fatalf("seed Close: %v", err)
		}
	}()
	_, err = seed.ExecContext(context.Background(), `
		CREATE TABLE schema_migrations (version uint64, dirty bool);
		CREATE UNIQUE INDEX version_unique ON schema_migrations (version);
		INSERT INTO schema_migrations (version, dirty) VALUES (1, 1);
	`)
	if err != nil {
		t.Fatalf("seed dirty schema_migrations: %v", err)
	}
}

// TestOpen_DirtyMigrationStateFailsAtRunMigrations drives Open() through the
// runMigrations error branch — one of the 9 branches in Open() that call
// closeAbandonedOpen (db.go:93-96) — using a real, unmocked database state
// rather than any injected failure. openSQLiteConnection has already fully
// succeeded (secured the file, pinged, resolved the main path) by the time
// runMigrations fails, matching the "fail only after openSQLiteConnection
// succeeds" requirement.
func TestOpen_DirtyMigrationStateFailsAtRunMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dirty.db")
	seedDirtyMigrationState(t, dbPath)

	_, err := Open(context.Background(), "file:"+dbPath, "")
	if err == nil {
		t.Fatal("Open(dirty schema_migrations) succeeded, want a migration failure")
	}
	if !strings.Contains(err.Error(), "sqlite run migrations") {
		t.Fatalf("Open error = %v, want it to originate from runMigrations", err)
	}
}

// TestCloseAbandonedOpen_ReleasesConnAndModeReference is the direct,
// mutation-sensitive proof that closeAbandonedOpen (db.go:127-130) actually
// releases both resources it is documented to own.
//
// This is deliberately decoupled from asserting resource release through a
// full Open() failure end-to-end: Open() never hands the abandoned *sql.DB /
// *os.File back to the caller on an error path (by design — no *DB survives
// to expose them), so there is no way to observe release from outside Open()
// without either (a) OS-level fd counting, which this package's own comments
// (see newOwnerOnlyModeReference) already flag as unreliable on Darwin for
// descriptors of this kind, or (b) SQLite lock-probing, which is not
// deterministic here since NoTxWrap means a failing migration does not
// reliably leave an exclusive lock held. Calling closeAbandonedOpen directly
// with the exact resource types openSQLiteConnection hands to Open() (the
// same call Open() itself makes as its first step) avoids both, and is the
// only technique in this file that reliably goes red when conn.Close() is
// removed from closeAbandonedOpen.
func TestCloseAbandonedOpen_ReleasesConnAndModeReference(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "abandoned.db")
	conn, _, modeReference, err := openSQLiteConnection(context.Background(), dsn)
	if err != nil {
		t.Fatalf("openSQLiteConnection(%q): %v", dsn, err)
	}
	if modeReference == nil {
		t.Fatal("modeReference is nil for a file: DSN, want a live descriptor to exercise release")
	}

	closeAbandonedOpen(conn, modeReference)

	if pingErr := conn.PingContext(context.Background()); pingErr == nil {
		t.Error("conn.PingContext succeeded after closeAbandonedOpen, want a closed *sql.DB")
	} else if !strings.Contains(pingErr.Error(), "database is closed") {
		t.Errorf("conn.PingContext error = %v, want \"database is closed\"", pingErr)
	}

	if closeErr := modeReference.Close(); closeErr == nil {
		t.Fatal("modeReference.Close() succeeded on second call, want an already-closed error: closeAbandonedOpen did not release it")
	} else if !errors.Is(closeErr, os.ErrClosed) {
		t.Fatalf("modeReference.Close() second-call error = %v, want errors.Is(os.ErrClosed)", closeErr)
	}
}

// TestDB_Close_IsIdempotent asserts DB.Close() can be called twice without
// error. Before the fix, the second call's closeModeReference(d.modeReference)
// re-closed the already-closed *os.File and returned a non-nil "close mode
// reference: ... file already closed" error even though d.conn.Close() (the
// stdlib *sql.DB) is itself documented idempotent. There is no fd-leak risk
// either way — *os.File refuses a second close outright regardless of this
// fix (see TestCloseAbandonedOpen_ReleasesConnAndModeReference above and
// TestDB_Close_ReleasesModeReferenceDescriptor in modeof_reconnect_test.go)
// — this is a behavioural consistency fix, not a resource-leak fix.
func TestDB_Close_IsIdempotent(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "idempotent-close.db")
	db, err := Open(context.Background(), dsn, "")
	if err != nil {
		t.Fatalf("Open(%q): %v", dsn, err)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("first Close: %v, want nil", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close: %v, want nil (idempotent)", err)
	}
}
