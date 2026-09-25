package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/testutil/sqlitetemplate"
	"github.com/google/uuid"
)

// templateKey identifies this package's shared migrate-once SQLite template
// in sqlitetemplate's cross-package template registry. Must map to exactly
// one Migrator (templateMigrator below) for the process's lifetime. [F0925-09]
const templateKey = "internal/storage/sqlite"

// templateMigrator builds a fully-migrated SQLite database at path by
// running the real production Open/Close path once -- the same thing every
// real caller of this package does at startup -- then closing it so the
// template file is a plain, checkpointed copy source. [F0925-09]
func templateMigrator(ctx context.Context, path string) error {
	d, err := Open(ctx, path, "")
	if err != nil {
		return fmt.Errorf("migrate sqlite template: %w", err)
	}
	if err := d.Close(); err != nil {
		return fmt.Errorf("close sqlite template: %w", err)
	}
	return nil
}

// templateCopySeq gives every template materialization its own destination
// filename under a shared t.TempDir(), so concurrent/parallel callers whose
// t.TempDir() happens to coincide (a parent test and its own subtests) never
// collide on the same basename. [F0925-09]
var templateCopySeq atomic.Int64

// templateCopyMu guards templateCopyLog, the test-only observation point
// TestOpenTemplated_* below read via templateCopyCount. Production Open
// never touches either. [F0925-09]
var (
	templateCopyMu  sync.Mutex
	templateCopyLog []string
)

// recordTemplateCopy appends dst to templateCopyLog every time openTemplated
// actually copies a template file to dst. [F0925-09]
func recordTemplateCopy(dst string) {
	templateCopyMu.Lock()
	defer templateCopyMu.Unlock()
	templateCopyLog = append(templateCopyLog, dst)
}

// templateCopyCount returns how many recorded copies have a destination
// under prefix ("" matches everything, for a total count). Callers that run
// concurrently with other openTemplated callers should pass their own
// t.TempDir() as prefix to isolate their own calls' effect. [F0925-09]
func templateCopyCount(prefix string) int {
	templateCopyMu.Lock()
	defer templateCopyMu.Unlock()
	n := 0
	for _, dst := range templateCopyLog {
		if strings.HasPrefix(dst, prefix) {
			n++
		}
	}
	return n
}

// openTemplated is the semantics-preserving replacement for calling Open
// directly from this package's own (white-box "package sqlite") tests.
// Running golang-migrate for every test that needs a real, migrated SQLite
// database dominates wall-clock time once a package has this many such
// tests (see internal/testutil/sqlitetemplate's package doc for the
// underlying cause). Every DSN shape below is dispatched purely from the
// DSN string at call time -- never from guessing caller intent -- so
// callers do not need to be individually classified:
//
//   - exactly ":memory:" -> every call gets its own independent template
//     copy under t.TempDir(), then Opens that copy. Opening ":memory:"
//     twice already produces two fully independent databases
//     (max-open-conns=1 per *DB), so an independent file copy per call
//     preserves that.
//   - a bare path, or "file:"+path, with no "?" query component -> the
//     template is copied to that exact path only if nothing exists there
//     yet (including a pre-existing 0-byte file, which is left alone), then
//     the ORIGINAL dsn is opened for real. Two calls against the same path
//     therefore share one physical database (the second call's copy is
//     skipped because the first call's file already exists) -- exactly like
//     two direct Opens of the same path today.
//   - anything else (a DSN with a "?" query component, e.g. "cache=shared",
//     "mode=memory") -> passed straight to the real Open, untouched. These
//     DSNs encode behaviour (process-wide shared in-memory databases, fixed
//     shared names) this helper must not disturb.
//
// Return value and error semantics match Open exactly -- callers' existing
// "if err != nil" handling needs no change. [F0925-09]
func openTemplated(t testing.TB, ctx context.Context, dsn, workspaceID string) (*DB, error) {
	t.Helper()
	openDSN := dsn
	if !strings.ContainsRune(dsn, '?') {
		if dsn == memoryDSN {
			// Every ":memory:" call gets its own independent copy -- Open
			// that copy, not the literal ":memory:" DSN.
			openDSN = materializeMemoryTemplate(t)
		} else {
			// Bare path, or "file:"+path, no query: copy in place only if
			// nothing is there yet, then open the ORIGINAL dsn so a second
			// call against the same path shares the first call's database.
			target := strings.TrimPrefix(dsn, "file:")
			materializeFileTemplateIfAbsent(t, target)
		}
	}
	return Open(ctx, openDSN, workspaceID)
}

