package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrSchemaNotCurrent is returned by OpenReadOnly when the database at path
// does not have a schema_migrations row matching latestSQLiteSchemaVersion
// with dirty=0 — including a database with no schema_migrations table at
// all (a pre-migration-runner snapshot, or a brand-new/empty file).
// OpenReadOnly never runs migrations (a write operation on what the caller
// has explicitly asked to be a read-only connection — see dsnReadOnly), so
// a stale or unreadable schema is a hard error rather than a silent
// partial-schema open.
var ErrSchemaNotCurrent = errors.New("sqlite: database schema is not current (readonly connections never migrate)")

// OpenReadOnly opens path strictly read-only — no migrations, no PRAGMA
// writes to the database content, no chmod — and verifies its
// schema_migrations row matches latestSQLiteSchemaVersion with dirty=0
// before returning.
//
// It does NOT guarantee zero filesystem writes: opening a WAL-mode database
// with mode=ro still causes SQLite to create "-wal"/"-shm" side-car files
// next to it if they are not already present (empirically verified against
// modernc.org/sqlite v1.50.0 — the "-wal" side-car is created 0 bytes and
// stays 0 bytes since a read-only connection never appends WAL frames; the
// "-shm" side-car is the shared-memory index SQLite always mmaps for a
// WAL-mode db, regardless of read/write mode). Both side-cars are created
// with the SAME permission bits as the main db file (verified: chmod-ing
// the main file to 0400 before OpenReadOnly produces 0400 side-cars, not a
// process-umask default) — see
// TestOpenReadOnly_SideCarPermissionsMatchMainFile in readonly_test.go for
// the pinned proof. They are NOT removed by DB.Close(). This is an accepted
// trade-off, not a bug: it means a hook process running against a
// read-only-intended directory can leave two small files behind (polluting
// e.g. `git status` on a cloned repo), but it cannot leak data (the "-wal"
// side-car carries no WAL frames from this connection) and it cannot widen
// the file's exposure (permissions always match the main file, never
// broader).
//
// Intended for hook binaries (wbt context session-start) that must read an
// existing, possibly-untrusted-directory SQLite file without ever mutating
// its CONTENT — see backend-security-design.md §2.2/§5.1/§5.3 and the A5a
// dispatch's M-1 threat model (a repo-shipped .db file must never be a
// write vector, and a hook process must never be the thing that migrates a
// shared DB out from under a longer-lived process).
//
// path MUST be a plain filesystem path, not a "file:" URI or any DSN with a
// query string or fragment — OpenReadOnly builds the URI form itself (see
// dsnReadOnly) so the mode=ro query parameter can never be stripped,
// duplicated, or overridden by caller-controlled input.
//
// Returns the package's own *DB (not *sql.DB) so callers can pass it
// straight into every NewXStore constructor in this package unchanged —
// those all take *DB (see e.g. NewGTDStore, NewDecisionStore).
func OpenReadOnly(ctx context.Context, path, workspaceID string) (*DB, error) {
	resolvedPath, err := validateReadOnlyPath(path)
	if err != nil {
		return nil, err
	}

	dsn := dsnReadOnly(resolvedPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite readonly open %q: %w", resolvedPath, err)
	}
	// Single connection, matching Open()'s single-writer rationale (db.go) —
	// there is no writer to serialise here, but a second connection would
	// still be a second independent SQLite VFS handle for no benefit.
	conn.SetMaxOpenConns(1)

	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite readonly ping %q: %w", resolvedPath, err)
	}

	if err := verifyCurrentSchema(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// modeReference: nil — OpenReadOnly never creates its OWN modeof
	// mode-reference descriptor (see DB.modeReference's own doc comment on
	// why Open() needs one and this path does not); it may still cause
	// SQLite to create "-wal"/"-shm" side-cars (see this function's doc
	// comment above), but those are unrelated to modeReference and need no
	// descriptor kept alive across reconnects here. DB.Close() treats a nil
	// modeReference as a no-op (db.go closeModeReference), so this is safe.
	return &DB{conn: conn, modeReference: nil, workspaceID: workspaceID}, nil
}

