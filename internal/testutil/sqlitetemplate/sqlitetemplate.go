// Package sqlitetemplate provides a migrate-once SQLite template shared
// across test packages. [F0925-05]
//
// Running golang-migrate for every test that needs a real, fully-migrated
// SQLite database dominates wall-clock time once a package has more than a
// handful of such tests (internal/mcp measured this at [F0925-04]:
// modernc.org/libc's global allocMu serializes migration runs even across
// otherwise-parallel tests). Path instead builds one template file per
// (test binary, key) pair via the caller-supplied Migrator, then hands
// every caller its own independent copy under t.TempDir().
//
// This package intentionally does not import internal/storage or
// internal/storage/sqlite: internal/storage/sqlite has test files declaring
// "package sqlite" that need to use this package as a real migrator, and
// importing either of those packages from here would create an import
// cycle. Callers inject the migration logic instead (see Migrator).
package sqlitetemplate

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite" for SchemaSnapshot/SchemaVersion's raw sql.Open
)

// Migrator builds a fully-migrated SQLite database at path, closing every
// handle it opened before returning. [F0925-05]
type Migrator func(ctx context.Context, path string) error

// template tracks one key's migrate-once state: once guards a single
// Migrator run, and path/err record that run's outcome for every caller
// (including callers that arrive after the run has already finished) to
// read once once.Do returns.
type template struct {
	once sync.Once
	path string
	err  error
}

// registry is the process-wide (per test binary) set of templates, keyed by
// the caller-chosen key. Guarded by registryMu since templateFor may run
// concurrently with other callers registering different keys, independently
// of any individual template's own sync.Once.
var (
	registryMu sync.Mutex
	registry   = map[string]*template{}
)

// Path returns the path to a fresh, independent copy of the migrate-once
// template registered under key, in a new file named name under
// t.TempDir(). Any "-wal"/"-shm" sibling that exists alongside the
// template's own file is copied alongside the copy too.
//
// The first call to Path for a given key (across the whole test binary,
// regardless of which package or goroutine makes it) runs migrate exactly
// once to build the template; every later call for that key — including
// concurrent callers racing to be first — reuses that same template and
// pays no further migration cost.
//
// key must map to exactly one Migrator for the process's lifetime: Migrator
// values cannot be compared, so if two callers pass the same key with
// different migrators, whichever call reaches that key's sync.Once first
// silently decides the template's schema for every caller of that key.
//
// If migrate fails, every caller of that key — the one that triggered the
// run and every later one, concurrent or not — fails via t.Fatalf with the
// same cached error, by design: silently falling back to per-caller
// migration on error would trade a loud failure for merely-slow tests,
// defeating the point of this helper.
func Path(t testing.TB, key string, migrate Migrator, name string) string {
	t.Helper()
	tmpl := templateFor(key)
	tmpl.once.Do(func() {
		dir, err := os.MkdirTemp("", "wbt-sqlite-template-")
		if err != nil {
			tmpl.err = fmt.Errorf("create sqlite template dir for key %q: %w", key, err)
			return
		}
		path := filepath.Join(dir, "template.db")
		if err := migrate(context.Background(), path); err != nil {
			tmpl.err = fmt.Errorf("migrate sqlite template for key %q: %w", key, err)
			return
		}
		tmpl.path = path
	})
	if tmpl.err != nil {
		t.Fatalf("sqlite template %q: %v", key, tmpl.err)
	}
	return copyTemplate(t, tmpl.path, name)
}

// templateFor returns the *template for key, creating and registering an
// empty one on first use. The returned *template's own sync.Once (not
// registryMu) is what serializes concurrent Migrator runs for that key;
// registryMu only protects the registry map itself.
func templateFor(key string) *template {
	registryMu.Lock()
	defer registryMu.Unlock()
	tmpl, ok := registry[key]
	if !ok {
		tmpl = &template{}
		registry[key] = tmpl
	}
	return tmpl
}

// copyTemplate copies templatePath into a new file named name under
// t.TempDir(), plus any "-wal"/"-shm" WAL-mode siblings that exist
// alongside templatePath (a Migrator's Close() usually checkpoints the WAL
// into the main file, but this doesn't rely on that).
func copyTemplate(t testing.TB, templatePath, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	copyFile(t, templatePath, dst)
	for _, suffix := range []string{"-wal", "-shm"} {
		src := templatePath + suffix
		if _, err := os.Stat(src); err != nil {
			continue // sibling absent (e.g. WAL was checkpointed on Close) — not an error
		}
		copyFile(t, src, dst+suffix)
	}
	return dst
}

// copyFile copies src to dst, failing the test on any error.
func copyFile(t testing.TB, src, dst string) {
	t.Helper()
	in, err := os.Open(src) //nolint:gosec // G304: src is the sync.Once-built template path or that path plus a fixed WAL/SHM suffix
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // G304: dst is under t.TempDir(), not user input
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatalf("copy %s -> %s: %v", src, dst, err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close %s: %v", dst, err)
	}
}

// SchemaSnapshot renders `SELECT type, name, sql FROM sqlite_master ORDER
// BY type, name` for the database at path as a comparable string. It opens
// path with a bare database/sql connection rather than the production
// wbtsqlite.Open helper: that helper runs migrations on open, which would
// silently re-migrate (and thus mask) a broken or empty copy — defeating
// the point of a template-vs-fresh-migration parity check. [F0925-05]
func SchemaSnapshot(t testing.TB, path string) string {
	t.Helper()
	conn := openRaw(t, path)
	defer func() { _ = conn.Close() }()
	rows, err := conn.QueryContext(context.Background(), `SELECT type, name, sql FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatalf("query sqlite_master in %s: %v", path, err)
	}
	defer func() { _ = rows.Close() }()
	var sb strings.Builder
	for rows.Next() {
		var typ, name string
		var stmt sql.NullString
		if err := rows.Scan(&typ, &name, &stmt); err != nil {
			t.Fatalf("scan sqlite_master row in %s: %v", path, err)
		}
		fmt.Fprintf(&sb, "%s|%s|%s\n", typ, name, stmt.String)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_master in %s: %v", path, err)
	}
	return sb.String()
}

// SchemaVersion returns golang-migrate's recorded schema version for the
// database at path, opened the same bare way as SchemaSnapshot (see its
// doc comment for why). [F0925-05]
func SchemaVersion(t testing.TB, path string) int64 {
	t.Helper()
	conn := openRaw(t, path)
	defer func() { _ = conn.Close() }()
	var version int64
	if err := conn.QueryRowContext(context.Background(), `SELECT version FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("query schema_migrations in %s: %v", path, err)
	}
	return version
}

// openRaw opens path with the bare "sqlite" database/sql driver (registered
// by this file's blank modernc.org/sqlite import), with none of
// wbtsqlite.Open's migration-on-open or WAL-pragma setup.
func openRaw(t testing.TB, path string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return conn
}
