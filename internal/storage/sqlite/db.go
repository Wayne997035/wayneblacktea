package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"
)

// DB is the package-internal connection wrapper. It holds the *sql.DB plus
// the optional workspace UUID (echoing the Postgres stores' Init-time
// scoping). Stores share this and add their own typed methods on top.
type DB struct {
	conn        *sql.DB
	workspaceID string // empty = legacy unscoped mode
}

// Open creates a new DB by opening dsn (e.g. "file:wbt.db" or ":memory:") and
// bringing its schema up to date via the embedded golang-migrate migration
// set (migrations/sqlite/); see runMigrations for the adoption-vs-replay
// logic. workspaceID may be empty. schema.sql is no longer the runtime
// authority — see its header comment.
func Open(ctx context.Context, dsn, workspaceID string) (*DB, error) {
	// Pre-create the file at 0600 *before* the driver ever touches it. The
	// SQLite driver's own file creation is 0666&~umask (commonly 0644); doing
	// that first and chmod-ing afterward (the previous implementation) leaves
	// a real window in which the file exists, group/world-readable, with an
	// open fd. This DB holds handoffs/decisions/knowledge — credential-
	// adjacent personal data (backend-security-design.md §4.1) — so closing
	// that window matters. A pre-existing file returns EEXIST here and is
	// left untouched; the chmod pass below re-tightens it instead.
	if path, ok := sqliteFilePath(dsn); ok {
		if err := preCreateSecureFile(path); err != nil {
			return nil, fmt.Errorf("sqlite pre-create %q: %w", path, err)
		}
	}
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open %q: %w", dsn, err)
	}
	// Serialise all writes through a single connection. SQLite has no
	// multi-writer concurrency; a pool > 1 triggers SQLITE_BUSY under concurrent
	// goroutines. Readers coexist via WAL mode set below.
	conn.SetMaxOpenConns(1)
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite ping %q: %w", dsn, err)
	}
	// Restrict the main DB file, plus its "-wal"/"-shm" WAL-mode siblings, to
	// 0600 on every Open (idempotent no-op once already 0600) rather than
	// only warning, since Open is the single choke point shared by every
	// caller (the hook, cmd/server, `wbt mcp`, and any future one) — no call
	// site can forget it. Also re-tightens pre-existing permissive files
	// (e.g. the three stray 0644 DBs already on disk, and — critically — the
	// -wal/-shm siblings alongside them, which do NOT get fixed retroactively
	// just because the main file does; a re-opened pre-fix DB's leftover
	// -wal/-shm keep whatever permissive mode they were created with). A
	// resolved file-backed DSN that still fails to chmod (or resolves through
	// a symlink — see chmodOwnerOnly) is now a hard error rather than a warn:
	// this process is the file's own creator, so a failure here means
	// something is already wrong (wrong uid, read-only fs, symlink
	// tampering) worth refusing to start over rather than silently serving a
	// DB confirmed group/world-readable.
	// ":memory:" (and a "file:"-scheme DSN with a "mode=memory" query param)
	// has no backing file to chmod — sqliteFilePath reports that via ok=false.
	if err := secureDBFile(dsn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite restrict file permissions %q: %w", dsn, err)
	}
	// Foreign keys are off by default in SQLite. We keep this PRAGMA ON as
	// defence-in-depth in case migration 000026.down.sql is rolled back at
	// runtime and historical FK declarations resurface; the app itself no
	// longer relies on FK behaviour (red line #9 — referential integrity in
	// code, not in the DB; see sql/queries/gtd.sql DeleteTask comment and
	// internal/gtd/store.go DeleteTask).
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite enable FK: %w", err)
	}
	// WAL mode lets readers run concurrently with the single writer.
	// In-memory databases silently stay in "memory" mode (no-op, no error).
	if _, err := conn.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite WAL mode: %w", err)
	}
	// busy_timeout: wait up to 5 s when another OS-level writer holds the lock.
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=5000`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite busy timeout: %w", err)
	}
	if err := runMigrations(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite run migrations: %w", err)
	}
	// Rebuilds the FTS5 inverted index unconditionally from knowledge_items; at personal scale completes in <1 ms.
	if _, err := conn.ExecContext(ctx, `INSERT INTO knowledge_items_fts(knowledge_items_fts) VALUES('rebuild')`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite fts5 rebuild: %w", err)
	}
	return &DB{conn: conn, workspaceID: workspaceID}, nil
}

// sqliteFilePath resolves dsn to the real filesystem path SQLite writes to,
// or ("", false) when dsn has no backing file at all. Two forms report no
// backing file: the bare ":memory:" DSN, and a "file:"-scheme DSN whose
// "mode=memory" query parameter selects SQLite's in-memory VFS.
//
// modernc.org/sqlite (internal conn.go newConn, v1.50.0) opens every DSN
// with SQLITE_OPEN_URI, so "file:" is a first-class accepted scheme — Open's
// own doc comment lists "file:wbt.db" as an example, and
// outcome_test.go / gtd_test.go already pass "file:"+absPath through Open.
// Without resolving the scheme + query string first, os.Chmod("file:/x/y.db")
// fails with ENOENT and the whole permission-tightening pass silently
// degrades to a no-op — the exact bypass this function exists to close.
//
// The "mode=memory" check is deliberately gated on the dsn actually carrying
// the "file:" prefix: per the driver's own newConn, the query string is only
// forwarded to SQLite's URI parser when the dsn starts with "file:" —
// otherwise the driver strips the query entirely before opening, so
// "somefile.db?mode=memory" (no "file:" prefix) silently opens a REAL file
// at "somefile.db" with the query discarded. Treating that case as in-memory
// would skip chmod on a real, disk-backed file.
func sqliteFilePath(dsn string) (string, bool) {
	if dsn == ":memory:" {
		return "", false
	}

	isFileURI := strings.HasPrefix(dsn, "file:")
	path, query, hasQuery := strings.Cut(dsn, "?")
	path = strings.TrimPrefix(path, "file:")
	// "file:///abs/path" (empty authority) and "file://localhost/abs/path"
	// both strip down to a plain absolute path; "file:/abs/path" (single
	// slash — the form this codebase's Open doc comment and existing tests
	// use) needs no further stripping.
	if rest, ok := strings.CutPrefix(path, "//"); ok {
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			path = rest[i:]
		} else {
			path = rest
		}
	}
	if path == "" || path == ":memory:" {
		return "", false
	}
	if isFileURI && hasQuery {
		if q, err := url.ParseQuery(query); err == nil && q.Get("mode") == "memory" {
			return "", false
		}
	}
	return path, true
}

// preCreateSecureFile creates path at 0600 if it doesn't already exist,
// closing the TOCTOU window between file creation and the chmod pass in
// secureDBFile — the mode is applied atomically by the OS inside the
// open(2)/CreateFile syscall itself, so there is no window in which the file
// exists at a more permissive mode. A pre-existing file (EEXIST) is left
// untouched; secureDBFile re-tightens it further down in Open.
//
// O_EXCL also means a symlinked path fails here with EEXIST rather than
// silently creating a file at the symlink's target: POSIX guarantees
// O_CREAT|O_EXCL on a path whose last component is a symlink always reports
// EEXIST without following it. secureDBFile's symlink guard (chmodOwnerOnly)
// is what actually refuses to operate on a symlinked dsn.
func preCreateSecureFile(path string) error {
	//nolint:gosec // G304: path comes from sqliteFilePath(dsn), i.e. SQLITE_PATH env / caller-supplied
	// DSN (config, not an HTTP request field), matching the same class of path already accepted
	// elsewhere in this codebase (e.g. internal/cli/config.go, internal/lifecycle/lock.go).
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil // pre-existing file: secureDBFile re-tightens it below
		}
		return fmt.Errorf("open %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q: %w", path, err)
	}
	return nil
}

// secureDBFile restricts dsn's backing file, plus its "-wal"/"-shm"
// WAL-mode siblings, to 0600. Every existing path among the three is
// chmod'd; a sibling that doesn't exist yet (a brand-new DB hasn't gone
// through PRAGMA journal_mode=WAL yet at the point Open calls this) is
// silently skipped — WAL mode then creates it inheriting the main file's
// already-tightened mode, so no separate re-check is needed for that case.
func secureDBFile(dsn string) error {
	path, ok := sqliteFilePath(dsn)
	if !ok {
		return nil // in-memory DSN: nothing to chmod
	}
	if err := chmodOwnerOnly(path, false); err != nil {
		return fmt.Errorf("%q: %w", path, err)
	}
	for _, sibling := range []string{path + "-wal", path + "-shm"} {
		if err := chmodOwnerOnly(sibling, true); err != nil {
			return fmt.Errorf("%q: %w", sibling, err)
		}
	}
	return nil
}

// chmodOwnerOnly restricts path to 0600 after an os.Lstat symlink check.
// This deliberately does NOT follow symlinks: an untrusted repo's .env
// consumed by `wbt mcp`'s godotenv.Load (internal/cli/mcp.go) can steer
// SQLITE_PATH at a symlink, and this function must not let that redirect a
// chmod onto an arbitrary file outside this DB's own lifecycle (a confused-
// deputy primitive to silently narrow permissions on a file wbt neither
// created nor owns the lifecycle of).
//
// skipMissing tolerates a not-exists stat error — used for the -wal/-shm
// siblings, which legitimately don't exist yet on a brand-new DB. The main
// file always passes skipMissing=false: Open only reaches this call after a
// successful PingContext, so the main file existing is not optional.
func chmodOwnerOnly(path string, skipMissing bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		if skipMissing && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("refusing to chmod a symlink")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	return nil
}

// Close releases the underlying connection.
func (d *DB) Close() error {
	if d == nil || d.conn == nil {
		return nil
	}
	return d.conn.Close() //nolint:wrapcheck // pass-through
}

// workspaceArg returns either the configured workspace UUID string or an
// empty interface representing SQL NULL — used as the `?` arg backing
// `(?1 IS NULL OR workspace_id = ?1)` predicates.
func (d *DB) workspaceArg() any {
	if d.workspaceID == "" {
		return nil
	}
	return d.workspaceID
}

// BeginTx starts a new database transaction scoped to ctx. The caller is
// responsible for calling Commit or Rollback on the returned *sql.Tx.
// Exported so multi-store cross-domain operations (e.g. the confirm_proposal
// accept path) can wrap several store writes in one atomic SQLite transaction.
func (d *DB) BeginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite BeginTx: %w", err)
	}
	return tx, nil
}

// ExecContext executes a query on the underlying connection. Exported so
// integration tests in sibling packages can insert fixture rows (e.g. parent
// tasks / sessions for cascade-cleanup tests) without depending on production
// write paths.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) error {
	_, err := d.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite ExecContext: %w", err)
	}
	return nil
}

// QueryRowContext is a thin wrapper exposed for sibling-package tests to
// assert post-condition state (e.g. that a referential cleanup performed by
// a service-layer DeleteTask actually NULL'd a column / removed a row).
// Production code paths inside the package use s.db.conn directly.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.conn.QueryRowContext(ctx, query, args...)
}

// SqlConn returns the underlying *sql.DB. Callers should prefer the typed store
// methods; this accessor exists for domain stores (like completioncandidate)
// that need sql.Rows-based list queries not covered by ExecContext/QueryRowContext.
// The *sql.DB is the same connection pool shared by all stores — no extra connections
// are opened. SQLite max-conns is 1 (set in Open), so concurrent writers are
// serialised automatically.
func (d *DB) SqlConn() *sql.DB {
	return d.conn
}

// WorkspaceID returns the configured workspace UUID string, or "" when operating
// in legacy unscoped mode.
func (d *DB) WorkspaceID() string {
	return d.workspaceID
}