// OpenTemplated is openTemplated's exported twin for this test binary's
// external "sqlite_test" package files, which cannot call the unexported
// name across the package boundary (same pattern as internal/mcp's
// newMigratedSQLitePath/NewMigratedSQLitePath). Both entry points dispatch
// through the same templateKey template. [F0925-09]
func OpenTemplated(t testing.TB, ctx context.Context, dsn, workspaceID string) (*DB, error) {
	t.Helper()
	return openTemplated(t, ctx, dsn, workspaceID)
}

// materializeMemoryTemplate returns the path to a template copy nobody else
// holds: templateCopySeq guarantees a distinct destination filename per
// call, so two ":memory:" opens within the very same test (sharing one
// t.TempDir()) still get two independent copies. [F0925-09]
func materializeMemoryTemplate(t testing.TB) string {
	t.Helper()
	name := fmt.Sprintf("template-%d.db", templateCopySeq.Add(1))
	path := sqlitetemplate.Path(t, templateKey, templateMigrator, name)
	recordTemplateCopy(path)
	return path
}

// materializeFileTemplateIfAbsent copies the template to target only when
// nothing exists at target yet (a pre-existing file, including a 0-byte
// one, is left untouched). This is what makes two openTemplated calls
// against the same target share one physical database: the second call's
// os.Stat finds the first call's file already there and skips the copy.
// sqlitetemplate.Path has no way to target an arbitrary destination (it
// always picks a fresh name under its own t.TempDir()), so this copies that
// output a second time to target -- an accepted extra I/O cost for not
// having to touch sqlitetemplate.go's own copy logic. [F0925-09]
func materializeFileTemplateIfAbsent(t testing.TB, target string) {
	t.Helper()
	if _, err := os.Stat(target); err == nil {
		return // already exists -- do not overwrite
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat template target %s: %v", target, err)
	}
	name := fmt.Sprintf("template-%d.db", templateCopySeq.Add(1))
	src := sqlitetemplate.Path(t, templateKey, templateMigrator, name)
	copyTemplateFile(t, src, target)
	recordTemplateCopy(target)
}

// copyTemplateFile copies src to dst, plus any "-wal"/"-shm" siblings that
// exist alongside src, mirroring sqlitetemplate's own sibling-copy defence
// in case a Migrator's Close() ever leaves an un-checkpointed WAL behind.
// [F0925-09]
func copyTemplateFile(t testing.TB, src, dst string) {
	t.Helper()
	copyOneFile(t, src, dst)
	for _, suffix := range []string{"-wal", "-shm"} {
		s := src + suffix
		if _, err := os.Stat(s); err != nil {
			continue // sibling absent -- not an error
		}
		copyOneFile(t, s, dst+suffix)
	}
}

