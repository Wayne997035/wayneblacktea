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
// 7. Side-car ("-wal"/"-shm") permissions must match the main db file's
//    permissions — pins the doc comment's "inherits the main file's
//    permissions, never broader" claim so a future umask or driver change
//    can't silently regress it to something more permissive (see
//    TestOpenReadOnly_SideCarPermissionsMatchMainFile_MutationSelfProof for
//    the self-proof that this assertion actually bites).
// ---------------------------------------------------------------------------

func TestOpenReadOnly_SideCarPermissionsMatchMainFile(t *testing.T) {
	dbPath := seedCurrentSchemaDB(t)

	// Deliberately set the main file to a non-default mode (0400, still
	// gosec G302-compliant — no broader than 0600) so this test proves the
	// side-cars COPY the main file's mode rather than just happening to
	// match it via process umask (empirically verified: a 0400 main file
	// produces 0400 "-wal"/"-shm" side-cars, not a umask-derived default).
	if err := os.Chmod(dbPath, 0o400); err != nil {
		t.Fatalf("Chmod main file: %v", err)
	}
	mainInfo, err := os.Lstat(dbPath)
	if err != nil {
		t.Fatalf("Lstat main file: %v", err)
	}
	wantPerm := mainInfo.Mode().Perm()

	roDB, err := OpenReadOnly(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("OpenReadOnly(%q): %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := roDB.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := dbPath + suffix
		info, err := os.Lstat(sidecar)
		if err != nil {
			t.Fatalf("Lstat side-car %q: %v (OpenReadOnly of a WAL-mode db is expected to create it)", sidecar, err)
		}
		if got := info.Mode().Perm(); got != wantPerm {
			t.Errorf("side-car %q perm = %o, want %o (must inherit the main file's permissions, never broader)",
				sidecar, got, wantPerm)
		}
	}
}

// TestOpenReadOnly_SideCarPermissionsMatchMainFile_MutationSelfProof is the
// mutation-sensitivity companion to the test above (same pattern as
// TestOpenReadOnly_MutationSelfProof_WithoutFilePrefixWriteSucceeds): it
// deliberately asserts a WRONG expected permission (the main file's mode
// XORed with a bit that can never legitimately appear on a side-car created
// by this code path) and requires that assertion to fail, proving the
// perm-comparison in the test above is not a vacuously-true check.
func TestOpenReadOnly_SideCarPermissionsMatchMainFile_MutationSelfProof(t *testing.T) {
	dbPath := seedCurrentSchemaDB(t)
	if err := os.Chmod(dbPath, 0o400); err != nil {
		t.Fatalf("Chmod main file: %v", err)
	}
	mainInfo, err := os.Lstat(dbPath)
	if err != nil {
		t.Fatalf("Lstat main file: %v", err)
	}
	wrongWantPerm := mainInfo.Mode().Perm() ^ 0o004 // deliberately wrong

	roDB, err := OpenReadOnly(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("OpenReadOnly(%q): %v", dbPath, err)
	}
	t.Cleanup(func() {
		if err := roDB.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	sidecar := dbPath + "-wal"
	info, err := os.Lstat(sidecar)
	if err != nil {
		t.Fatalf("Lstat side-car %q: %v", sidecar, err)
	}
	if got := info.Mode().Perm(); got == wrongWantPerm {
		t.Fatalf("self-proof failed: side-car perm %o unexpectedly matched the deliberately-wrong want %o — "+
			"the assertion in TestOpenReadOnly_SideCarPermissionsMatchMainFile would not catch a real regression",
			got, wrongWantPerm)
	}
	// Expected outcome: mismatch (proves the comparison is discriminating).
}

// ---------------------------------------------------------------------------
// 8. A path containing "%" is rejected before any filesystem access — closes
//    the percent-decoding bypass where os.Lstat validates a literal,
//    non-symlink decoy file but SQLite's own URI parser (reached via
//    dsnReadOnly's "file:" prefix) decodes e.g. "%2f" to "/" and actually
//    opens a DIFFERENT file one or more directory levels away. Empirically
//    reproduced against modernc.org/sqlite v1.50.0 before this fix: a
//    0-byte regular file named "sub%2freal.db" passed the old
//    symlink-only check, then OpenReadOnly silently opened "sub/real.db"
//    instead.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_RejectsPercentInPath(t *testing.T) {
	for _, path := range []string{
		"weird%2fescape.db",
		"weird%00null.db",
	} {
		t.Run(path, func(t *testing.T) {
			_, err := OpenReadOnly(context.Background(), path, "")
			if err == nil {
				t.Fatalf("OpenReadOnly(%q) succeeded, want a rejection before any file access", path)
			}
			if !strings.Contains(err.Error(), "must not contain") {
				t.Errorf("error = %v, want it to mention the '%%' rejection", err)
			}
		})
	}
}

// TestOpenReadOnly_PercentEncodingCannotRedirectToADifferentFile is the
// end-to-end reproduction of the bypass described above: it builds a
// directory layout where the LITERAL (validated) path and the
// percent-DECODED path point at two different files — a 0-byte decoy at the
// literal path, and a real, current-schema db one directory level away at
// the decoded path — and asserts OpenReadOnly refuses the literal path
// outright rather than silently opening the decoded target.
func TestOpenReadOnly_PercentEncodingCannotRedirectToADifferentFile(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "sub")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Real target: a valid current-schema db one directory level below base,
	// reachable ONLY if "%2f" in the decoy's name below gets decoded to "/".
	realPath := filepath.Join(sub, "real.db")
	seed, err := Open(context.Background(), realPath, "")
	if err != nil {
		t.Fatalf("seed Open(%q): %v", realPath, err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("seed Close: %v", err)
	}

	// Decoy: an empty, non-symlink regular file living directly in base/,
	// whose literal name contains a percent-encoded "/" escape.
	decoyPath := filepath.Join(base, "sub%2freal.db")
	if err := os.WriteFile(decoyPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", decoyPath, err)
	}

	_, err = OpenReadOnly(context.Background(), decoyPath, "")
	if err == nil {
		t.Fatal("OpenReadOnly(percent-encoded decoy path) succeeded — the connection opened a different, " +
			"decoded file instead of being refused at validation time")
	}
	if !strings.Contains(err.Error(), "must not contain") {
		t.Errorf("error = %v, want it to mention the '%%' rejection", err)
	}
}

// ---------------------------------------------------------------------------
// 9. A symlinked INTERMEDIATE directory component is refused, not just a
//    symlinked final path component. Note this is NOT "refuse any symlink
//    anywhere in the path": on this very machine (and on every macOS CI
//    runner), t.TempDir() itself lives under /var, which is a symlink to
//    /private/var — refusing ANY ancestor symlink would make every test in
//    this file fail on Darwin. What this test instead pins is the
//    consistency guarantee: os.Lstat used to check the full literal path
//    string, and dsnReadOnly/sql.Open separately (and independently, at a
//    LATER time) resolved that same literal string again — meaning an
//    intermediate symlinked directory could be repointed by an attacker
//    between the two resolutions with SQLite none the wiser. Resolving the
//    directory ONCE via filepath.EvalSymlinks and reusing that resolved,
//    concrete path for both the Lstat check and the sql.Open call closes
//    that specific re-resolution race (the residual TOCTOU window — an
//    attacker swapping the FINAL, already-resolved file between Lstat and
//    Open — is a different, narrower race documented on
//    validateReadOnlyPath and is not eliminated). A deterministic unit test
//    cannot exercise a race by definition; this test instead pins the safe,
//    steady-state behavior: reading THROUGH a symlinked intermediate
//    directory reaches the real target's actual data, not some other file.
// ---------------------------------------------------------------------------

func TestOpenReadOnly_ReadsThroughIntermediateSymlinkedDirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real-dir")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	realPath := filepath.Join(realDir, "target.db")
	seed, err := Open(context.Background(), realPath, "")
	if err != nil {
		t.Fatalf("seed Open(%q): %v", realPath, err)
	}
	decisionStore := NewDecisionStore(seed)
	seedParams := decision.LogParams{
		Title: "reached via symlinked dir", Context: "ctx", Decision: "do it", Rationale: "because",
		Source: decision.SourceManual,
	}
	if _, err := decisionStore.Log(context.Background(), seedParams); err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("seed Close: %v", err)
	}

	linkedDir := filepath.Join(root, "innocent-looking-dir")
	if err := os.Symlink(realDir, linkedDir); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	// Final path component ("target.db") is not itself a symlink — only the
	// directory it lives in, reached via linkedDir, is.
	pathThroughSymlinkedDir := filepath.Join(linkedDir, "target.db")

	roDB, err := OpenReadOnly(context.Background(), pathThroughSymlinkedDir, "")
	if err != nil {
		t.Fatalf("OpenReadOnly(path through a symlinked intermediate directory): %v — the real target is a "+
			"legitimate, current-schema db, so this must succeed and must read the SAME file the validation "+
			"step resolved, not diverge from it", err)
	}
	t.Cleanup(func() {
		if err := roDB.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	got, err := NewDecisionStore(roDB).All(context.Background(), 10)
	if err != nil {
		t.Fatalf("All via readonly connection through symlinked dir: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("All() via readonly connection through symlinked dir = %d rows, want 1 (the seeded row from "+
			"the REAL target — a different count/zero would mean the connection opened a different file than "+
			"the one validated)", len(got))
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
