package mcp

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

	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// [F0925-04] internal/mcp tests that need a real SQLite-backed store used to
// each pay for a full golang-migrate run via storage.NewServerStores /
// wbtsqlite.Open. modernc.org/libc's global allocMu (modernc.org/libc's
// mem.go) serializes every one of those migration runs even across
// otherwise-parallel tests, and migration dominated this package's
// wall-clock time as a result. newMigratedSQLitePath instead migrates ONE
// template file per test binary (guarded by sqliteTemplateOnce) and hands
// each caller a byte-for-byte copy under its own t.TempDir(), so the
// migration cost is paid once per binary instead of once per test.
var (
	sqliteTemplateOnce sync.Once
	sqliteTemplatePath string
	sqliteTemplateErr  error
)

// newMigratedSQLitePath returns the path to a fresh copy of the shared,
// already-migrated SQLite template, isolated under t.TempDir() so each
// caller keeps its own independent file. name is used only as the
// destination file's basename (for readability when inspecting a failing
// test's temp dir) — it does not need to be globally unique since
// t.TempDir() already is.
//
// Template creation failure is fatal for every caller via t.Fatalf, by
// design: silently falling back to per-test migration on error would trade
// a loud failure for merely-slow tests, defeating the point of this helper.
//
// The template's own directory is created with os.MkdirTemp (not
// t.TempDir()) and is deliberately never removed by this helper: it holds a
// few hundred KB and one such directory is created per test binary run, so
// it is left for the OS's normal temp-dir janitor rather than test cleanup.
func newMigratedSQLitePath(t *testing.T, name string) string {
	t.Helper()
	sqliteTemplateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wbt-mcp-sqlite-template-")
		if err != nil {
			sqliteTemplateErr = fmt.Errorf("create sqlite template dir: %w", err)
			return
		}
		path := filepath.Join(dir, "template.db")
		stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
			Backend:    storage.BackendSQLite,
			SQLitePath: path,
		})
		if err != nil {
			sqliteTemplateErr = fmt.Errorf("migrate sqlite template: %w", err)
			return
		}
		if err := stores.Close(); err != nil {
			sqliteTemplateErr = fmt.Errorf("close sqlite template: %w", err)
			return
		}
		sqliteTemplatePath = path
	})
	if sqliteTemplateErr != nil {
		t.Fatalf("sqlite template: %v", sqliteTemplateErr)
	}
	return copySQLiteTemplate(t, sqliteTemplatePath, name)
}

// NewMigratedSQLitePath is the exported entry point to newMigratedSQLitePath
// for this package's external (mcp_test) test files: go test links them
// into the same test binary as this package's own _test.go files, but an
// unexported identifier still can't cross that package boundary. Both entry
// points share the same sqliteTemplateOnce-built template, so the migration
// itself still runs at most once per test binary regardless of which one
// callers use.
func NewMigratedSQLitePath(t *testing.T, name string) string {
	t.Helper()
	return newMigratedSQLitePath(t, name)
}

// copySQLiteTemplate copies templatePath into a new file named name under
// t.TempDir(), plus any "-wal"/"-shm" WAL-mode siblings that exist alongside
// templatePath (Close() usually checkpoints the WAL into the main file, but
// this doesn't rely on that — see [F0925-04] design decision 3).
func copySQLiteTemplate(t *testing.T, templatePath, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	copySQLiteFile(t, templatePath, dst)
	for _, suffix := range []string{"-wal", "-shm"} {
		src := templatePath + suffix
		if _, err := os.Stat(src); err != nil {
			continue // sibling absent (e.g. WAL was checkpointed on Close) — not an error
		}
		copySQLiteFile(t, src, dst+suffix)
	}
	return dst
}

// copySQLiteFile copies src to dst, failing the test on any error.
func copySQLiteFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src) //nolint:gosec // G304: src is the sqliteTemplateOnce-built template path or that path plus a fixed suffix
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

// TestMigratedSQLiteTemplate_MatchesFreshMigration is the correctness proof
// for [F0925-04]: a copy handed out by newMigratedSQLitePath must be
// schema-identical to a DB that went through storage.NewServerStores on a
// brand new file — otherwise the speedup would be silently trading away
// migration correctness.
//
// Schema inspection deliberately opens each file with a bare database/sql
// connection instead of wbtsqlite.Open: wbtsqlite.Open always runs
// golang-migrate's m.Up() on open, which would silently re-migrate (and
// thus mask) a broken or empty copy — defeating the point of this test.
func TestMigratedSQLiteTemplate_MatchesFreshMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	copyPath := newMigratedSQLitePath(t, "template-parity-copy.db")

	freshPath := filepath.Join(t.TempDir(), "template-parity-fresh.db")
	freshStores, err := storage.NewServerStores(ctx, storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: freshPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores fresh migration: %v", err)
	}
	// Close before raw inspection so the WAL is checkpointed into the main
	// file (same reasoning as [F0925-04] design decision 3).
	if err := freshStores.Close(); err != nil {
		t.Fatalf("close fresh migration stores: %v", err)
	}

	copySchema := sqliteMasterSnapshot(t, copyPath)
	freshSchema := sqliteMasterSnapshot(t, freshPath)
	if copySchema != freshSchema {
		t.Errorf("template copy sqlite_master diverges from fresh migration:\n--- copy ---\n%s\n--- fresh ---\n%s",
			copySchema, freshSchema)
	}

	copyVersion := schemaMigrationsVersion(t, copyPath)
	freshVersion := schemaMigrationsVersion(t, freshPath)
	if copyVersion != freshVersion {
		t.Errorf("template copy schema_migrations version %d != fresh migration version %d", copyVersion, freshVersion)
	}
}

// sqliteMasterSnapshot renders `SELECT type, name, sql FROM sqlite_master
// ORDER BY type, name` as a comparable string, reading path directly (see
// TestMigratedSQLiteTemplate_MatchesFreshMigration's doc comment for why
// this bypasses wbtsqlite.Open).
func sqliteMasterSnapshot(t *testing.T, path string) string {
	t.Helper()
	conn := openRawSQLite(t, path)
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

// schemaMigrationsVersion returns golang-migrate's recorded schema version
// for the file at path, reading it directly (see
// TestMigratedSQLiteTemplate_MatchesFreshMigration's doc comment for why
// this bypasses wbtsqlite.Open).
func schemaMigrationsVersion(t *testing.T, path string) int64 {
	t.Helper()
	conn := openRawSQLite(t, path)
	defer func() { _ = conn.Close() }()
	var version int64
	if err := conn.QueryRowContext(context.Background(), `SELECT version FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("query schema_migrations in %s: %v", path, err)
	}
	return version
}

// openRawSQLite opens path with the bare "sqlite" database/sql driver
// (registered by modernc.org/sqlite's init(), pulled in transitively via
// the storage package import above), with none of wbtsqlite.Open's
// migration-on-open or WAL-pragma setup.
func openRawSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	return conn
}