func copyOneFile(t testing.TB, src, dst string) {
	t.Helper()
	in, err := os.Open(src) //nolint:gosec // G304: src is this file's own sqlitetemplate.Path output, not external input
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // G304: dst is a test-supplied DSN path under t.TempDir(), never external input
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

// TestOpenTemplated_MemoryCallsAreIndependent verifies the ":memory:" branch
// of the DSN dispatch table: two ":memory:" calls must produce two databases
// that share nothing, exactly like two real ":memory:" Opens would.
// [F0925-09]
func TestOpenTemplated_MemoryCallsAreIndependent(t *testing.T) {
	// Not parallel: F0925-10 -- asserts on templateCopyLog/templateCopySeq,
	// package-level state every other test's openTemplated call also writes
	// to; running alongside other parallel tests elsewhere would make the
	// count flaky.
	ctx := context.Background()
	d1, err := openTemplated(t, ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("openTemplated d1: %v", err)
	}
	t.Cleanup(func() { _ = d1.Close() })
	d2, err := openTemplated(t, ctx, ":memory:", "")
	if err != nil {
		t.Fatalf("openTemplated d2: %v", err)
	}
	t.Cleanup(func() { _ = d2.Close() })

	if err := d1.ExecContext(ctx, `CREATE TABLE test_marker_independent (id INTEGER)`); err != nil {
		t.Fatalf("create marker table on d1: %v", err)
	}

	var count int
	if err := d2.QueryRowContext(
		ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='test_marker_independent'`,
	).Scan(&count); err != nil {
		t.Fatalf("query d2 for d1's marker table: %v", err)
	}
	if count != 0 {
		t.Errorf("two independent \":memory:\" opens must not share state, but d2 sees d1's marker table")
	}
}

// TestOpenTemplated_SamePathSharesOneDB verifies the file-path branch's
// sharing half: two openTemplated calls against the same on-disk path (two
// different workspaceIDs, matching openFileStore's real usage) must land on
// one physical database, readable via raw SQL that bypasses any
// workspace_id filter. [F0925-09]
func TestOpenTemplated_SamePathSharesOneDB(t *testing.T) {
	// Not parallel: F0925-10 -- asserts on templateCopyLog/templateCopySeq,
	// package-level state every other test's openTemplated call also writes
	// to; running alongside other parallel tests elsewhere would make the
	// count flaky.
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "shared-template.db")

	d1, err := openTemplated(t, ctx, path, "ws-a")
	if err != nil {
		t.Fatalf("openTemplated d1: %v", err)
	}
	t.Cleanup(func() { _ = d1.Close() })
	if err := d1.ExecContext(ctx, `CREATE TABLE test_marker_shared (id INTEGER)`); err != nil {
		t.Fatalf("create marker table on d1: %v", err)
	}
	if err := d1.ExecContext(ctx, `INSERT INTO test_marker_shared (id) VALUES (1)`); err != nil {
		t.Fatalf("insert marker row on d1: %v", err)
	}

	d2, err := openTemplated(t, ctx, path, "ws-b")
	if err != nil {
		t.Fatalf("openTemplated d2: %v", err)
	}
	t.Cleanup(func() { _ = d2.Close() })

	var count int
	if err := d2.QueryRowContext(ctx, `SELECT count(*) FROM test_marker_shared`).Scan(&count); err != nil {
		t.Fatalf("query d2 for d1's marker row: %v", err)
	}
	if count != 1 {
		t.Errorf("two opens of the same path must share one physical database, want row count 1, got %d", count)
	}
}

// TestOpenTemplated_ExistingFileIsNotOverwritten verifies that a file
// already present at the target path -- pre-created by a bare connection so
// it never goes through Open/migrations first -- keeps its own content:
// openTemplated must not clobber it with the template copy. [F0925-09]
func TestOpenTemplated_ExistingFileIsNotOverwritten(t *testing.T) {
	// Not parallel: F0925-10 -- asserts on templateCopyLog/templateCopySeq,
	// package-level state every other test's openTemplated call also writes
	// to; running alongside other parallel tests elsewhere would make the
	// count flaky.
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "preexisting.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open %s: %v", path, err)
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE test_marker_preexisting (id INTEGER)`); err != nil {
		t.Fatalf("create marker table via bare connection: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close bare connection: %v", err)
	}

	d, err := openTemplated(t, ctx, path, "")
	if err != nil {
		t.Fatalf("openTemplated: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })

	var count int
	if err := d.QueryRowContext(
		ctx,
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='test_marker_preexisting'`,
	).Scan(&count); err != nil {
		t.Fatalf("query pre-existing marker table: %v", err)
	}
	if count != 1 {
		t.Errorf("pre-existing marker table was lost -- the target file got overwritten by the template copy")
	}
}

// TestOpenTemplated_FilePrefixPath verifies the "file:"-prefix half of the
// file-path branch: the copy's destination is the path with "file:" stripped
// (asserted via the templateCopyCount observation point), and a later bare-
// path reopen of that same file sees what was written through the
// "file:"-prefixed DSN. [F0925-09]
func TestOpenTemplated_FilePrefixPath(t *testing.T) {
	// Not parallel: F0925-10 -- asserts on templateCopyLog/templateCopySeq,
	// package-level state every other test's openTemplated call also writes
	// to; running alongside other parallel tests elsewhere would make the
	// count flaky.
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "file-prefix.db")
	dsn := "file:" + target

	before := templateCopyCount(target)
	d1, err := openTemplated(t, ctx, dsn, "")
	if err != nil {
		t.Fatalf("openTemplated: %v", err)
	}
	t.Cleanup(func() { _ = d1.Close() })
	after := templateCopyCount(target)
	if after != before+1 {
		t.Errorf("expected exactly 1 copy recorded at the \"file:\"-stripped path %s, delta=%d", target, after-before)
	}

	if err := d1.ExecContext(ctx, `CREATE TABLE test_marker_file_prefix (id INTEGER)`); err != nil {
		t.Fatalf("create marker table on d1: %v", err)
	}
	if err := d1.ExecContext(ctx, `INSERT INTO test_marker_file_prefix (id) VALUES (1)`); err != nil {
		t.Fatalf("insert marker row on d1: %v", err)
	}

	d2, err := openTemplated(t, ctx, target, "") // bare path this time, no "file:" prefix
	if err != nil {
		t.Fatalf("openTemplated (bare path reopen): %v", err)
	}
	t.Cleanup(func() { _ = d2.Close() })

	var count int
	if err := d2.QueryRowContext(ctx, `SELECT count(*) FROM test_marker_file_prefix`).Scan(&count); err != nil {
		t.Fatalf("query d2 for d1's marker row: %v", err)
	}
	if count != 1 {
		t.Errorf("data written via \"file:\"-prefixed DSN must be visible on a bare-path reopen of the same file, got count=%d", count)
	}
}

// TestOpenTemplated_QueryDSNPassesThrough verifies the passthrough branch:
// a DSN with a "?" query component (cache=shared, the same shape several
// real callers in this package use) must reach the real Open completely
// unmodified, with the helper performing no template copy at all.
// [F0925-09]
func TestOpenTemplated_QueryDSNPassesThrough(t *testing.T) {
	// Not parallel: F0925-10 -- asserts on templateCopyLog/templateCopySeq,
	// package-level state every other test's openTemplated call also writes
	// to; running alongside other parallel tests elsewhere would make the
	// count flaky.
	ctx := context.Background()
	dsn := "file:test-query-passthrough-" + uuid.New().String() + "?mode=memory&cache=shared"

	before := templateCopyCount("")
	d, err := openTemplated(t, ctx, dsn, "")
	if err != nil {
		t.Fatalf("openTemplated: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	after := templateCopyCount("")
	if after != before {
		t.Errorf("a query-string DSN must pass through untouched, but the helper recorded %d copy/copies", after-before)
	}
}

// TestSQLiteTemplate_MatchesFreshOpen is the correctness proof for
// openTemplated's whole premise: a template copy handed out by this
// package's templateMigrator must be schema-identical to a database that
// went through a real Open on a brand new path -- otherwise the speedup
// would silently trade away migration correctness. Mirrors internal/mcp's
// TestMigratedSQLiteTemplate_MatchesFreshMigration, whose pass is only
// indirect evidence for this package (different Migrator, different
// production code path). [F0925-09]
func TestSQLiteTemplate_MatchesFreshOpen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	copyPath := sqlitetemplate.Path(t, templateKey, templateMigrator, "template-parity-copy.db")

	freshPath := filepath.Join(t.TempDir(), "template-parity-fresh.db")
	fresh, err := Open(ctx, freshPath, "")
	if err != nil {
		t.Fatalf("Open fresh: %v", err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatalf("close fresh: %v", err)
	}

	copySchema := sqlitetemplate.SchemaSnapshot(t, copyPath)
	freshSchema := sqlitetemplate.SchemaSnapshot(t, freshPath)
	if copySchema != freshSchema {
		t.Errorf("template copy schema diverges from a fresh Open's schema")
	}

	copyVersion := sqlitetemplate.SchemaVersion(t, copyPath)
	freshVersion := sqlitetemplate.SchemaVersion(t, freshPath)
	if copyVersion != freshVersion {
		t.Errorf("template copy schema_migrations version %d != fresh Open's %d", copyVersion, freshVersion)
	}
}
