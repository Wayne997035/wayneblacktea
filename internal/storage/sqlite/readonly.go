package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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
// writes, no file creation, no chmod — and verifies its schema_migrations
// row matches latestSQLiteSchemaVersion with dirty=0 before returning.
// Intended for hook binaries (wbt context session-start) that must read an
// existing, possibly-untrusted-directory SQLite file without ever mutating
// it — see backend-security-design.md §2.2/§5.1/§5.3 and the A5a dispatch's
// M-1 threat model (a repo-shipped .db file must never be a write vector,
// and a hook process must never be the thing that migrates a shared DB out
// from under a longer-lived process).
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
	if err := validateReadOnlyPath(path); err != nil {
		return nil, err
	}

	dsn := dsnReadOnly(path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite readonly open %q: %w", path, err)
	}
	// Single connection, matching Open()'s single-writer rationale (db.go) —
	// there is no writer to serialise here, but a second connection would
	// still be a second independent SQLite VFS handle for no benefit.
	conn.SetMaxOpenConns(1)

	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite readonly ping %q: %w", path, err)
	}

	if err := verifyCurrentSchema(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	// modeReference: nil — OpenReadOnly creates nothing (no new file, no
	// modeof mode-reference descriptor to keep alive across reconnects; see
	// DB.modeReference's own doc comment on why Open() needs one and this
	// path does not). DB.Close() treats a nil modeReference as a no-op
	// (db.go closeModeReference), so this is safe.
	return &DB{conn: conn, modeReference: nil, workspaceID: workspaceID}, nil
}

// validateReadOnlyPath rejects any path shape OpenReadOnly is not prepared
// to safely turn into a mode=ro URI, and refuses a symlinked path outright.
//
//   - "?" or "#" in path would be interpreted as the start of a query
//     string / fragment once dsnReadOnly prefixes it with "file:", letting a
//     caller-controlled path smuggle in extra URI parameters — including one
//     that could override mode= back to a writable mode (SQLite's URI
//     parsing takes the LAST occurrence of a repeated parameter; see
//     modernc.org/sqlite's applyQueryParams and this package's own
//     prependModeOf comment for the general shape of this hazard).
//   - os.Lstat (not os.Stat, which follows symlinks) rejects a path whose
//     final component is itself a symlink, so a malicious repo cannot point
//     SQLITE_PATH at an arbitrary target outside the intended directory
//     (backend-security-design.md §2.2).
func validateReadOnlyPath(path string) error {
	if path == "" {
		return errors.New("sqlite readonly: empty path")
	}
	if strings.ContainsAny(path, "?#") {
		return fmt.Errorf("sqlite readonly: path %q must not contain '?' or '#'", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("sqlite readonly: stat %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("sqlite readonly: refusing symlink path %q", path)
	}
	return nil
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
