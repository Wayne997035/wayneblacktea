package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestOpen_ReconnectsAfterPoolDropsConnection is a regression test for a bug
// where a "file:"-prefixed DSN's SQLite main database failed to reconnect
// with "disk I/O error (10)" once database/sql discarded the first physical
// connection and re-dialed the stored DSN for a fresh one.
//
// Root cause: openSQLiteConnection used to close the modeof mode-reference
// descriptor (the anonymous 0600 file that prependModeOf/secureCreationDSN
// points SQLite's modeof URI option at) right after the FIRST connection's
// PingContext succeeded. But database/sql retains the DSN string for the
// entire life of the *sql.DB and silently re-dials it verbatim on every pool
// reconnect (idle-conn eviction, MaxLifetime expiry, a dropped connection,
// ...), and SQLite resolves modeof on EVERY main-db open — not only file
// creation. So any reconnect after that descriptor was closed always failed
// stat()ing a dead (or worse, fd-recycled) /dev/fd/N target with
// SQLITE_IOERR_FSTAT. The fix keeps the descriptor open for the *DB's entire
// life and closes it only in DB.Close() (see DB.modeReference).
//
// Mechanism chosen to force the reconnect: SetMaxIdleConns(0). Per
// database/sql's documented behaviour, MaxIdleConns<=0 means a connection
// returned to the pool after use is closed instead of retained, so every
// subsequent query MUST dial a brand-new physical connection through the
// stored DSN rather than reuse an idle one. This is deterministic (no
// timing/expiry races), unlike SetConnMaxLifetime, which is only checked
// when a connection is returned/acquired and would need a sleep to
// guarantee expiry before the next query runs.
func TestOpen_ReconnectsAfterPoolDropsConnection(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "reconnect.db")
	db, err := Open(context.Background(), dsn, "")
	if err != nil {
		t.Fatalf("Open(%q): %v", dsn, err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	conn := db.SqlConn()

	// Baseline: the first physical connection (opened by Open/Ping above)
	// works.
	if _, err := conn.ExecContext(context.Background(), `SELECT 1`); err != nil {
		t.Fatalf("baseline query: %v", err)
	}

	// From here on, every connection handed back to the pool after a query
	// is closed rather than pooled, forcing every following query to dial a
	// brand-new physical connection through the DSN retained by database/sql
	// (modeof and all).
	conn.SetMaxIdleConns(0)

	// Each of these queries drops the previous connection on return and
	// dials a fresh one for the next query. Before the fix, this failed with
	// "disk I/O error (10)" starting at the very first reconnect because the
	// modeof mode-reference descriptor was already closed.
	for i := 0; i < 3; i++ {
		if _, err := conn.ExecContext(context.Background(), `SELECT 1`); err != nil {
			t.Fatalf("reconnect query #%d: %v", i+1, err)
		}
	}
}

// TestDB_Close_ReleasesModeReferenceDescriptor proves the modeof mode
// reference (see DB.modeReference) does not leak: it must be released when
// DB.Close() runs, not held open for the process's remaining lifetime. This
// is the flip side of the reconnect fix above — keeping the descriptor open
// across the *DB's life (fixing the reconnect bug) must not turn into "never
// closing it at all" (a slow fd leak for any caller that opens many
// short-lived DBs, e.g. tests or `wbt` CLI subcommands).
//
// Verified directly against the *os.File DB.Close() is supposed to release,
// rather than by counting process-wide descriptors: /dev/fd enumeration is
// unreliable on Darwin for descriptors of this kind (see the portability
// caveat already documented on newOwnerOnlyModeReference above), so instead
// this asserts that a second Close() on the exact same *os.File DB.Close()
// used fails with "already closed" — which is only true if DB.Close() really
// released it. A leaked descriptor would let this second Close succeed.
func TestDB_Close_ReleasesModeReferenceDescriptor(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "fdleak.db")
	db, err := Open(context.Background(), dsn, "")
	if err != nil {
		t.Fatalf("Open(%q): %v", dsn, err)
	}
	reference := db.modeReference
	if reference == nil {
		t.Fatal("db.modeReference is nil for a file: DSN, want a live descriptor")
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := reference.Close(); err == nil {
		t.Fatal("modeReference.Close() succeeded on second call, want an already-closed error: DB.Close() did not release it")
	} else if !errors.Is(err, os.ErrClosed) {
		t.Fatalf("modeReference.Close() second-call error = %v, want errors.Is(os.ErrClosed)", err)
	}
}
