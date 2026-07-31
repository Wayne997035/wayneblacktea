package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"
)

const memoryDSN = ":memory:"

// DB is the package-internal connection wrapper. It holds the *sql.DB plus
// the optional workspace UUID (echoing the Postgres stores' Init-time
// scoping). Stores share this and add their own typed methods on top.
type DB struct {
	conn *sql.DB
	// modeReference is the owner-only 0600 descriptor prependModeOf pointed
	// SQLite's modeof URI option at (see secureCreationDSN / openSQLiteConnection).
	// database/sql keeps the DSN string for the *sql.DB's entire life and
	// silently re-dials it on every pool reconnect (idle eviction,
	// SetConnMaxLifetime, a cancelled in-flight query, ...), and SQLite
	// resolves modeof on EVERY main-db open, not just first creation. So this
	// descriptor must stay open for as long as conn can still reconnect —
	// closing it right after the first Ping (the pre-fix behaviour) left
	// later reconnects stat()ing a dead, possibly fd-recycled, target and
	// failing with SQLITE_IOERR_FSTAT ("disk I/O error (10)"). Closed only in
	// Close(). nil for :memory: / non-file DSNs (see secureCreationDSNWith).
	modeReference *os.File
	workspaceID   string // empty = legacy unscoped mode
}

// Open creates a new DB by opening dsn (e.g. "file:wbt.db" or ":memory:") and
// bringing its schema up to date via the embedded golang-migrate migration
// set (migrations/sqlite/); see runMigrations for the adoption-vs-replay
// logic. workspaceID may be empty. schema.sql is no longer the runtime
// authority — see its header comment.
func Open(ctx context.Context, dsn, workspaceID string) (*DB, error) {
	conn, mainPath, modeReference, err := openSQLiteConnection(ctx, dsn)
	if err != nil {
		return nil, err
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
	// file-backed DB that still fails to chmod (or resolves through a symlink
	// — see chmodOwnerOnly) is a hard error rather than a warning:
	// this process is the file's own creator, so a failure here means
	// something is already wrong (wrong uid, read-only fs, symlink
	// tampering) worth refusing to start over rather than silently serving a
	// DB confirmed group/world-readable.
	//
	// mainPath comes only from SQLite's PRAGMA database_list. This is the
	// security boundary: no Go-side URI parser gets to guess which path
	// SQLite opened. In-memory databases report an empty path.
	if err := secureDBFile(mainPath); err != nil {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite restrict file permissions %q: %w", mainPath, err)
	}
	// Foreign keys are off by default in SQLite. We keep this PRAGMA ON as
	// defence-in-depth in case migration 000026.down.sql is rolled back at
	// runtime and historical FK declarations resurface; the app itself no
	// longer relies on FK behaviour (red line #9 — referential integrity in
	// code, not in the DB; see sql/queries/gtd.sql DeleteTask comment and
	// internal/gtd/store.go DeleteTask).
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite enable FK: %w", err)
	}
	// WAL mode lets readers run concurrently with the single writer.
	// In-memory databases silently stay in "memory" mode (no-op, no error).
	if _, err := conn.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite WAL mode: %w", err)
	}
	// busy_timeout: wait up to 5 s when another OS-level writer holds the lock.
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=5000`); err != nil {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite busy timeout: %w", err)
	}
	if err := runMigrations(ctx, conn); err != nil {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite run migrations: %w", err)
	}
	// Rebuilds the FTS5 inverted index unconditionally from knowledge_items; at personal scale completes in <1 ms.
	if _, err := conn.ExecContext(ctx, `INSERT INTO knowledge_items_fts(knowledge_items_fts) VALUES('rebuild')`); err != nil {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite fts5 rebuild: %w", err)
	}
	// WAL and migrations can create or replace sidecars after the first pass.
	// Re-read SQLite's authoritative path and harden all extant files before
	// reporting success.
	finalMainPath, err := mainDatabasePath(ctx, conn)
	if err != nil {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite resolve final main database: %w", err)
	}
	if finalMainPath != mainPath {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite main database path changed during Open from %q to %q", mainPath, finalMainPath)
	}
	if err := secureDBFile(finalMainPath); err != nil {
		closeAbandonedOpen(conn, modeReference)
		return nil, fmt.Errorf("sqlite re-restrict file permissions %q: %w", finalMainPath, err)
	}
	return &DB{conn: conn, modeReference: modeReference, workspaceID: workspaceID}, nil
}

// closeAbandonedOpen releases conn and modeReference together on any Open
// error path that runs after openSQLiteConnection has already handed
// ownership of both to Open, but before Open constructs the *DB that would
// otherwise own them. Close errors are deliberately discarded here — Open is
// already returning the actionable error from the failed step, matching the
// pre-existing best-effort discard convention used throughout this function.
func closeAbandonedOpen(conn *sql.DB, modeReference *os.File) {
	_ = conn.Close()
	_ = closeModeReference(modeReference)
}

// openSQLiteConnection opens dsn, establishes the first physical connection,
// and asks SQLite which path its main database actually uses. No caller may
// derive a chmod target from dsn: SQLite's PRAGMA database_list is the sole
// authority, and reports an empty path for in-memory databases.
//
// The returned *os.File (nil for :memory: / non-file DSNs) is the modeof mode
// reference described on DB.modeReference: on success it is handed to the
// caller, NOT closed here, because database/sql retains openDSN for the
// entire life of the resulting *sql.DB and silently re-dials it — modeof and
// all — on every future reconnect. It is closed on every error path below
// since no *DB survives to take ownership of it.
func openSQLiteConnection(ctx context.Context, dsn string) (*sql.DB, string, *os.File, error) {
	openDSN, modeReference, err := secureCreationDSN(dsn)
	if err != nil {
		return nil, "", nil, fmt.Errorf("sqlite secure creation for %q: %w", dsn, err)
	}
	conn, err := sql.Open("sqlite", openDSN)
	if err != nil {
		return nil, "", nil, errors.Join(fmt.Errorf("sqlite open %q: %w", dsn, err), closeModeReference(modeReference))
	}
	// Serialise all writes through a single connection. SQLite has no
	// multi-writer concurrency; a pool > 1 triggers SQLITE_BUSY under concurrent
	// goroutines. Readers coexist via WAL mode set in Open.
	conn.SetMaxOpenConns(1)
	if err := conn.PingContext(ctx); err != nil {
		return nil, "", nil, errors.Join(
			fmt.Errorf("sqlite ping %q: %w", dsn, err),
			closeModeReference(modeReference),
			conn.Close(),
		)
	}
	mainPath, pathErr := mainDatabasePath(ctx, conn)
	symlinkErr := refuseSymlinkDSN(dsn)
	if pathErr != nil || symlinkErr != nil {
		var wrappedPathErr error
		if pathErr != nil {
			wrappedPathErr = fmt.Errorf("sqlite resolve main database %q: %w", dsn, pathErr)
		}
		return nil, "", nil, errors.Join(
			wrappedPathErr,
			symlinkErr,
			closeModeReference(modeReference),
			conn.Close(),
		)
	}
	return conn, mainPath, modeReference, nil
}

// secureCreationDSN ensures a newly created database is owner-only from its
// first observable instant without trying to resolve a file: URI in Go.
//
// For file: URIs, SQLite remains the filename parser. We prepend its native
// modeof URI option, pointing at an owner-only descriptor that stays open
// through PingContext. SQLite copies that mode into the same open(2) call
// that creates its resolved target. modeof is first-match, so prepending also
// prevents a later caller-controlled modeof from weakening the mode.
//
// For non-file DSNs, modernc.org/sqlite v1.50.0 removes bytes from the first
// '?' before passing the filename to SQLite. Pre-creating that exact filename
// preserves the original query semantics: converting it to file: would make
// SQLite newly interpret URI-only options such as mode=rw or cache=shared.
// This narrow driver split is used only for atomic creation; PRAGMA remains
// the sole chmod authority, so future parser drift cannot redirect chmod.
func secureCreationDSN(dsn string) (string, *os.File, error) {
	return secureCreationDSNWith(dsn, newOwnerOnlyModeReference)
}

func secureCreationDSNWith(
	dsn string,
	newModeReference func() (*os.File, string, error),
) (string, *os.File, error) {
	if isDefinitelyMemoryDSN(dsn) {
		return dsn, nil, nil
	}
	if err := refuseSymlinkDSN(dsn); err != nil {
		return "", nil, err
	}
	if strings.HasPrefix(dsn, "file:") {
		modeReference, referencePath, err := newModeReference()
		if err != nil {
			return "", nil, err
		}
		return prependModeOf(dsn, referencePath), modeReference, nil
	}

	filename := dsn
	if queryAt := strings.IndexRune(dsn, '?'); queryAt >= 1 {
		filename = dsn[:queryAt]
	}
	if filename == "" || filename == memoryDSN {
		return dsn, nil, nil
	}
	if err := preCreateSecureFile(filename); err != nil {
		return "", nil, fmt.Errorf("pre-create %q: %w", filename, err)
	}
	return dsn, nil, nil
}

// isDefinitelyMemoryDSN is deliberately conservative: its only security
// effect is avoiding the mode-reference file for DSNs SQLite will not back
// with a file. Any parse ambiguity returns false and takes the secure
// file-capable path. It is never used to choose a chmod target.
func isDefinitelyMemoryDSN(dsn string) bool {
	if !strings.HasPrefix(dsn, "file:") {
		filename, _, _ := strings.Cut(dsn, "?")
		return filename == memoryDSN
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return false
	}
	path := u.Path
	if path == "" {
		path, err = url.PathUnescape(u.Opaque)
		if err != nil {
			return false
		}
	}
	if path == memoryDSN {
		return true
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	modes := query["mode"]
	return len(modes) > 0 && modes[len(modes)-1] == "memory"
}

func prependModeOf(dsn, referencePath string) string {
	withoutFragment, fragment, hasFragment := strings.Cut(dsn, "#")
	queryAt := strings.IndexRune(withoutFragment, '?')
	parameter := "modeof=" + url.QueryEscape(referencePath)
	switch {
	case queryAt < 0:
		withoutFragment += "?" + parameter
	case queryAt == len(withoutFragment)-1:
		withoutFragment += parameter
	default:
		withoutFragment = withoutFragment[:queryAt+1] + parameter + "&" + withoutFragment[queryAt+1:]
	}
	if hasFragment {
		withoutFragment += "#" + fragment
	}
	return withoutFragment
}

// refuseSymlinkDSN rejects a final path component that is already a symlink.
// This parser is deliberately NOT used to choose any chmod target: its only
// effect is fail-closed refusal, and the check is repeated after Ping to
// narrow replacement races. The final permission pass remains exclusively
// grounded in PRAGMA database_list.
func refuseSymlinkDSN(dsn string) error {
	path, err := dsnPathForSymlinkCheck(dsn)
	if err != nil || path == "" {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat DSN path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing SQLite DSN symlink %q", path)
	}
	return nil
}

func dsnPathForSymlinkCheck(dsn string) (string, error) {
	if !strings.HasPrefix(dsn, "file:") {
		if queryAt := strings.IndexRune(dsn, '?'); queryAt >= 1 {
			return dsn[:queryAt], nil
		}
		return dsn, nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse file URI for symlink check: %w", err)
	}
	if u.Path != "" {
		return u.Path, nil
	}
	path, err := url.PathUnescape(u.Opaque)
	if err != nil {
		return "", fmt.Errorf("decode file URI path for symlink check: %w", err)
	}
	return path, nil
}

// newOwnerOnlyModeReference returns an anonymous filesystem object whose
// exact 0600 mode SQLite's modeof option can copy. /dev/fd is available on
// the supported Darwin runtime and Linux CI. Pipes are used where /dev/fd
// exposes their mode as 0600. Darwin exposes pipe descriptors as 0440, so
// the portable fallback is a 0600 temporary file unlinked immediately while
// its descriptor remains open; no named file survives this function.
func newOwnerOnlyModeReference() (*os.File, string, error) {
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		return nil, "", fmt.Errorf("create mode reference pipe: %w", err)
	}
	if err := writeEnd.Close(); err != nil {
		return nil, "", errors.Join(
			fmt.Errorf("close mode reference writer: %w", err),
			readEnd.Close(),
		)
	}
	referencePath := fmt.Sprintf("/dev/fd/%d", readEnd.Fd())
	info, err := os.Stat(referencePath)
	if err == nil && info.Mode().Perm() == 0o600 {
		return readEnd, referencePath, nil
	}
	if err := readEnd.Close(); err != nil {
		return nil, "", fmt.Errorf("close unsuitable pipe mode reference: %w", err)
	}

	reference, err := os.CreateTemp("", ".wbt-sqlite-mode-*")
	if err != nil {
		return nil, "", fmt.Errorf("create fallback mode reference: %w", err)
	}
	name := reference.Name()
	if err := os.Remove(name); err != nil {
		return nil, "", errors.Join(
			fmt.Errorf("unlink fallback mode reference %q: %w", name, err),
			reference.Close(),
		)
	}
	referencePath = fmt.Sprintf("/dev/fd/%d", reference.Fd())
	info, err = os.Stat(referencePath)
	if err != nil {
		return nil, "", errors.Join(
			fmt.Errorf("stat fallback mode reference %q: %w", referencePath, err),
			reference.Close(),
		)
	}
	if info.Mode().Perm() != 0o600 {
		return nil, "", errors.Join(
			fmt.Errorf("fallback mode reference %q has permissions %o, want 0600", referencePath, info.Mode().Perm()),
			reference.Close(),
		)
	}
	return reference, referencePath, nil
}

func closeModeReference(reference *os.File) error {
	if reference == nil {
		return nil
	}
	if err := reference.Close(); err != nil {
		return fmt.Errorf("close mode reference: %w", err)
	}
	return nil
}

func mainDatabasePath(ctx context.Context, conn *sql.DB) (path string, returnErr error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return "", fmt.Errorf("query PRAGMA database_list: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close PRAGMA database_list rows: %w", err))
		}
	}()
	for rows.Next() {
		var sequence int
		var name, reportedPath string
		if err := rows.Scan(&sequence, &name, &reportedPath); err != nil {
			return "", fmt.Errorf("scan PRAGMA database_list: %w", err)
		}
		if name == "main" {
			return reportedPath, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate PRAGMA database_list: %w", err)
	}
	return "", errors.New("PRAGMA database_list did not report the main database")
}

// preCreateSecureFile creates path at 0600 if it doesn't already exist. It
// is used only for modernc's non-file DSN filename rule; file: URI resolution
// stays entirely inside SQLite.
//
// O_EXCL also means a symlinked path fails here with EEXIST rather than
// silently creating a file at the symlink's target: POSIX guarantees
// O_CREAT|O_EXCL on a path whose last component is a symlink always reports
// EEXIST without following it. The pre/post-open DSN checks refuse that
// symlink explicitly, and the authoritative chmod pass independently
// refuses symlinked main/WAL/SHM paths.
func preCreateSecureFile(path string) error {
	//nolint:gosec // G304: path is the modernc driver's non-file DSN filename segment from SQLITE_PATH/config,
	// not an HTTP request field, matching the same class of path already accepted elsewhere in this codebase.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil // pre-existing file: the PRAGMA-authoritative pass tightens it
		}
		return fmt.Errorf("open %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %q: %w", path, err)
	}
	return nil
}

// secureDBFile restricts SQLite's authoritative main path, plus its
// "-wal"/"-shm" siblings, to 0600. An empty main path means SQLite reported
// an in-memory database.
func secureDBFile(path string) error {
	if path == "" {
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
	return chmodOwnerOnlyWith(path, skipMissing, os.Chmod)
}

func chmodOwnerOnlyWith(path string, skipMissing bool, chmod func(string, os.FileMode) error) error {
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
	if err := chmod(path, 0o600); err != nil {
		// Some filesystems/mounts (exFAT, certain SMB/bind mounts) or a file
		// owned by a different uid than this process reject chmod outright
		// (EPERM/ENOTSUP) with no attacker involved at all — a legitimate
		// availability concern the security fix must not introduce. Only
		// tolerate that when the file's ALREADY-OBSERVED mode (from the
		// Lstat above, not re-derived after the failed chmod) has no
		// group/world bits set; a file that IS actually group/world-readable
		// and can't be chmod'd is still a hard error — this does not weaken
		// the fail-closed decision, it only stops it from misfiring on a
		// file that was already safe.
		if info.Mode().Perm()&0o077 == 0 {
			slog.Warn("sqlite: chmod not supported for this file, but it is already owner-only; continuing",
				"path", path, "mode", fmt.Sprintf("%o", info.Mode().Perm()), "err", err)
			return nil
		}
		return fmt.Errorf("chmod: %w", err)
	}
	return nil
}

// Close releases the underlying connection and the modeof mode-reference
// descriptor (see DB.modeReference) kept open for the DSN's entire life.
// Safe on a nil *DB or nil modeReference (closeModeReference is a no-op for
// nil, matching the :memory: / non-file DSN case).
func (d *DB) Close() error {
	if d == nil || d.conn == nil {
		return nil
	}
	return errors.Join(d.conn.Close(), closeModeReference(d.modeReference))
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
