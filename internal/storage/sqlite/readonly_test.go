package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/decision"
)

// seedCurrentSchemaDB creates a fresh SQLite file at latestSQLiteSchemaVersion
// via the real (read-write) Open()/migration runner, closes it, and returns
// the file path — the "everything is fine" fixture every OpenReadOnly test
// below starts from.
func seedCurrentSchemaDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "readonly-fixture.db")
	seed, err := Open(context.Background(), path, "")
	if err != nil {
		t.Fatalf("seed Open(%q): %v", path, err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("seed Close: %v", err)
	}
	return path
}

// seedRawSchemaMigrationsRow creates a minimal SQLite file containing only a
// schema_migrations table with one (version, dirty) row — enough for
// verifyCurrentSchema to read without needing a full migrated app schema.
// Mirrors close_test.go's seedDirtyMigrationState technique (real DB state,
// no test-only injection seam).
func seedRawSchemaMigrationsRow(t *testing.T, path string, version int, dirty bool) {
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
	_, err = seed.ExecContext(context.Background(),
		`CREATE TABLE schema_migrations (version uint64, dirty bool);
		 CREATE UNIQUE INDEX version_unique ON schema_migrations (version);
		 INSERT INTO schema_migrations (version, dirty) VALUES (?, ?)`,
		version, dirty)
	if err != nil {
		t.Fatalf("seed schema_migrations row: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 1. Writes fail (SQLITE_READONLY) on a genuinely read-only connection, plus
//    the mutation-sensitivity companion proving the "file:" prefix is what
//    makes that true.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_ConnectionIsTrulyReadOnly(t *testing.T) {
	dbPath := seedCurrentSchemaDB(t)

	roDB, err := OpenReadOnly(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("OpenReadOnly(%q): %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := roDB.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	err = roDB.ExecContext(context.Background(), `CREATE TABLE mutation_probe (x)`)
	if err == nil {
		t.Fatal("write succeeded on an OpenReadOnly connection, want a SQLITE_READONLY-style error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "readonly") &&
		!strings.Contains(strings.ToLower(err.Error()), "read-only") {
		t.Errorf("error = %v, want it to mention read-only", err)
	}
}

// TestOpenReadOnly_MutationSelfProof_WithoutFilePrefixWriteSucceeds is the
// mutation-sensitivity companion to TestOpenReadOnly_ConnectionIsTrulyReadOnly
// above: it reproduces exactly the bug dsnReadOnly's own doc comment
// describes (modernc.org/sqlite v1.50.0 silently drops "?mode=ro" from a DSN
// that is not "file:"-prefixed) against the SAME seeded fixture, and asserts
// the OPPOSITE outcome — the write SUCCEEDS.
//
// This is what makes TestOpenReadOnly_ConnectionIsTrulyReadOnly a real proof
// and not an accidental pass: if a future edit ever removed the "file:"
// prefix from dsnReadOnly, that test's write-attempt would flip from
// "fails" to "succeeds" and go red. This test exists to make that failure
// mode concrete and pre-verified, not to protect production code directly —
// dsnReadOnly itself is already exercised for real by OpenReadOnly above.
func TestOpenReadOnly_MutationSelfProof_WithoutFilePrefixWriteSucceeds(t *testing.T) {
	dbPath := seedCurrentSchemaDB(t)

	brokenDSN := dbPath + "?mode=ro" // deliberately missing the "file:" prefix dsnReadOnly always adds
	conn, err := sql.Open("sqlite", brokenDSN)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", brokenDSN, err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	if _, err := conn.ExecContext(context.Background(), `CREATE TABLE mutation_probe (x)`); err != nil {
		t.Fatalf("write on non-\"file:\"-prefixed %q failed (%v); want it to SUCCEED — "+
			"this is the exact bug dsnReadOnly's \"file:\" prefix must prevent, and its "+
			"absence here would mean the mutation-sensitivity proof no longer holds", brokenDSN, err)
	}
}

// ---------------------------------------------------------------------------
// 2. Schema version behind latestSQLiteSchemaVersion → ErrSchemaNotCurrent.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_StaleVersionRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale.db")
	seedRawSchemaMigrationsRow(t, path, latestSQLiteSchemaVersion-1, false)

	_, err := OpenReadOnly(context.Background(), path, "")
	if err == nil {
		t.Fatal("OpenReadOnly(stale version) succeeded, want ErrSchemaNotCurrent")
	}
	if !errors.Is(err, ErrSchemaNotCurrent) {
		t.Errorf("error = %v, want errors.Is(ErrSchemaNotCurrent)", err)
	}
}

// ---------------------------------------------------------------------------
// 3. dirty=1 → ErrSchemaNotCurrent.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_DirtySchemaRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dirty.db")
	seedRawSchemaMigrationsRow(t, path, latestSQLiteSchemaVersion, true)

	_, err := OpenReadOnly(context.Background(), path, "")
	if err == nil {
		t.Fatal("OpenReadOnly(dirty=1) succeeded, want ErrSchemaNotCurrent")
	}
	if !errors.Is(err, ErrSchemaNotCurrent) {
		t.Errorf("error = %v, want errors.Is(ErrSchemaNotCurrent)", err)
	}
}

// ---------------------------------------------------------------------------
// 4. Symlinked path is refused.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_RefusesSymlink(t *testing.T) {
	realPath := seedCurrentSchemaDB(t)
	linkPath := filepath.Join(filepath.Dir(realPath), "readonly-symlink.db")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := OpenReadOnly(context.Background(), linkPath, "")
	if err == nil {
		t.Fatal("OpenReadOnly(symlink path) succeeded, want a hard error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %v, want it to mention refusing a symlink", err)
	}
}

// ---------------------------------------------------------------------------
// 5. Missing file: refused, and — critically — never created.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_MissingFileNeverCreatesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.db")

	_, err := OpenReadOnly(context.Background(), path, "")
	if err == nil {
		t.Fatal("OpenReadOnly(missing file) succeeded, want an error")
	}
	if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("OpenReadOnly(missing file) created %q (Lstat err = %v), want it to stay absent", path, statErr)
	}
}

