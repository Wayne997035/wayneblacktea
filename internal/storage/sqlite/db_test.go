package sqlite_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
)

// permOf is a small stat-and-extract helper shared by the permission tests
// below to keep each test body focused on what it's actually proving.
func permOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return info.Mode().Perm()
}

// TestOpen_FileBackedDBIsChmod0600 proves the security fix for PR16's Major
// finding: the SQLite driver creates new files at 0666&~umask (commonly
// 0644, group/world-readable), but this DB holds handoffs/decisions/
// knowledge (personal data) — backend-security-design.md §4.1. Open MUST
// restrict the file to 0600 regardless of the caller (hook/server/mcp all
// share this function).
func TestOpen_FileBackedDBIsChmod0600(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "perm-check.db")

	d, err := sqlite.Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if perm := permOf(t, dbPath); perm != 0o600 {
		t.Errorf("DB file mode = %o, want 0600", perm)
	}
}

// TestOpen_PreExistingPermissiveFileGetsTightened proves Open actively
// corrects an already-permissive file (e.g. one created by a pre-fix binary,
// like the three stray 0644 wayneblacktea.db files found on disk) rather
// than only warning about it — stronger than the read-time warn-only pattern
// in backend-security-design.md §4.1 since Open is also the file's writer.
//
// It also covers PR149 review Major 1: the main file's chmod does NOT
// retroactively fix pre-existing "-wal"/"-shm" siblings left over by a
// pre-fix binary (e.g. wayneblacktea.db-wal / -shm found at 0644 on disk
// alongside a since-tightened main file) — Open must chmod all three.
func TestOpen_PreExistingPermissiveFileGetsTightened(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "already-permissive.db")
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"

	// Create+open once, then manually widen permissions to simulate a
	// pre-fix-created file, then re-open and confirm it's tightened back.
	d1, err := sqlite.Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("sqlite.Open (first): %v", err)
	}
	if err := d1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Seed all three paths at 0644 regardless of what the first Open/Close
	// left behind (SQLite may or may not have removed -wal/-shm on close) —
	// this deterministically reproduces "pre-fix binary left permissive
	// siblings on disk" rather than depending on WAL-checkpoint timing.
	for _, p := range []string{dbPath, walPath, shmPath} {
		if _, statErr := os.Stat(p); os.IsNotExist(statErr) {
			//nolint:gosec // G306: deliberately seeding at 0644 to simulate a pre-fix-created file, not creating credentials
			if err := os.WriteFile(p, []byte("stray"), 0o644); err != nil {
				t.Fatalf("seeding %q: %v", p, err)
			}
		}
		//nolint:gosec // G302: deliberately widening perms to simulate a pre-fix-created file, not creating credentials
		if err := os.Chmod(p, 0o644); err != nil {
			t.Fatalf("chmod to simulate pre-fix file %q: %v", p, err)
		}
	}

	d2, err := sqlite.Open(context.Background(), dbPath, "")
	if err != nil {
		t.Fatalf("sqlite.Open (second): %v", err)
	}
	t.Cleanup(func() { _ = d2.Close() })

	for _, p := range []string{dbPath, walPath, shmPath} {
		if perm := permOf(t, p); perm != 0o600 {
			t.Errorf("%s mode after re-Open = %o, want 0600 (Open must re-tighten permissive files, including -wal/-shm siblings)", p, perm)
		}
	}
}