// validateReadOnlyPath rejects any path shape OpenReadOnly is not prepared
// to safely turn into a mode=ro URI, refuses a path whose FINAL component
// is itself a symlink outright, and returns the RESOLVED path OpenReadOnly
// must use for both the Lstat check above and the sql.Open call below —
// using two different strings for "what we validated" and "what we
// actually open" is exactly the class of bug this function used to have
// (see the bullets below), so the resolved path is the only path callers
// may hand to dsnReadOnly.
//
//   - "?", "#", or "%" in path are all rejected outright rather than
//     validated-around. "?"/"#" would be interpreted as the start of a
//     query string / fragment once dsnReadOnly prefixes path with "file:",
//     letting a caller-controlled path smuggle in extra URI parameters —
//     including one that could override mode= back to a writable mode
//     (SQLite's URI parsing takes the LAST occurrence of a repeated
//     parameter; see modernc.org/sqlite's applyQueryParams and this
//     package's own prependModeOf comment for the general shape of this
//     hazard). "%" is rejected for a DIFFERENT reason: once dsnReadOnly's
//     "file:" prefix routes the DSN through SQLite's own URI parser (see
//     that function's doc comment), percent sequences are decoded by
//     SQLITE's C layer — NOT by this package or by os.Lstat. Empirically
//     verified: a literal, non-symlink regular file whose NAME contains
//     "sub%2freal.db" passes the Lstat-not-a-symlink check below untouched,
//     while sql.Open on "file:.../sub%2freal.db?mode=ro" has SQLite decode
//     "%2f" to "/" and actually open a completely different file
//     ("sub/real.db", one directory level down) — the validated target and
//     the opened target silently diverge. Go's path/filepath package has no
//     matching percent-decode step to call here to keep the two in sync, so
//     outright rejection is the only fix that doesn't require reimplementing
//     SQLite's own URI-percent-decoder. This mirrors the existing "?"/"#"
//     rejection and accepts the same trade-off: a legitimate filename
//     containing a literal "%" can no longer be opened via OpenReadOnly.
//   - "\x00", "\r", "\n" are rejected because they break path semantics
//     differently across OS layers (backend-security-design.md §2.2) — a
//     defence-in-depth check alongside the existing OS-level rejection of
//     embedded NUL bytes.
//   - The directory portion of path is resolved with filepath.EvalSymlinks
//     BEFORE the symlink check on the final component. This does NOT
//     refuse an intermediate symlinked directory — plain os.Lstat(path)
//     already transparently followed intermediate directory symlinks
//     exactly like this does (both Lstat and the underlying open() syscall
//     resolve every path component except a final symlink identically;
//     empirically confirmed no behavior change for that case), and
//     refusing ANY ancestor symlink outright is not viable on this
//     codebase's own dev/CI platform: t.TempDir() on Darwin lives under
//     /var, itself a symlink to /private/var, so a blanket ancestor-symlink
//     refusal would fail every test in this file on macOS. What resolving
//     the directory DOES change: the old code re-resolved the same
//     symlinked-directory path string TWICE, independently, at two
//     different times — once inside os.Lstat, once (later) inside
//     sql.Open's own path resolution — leaving a window where an attacker
//     could repoint the intermediate symlink between those two
//     resolutions and have SQLite open a different real-dir than the one
//     Lstat checked. Resolving once here and reusing that same resolved,
//     concrete path for both Lstat and dsnReadOnly collapses that specific
//     re-resolution race for the DIRECTORY portion of the path.
//   - os.Lstat (not os.Stat, which follows symlinks) on the resolved
//     directory + the ORIGINAL final path component still rejects a path
//     whose final component is itself a symlink, so a malicious repo cannot
//     point SQLITE_PATH directly at an arbitrary symlinked target
//     (backend-security-design.md §2.2).
//
// This narrows, but does NOT eliminate, the TOCTOU window between this
// validation and the sql.Open call in OpenReadOnly: an attacker who can
// replace the resolved LEAF file with a symlink in between those two calls
// still wins a race, but the prize shrinks from "the writer could reconnect
// and migrate/write through it" to "the connection reads a different file
// read-only" — OpenReadOnly's connection is unconditionally opened with
// mode=ro, so no write or migration can ever land through that race either
// way.
func validateReadOnlyPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("sqlite readonly: empty path")
	}
	if strings.ContainsAny(path, "\x00\r\n") {
		return "", fmt.Errorf("sqlite readonly: path %q must not contain a null byte or line break", path)
	}
	if strings.ContainsAny(path, "?#%") {
		return "", fmt.Errorf("sqlite readonly: path %q must not contain '?', '#', or '%%'", path)
	}

	dir, base := filepath.Dir(path), filepath.Base(path)
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("sqlite readonly: resolve directory of %q: %w", path, err)
	}
	resolvedPath := filepath.Join(resolvedDir, base)

	info, err := os.Lstat(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("sqlite readonly: stat %q: %w", resolvedPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("sqlite readonly: refusing symlink path %q", resolvedPath)
	}
	return resolvedPath, nil
}

// dsnReadOnly builds the URI OpenReadOnly connects with. The "file:" prefix
// is load-bearing, not cosmetic: modernc.org/sqlite v1.50.0's newConn (see
// conn.go) strips everything from the first "?" onward from the DSN BEFORE
// opening, UNLESS the DSN already starts with "file:" — a bare
// "path?mode=ro" DSN silently loses "?mode=ro" and SQLite opens it
// read-write. Only the "file:"-prefixed form reaches SQLite's own URI
// parser (gated by the SQLITE_OPEN_URI flag the driver always passes), which
// is what actually enforces mode=ro at the C-library level regardless of the
// SQLITE_OPEN_READWRITE|SQLITE_OPEN_CREATE flags the driver also always
// passes — see TestOpenReadOnly_MutationSelfProof_WithoutFilePrefixWriteSucceeds
// in readonly_test.go for the empirical proof this comment is describing.
func dsnReadOnly(path string) string {
	return "file:" + path + "?mode=ro"
}

// verifyCurrentSchema gates OpenReadOnly on an exact, clean schema version
// match. Any failure to read schema_migrations (missing table because the
// file predates the migration runner or was never migrated, or any other
// scan error) is treated the same as an explicit version mismatch: this
// connection deliberately never migrates, so "can't tell what version this
// is" is not meaningfully different from "not current" to the caller — both
// mean OpenReadOnly must refuse rather than serve a possibly-partial schema.
func verifyCurrentSchema(ctx context.Context, conn *sql.DB) error {
	var version int
	var dirty bool
	err := conn.QueryRowContext(ctx, `SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty)
	if err != nil {
		return fmt.Errorf("%w: reading schema_migrations: %w", ErrSchemaNotCurrent, err)
	}
	if dirty {
		return fmt.Errorf("%w: schema_migrations.dirty=1", ErrSchemaNotCurrent)
	}
	if version != latestSQLiteSchemaVersion {
		return fmt.Errorf("%w: schema_migrations.version=%d, want %d", ErrSchemaNotCurrent, version, latestSQLiteSchemaVersion)
	}
	return nil
}