// ---------------------------------------------------------------------------
// 6. A path containing "?" or "#" is rejected before any filesystem access.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_RejectsQueryAndFragmentCharsInPath(t *testing.T) {
	for _, path := range []string{
		"weird?mode=rw.db",
		"weird#frag.db",
	} {
		t.Run(path, func(t *testing.T) {
			_, err := OpenReadOnly(context.Background(), path, "")
			if err == nil {
				t.Fatalf("OpenReadOnly(%q) succeeded, want a rejection before any file access", path)
			}
			if !strings.Contains(err.Error(), "must not contain") {
				t.Errorf("error = %v, want it to mention the '?'/'#' rejection", err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// bonus: a successful OpenReadOnly can still read real data (not just "the
// write path is blocked") — proves the connection is usable, not just inert.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_ReadsExistingData(t *testing.T) {
	dbPath := seedCurrentSchemaDB(t)

	// Write a row via a normal read-write Open, close it, then read it back
	// through OpenReadOnly.
	rw, err := Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("re-Open for seeding: %v", err)
	}
	decisionStore := NewDecisionStore(rw)
	seedParams := decision.LogParams{
		Title: "test decision", Context: "ctx", Decision: "do it", Rationale: "because",
		Source: decision.SourceManual,
	}
	if _, err := decisionStore.Log(context.Background(), seedParams); err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	if err := rw.Close(); err != nil {
		t.Fatalf("close seeding connection: %v", err)
	}

	roDB, err := OpenReadOnly(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() {
		if err := roDB.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	got, err := NewDecisionStore(roDB).All(context.Background(), 10)
	if err != nil {
		t.Fatalf("All via readonly connection: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("All() via readonly connection = %d rows, want 1", len(got))
	}
}