// TestOpen_FileURIWithQueryStringIsChmod0600 proves PR149 review Major 2:
// os.Chmod("file:/x/y.db") fails ENOENT (the DSN is a URI, not a literal
// path), which used to leave the file at its driver-created 0644 with only a
// slog.Warn — silently defeating the entire permission-tightening pass for
// every "file:" DSN, including the ones this package's own tests
// (outcome_test.go, gtd_test.go) and factory.go's SQLitePathFromEnv doc
// comment treat as a normal, supported form. Open must resolve the DSN to
// the real path first, including when a query string is attached.
func TestOpen_FileURIWithQueryStringIsChmod0600(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "file-uri-check.db")
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(2000)"

	d, err := sqlite.Open(context.Background(), dsn, "")
	if err != nil {
		t.Fatalf("sqlite.Open(%q): %v", dsn, err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if perm := permOf(t, dbPath); perm != 0o600 {
		t.Errorf("DB file mode = %o, want 0600 (file: URI with query string must still resolve to the real path)", perm)
	}
}

// TestOpen_FileURIModeMemoryDoesNotTouchDisk proves the "mode=memory" query
// param (which the underlying SQLite C library — not modernc.org/sqlite's Go
// layer — interprets as an in-memory database, only when the DSN carries the
// "file:" scheme; see sqliteFilePath's doc comment) is recognised and never
// mistaken for a file-backed DSN: Open must succeed AND must never create a
// file at the given path.
func TestOpen_FileURIModeMemoryDoesNotTouchDisk(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "should-not-exist.db")
	dsn := "file:" + dbPath + "?mode=memory"

	d, err := sqlite.Open(context.Background(), dsn, "")
	if err != nil {
		t.Fatalf("sqlite.Open(%q): %v", dsn, err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, statErr := os.Stat(dbPath); statErr == nil {
		t.Errorf("mode=memory DSN created a real file at %q — it should never touch disk", dbPath)
	}
}

// TestOpen_RefusesToChmodSymlinkTarget proves PR149 review Minor 3: chmod
// must not follow a symlink. The realistic threat is `wbt mcp` running
// godotenv.Load() (internal/cli/mcp.go) against an untrusted repo's CWD,
// where a malicious .env can steer SQLITE_PATH at a symlink and use wbt as a
// confused deputy to chmod 0600 a file it neither created nor owns the
// lifecycle of. Open must hard-fail rather than silently chmod through it.
func TestOpen_RefusesToChmodSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real.db")
	linkPath := filepath.Join(dir, "link.db")

	d0, err := sqlite.Open(context.Background(), realPath, "")
	if err != nil {
		t.Fatalf("sqlite.Open (seed real file): %v", err)
	}
	if err := d0.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if _, err := sqlite.Open(context.Background(), linkPath, ""); err == nil {
		t.Error("sqlite.Open(symlink dsn) succeeded, want a hard error refusing to chmod through the symlink")
	}
}

// TestOpen_NewDBNeverObservablyPermissive proves PR149 review Minor 4: the
// TOCTOU window between the driver creating the file (0666&~umask) and
// Open's chmod pass tightening it back down. A watcher goroutine polls the
// file's mode in a tight loop concurrently with Open and fails the test if
// it ever observes anything other than "doesn't exist yet" or "0600".
//
// This is deterministic in practice, not merely probabilistic:
// os.OpenFile(path, O_CREATE|O_EXCL, 0600) applies the mode atomically
// inside the open(2) syscall itself, so there is no OS-level window in which
// the file exists at any other mode. A regression back to "let the driver
// create the file, chmod afterward" reopens a real window that a tight
// Lstat-polling loop reliably observes, since the driver's own SQLite-level
// file setup (well over a syscall's worth of work) is far slower than a
// single os.Lstat call.
func TestOpen_NewDBNeverObservablyPermissive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "toctou-check.db")

	stop := make(chan struct{})
	done := make(chan os.FileMode) // non-zero => the bad mode observed, else 0

	go func() {
		var badMode os.FileMode
		for {
			select {
			case <-stop:
				done <- badMode
				return
			default:
			}
			if info, err := os.Lstat(dbPath); err == nil {
				if perm := info.Mode().Perm(); perm != 0o600 {
					badMode = info.Mode()
				}
			}
		}
	}()

	d, err := sqlite.Open(context.Background(), dbPath, "")
	close(stop)
	badMode := <-done
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if badMode != 0 {
		t.Errorf("watcher observed %q at mode %o during Open — a TOCTOU window exists between file creation and chmod", dbPath, badMode.Perm())
	}
}

// TestOpen_InMemorySkipsChmod proves ":memory:" (no backing file) doesn't
// make Open fail, AND that Open never treats ":memory:" as a real filesystem
// path along the way (PR149 review three-army Major 2: the previous version
// of this test only asserted err == nil, which chmod failures never
// surfaced — they only ever slog.Warn'd — so deleting the `dsn != ":memory:"`
// guard entirely still passed. Now that a resolved file-backed DSN's chmod
// failure is a hard error (see secureDBFile), removing the ":memory:"
// special case has an observable, deterministic effect: sqliteFilePath would
// treat ":memory:" as a literal relative filename ("path == \":memory:\""
// happens to also be syntactically valid), so Open would try to
// pre-create/chmod a real file named ":memory:" in the working directory —
// which SUCCEEDS (":memory:" is a perfectly legal, if unusual, filename), so
// a plain err == nil assertion still can't see the regression. Checking that
// no such file was created can.
//
// Verified by deliberately commenting out both ":memory:" early-returns in
// sqliteFilePath and re-running this test: it goes red because a real file
// named ":memory:" appears in the package directory (see engineer report for
// the command transcript).
func TestOpen_InMemorySkipsChmod(t *testing.T) {
	// Guard against a stray ":memory:" file left by a previous failed run so
	// this test doesn't get a false pass by testing against leftover state.
	_ = os.Remove(":memory:")
	t.Cleanup(func() { _ = os.Remove(":memory:") })

	d, err := sqlite.Open(context.Background(), ":memory:", "")
	if err != nil {
		t.Fatalf("sqlite.Open(:memory:): %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if _, statErr := os.Stat(":memory:"); statErr == nil {
		t.Error(`sqlite.Open(":memory:") created a real file named ":memory:" in the ` +
			`working directory — the in-memory DSN guard was bypassed and the file-backed ` +
			`pre-create/chmod path ran against it`)
	}
}
