// Package sqlitetemplate_test is an external test package: a separate test
// binary target from sqlitetemplate itself, so importing
// internal/storage/sqlite here (as TestSchemaHelpers_MatchRealMigration
// does, for a real Migrator) cannot create an import cycle with
// sqlitetemplate's own non-test files, which never import it. [F0925-05]
package sqlitetemplate_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	wbtsqlite "github.com/Wayne997035/wayneblacktea/internal/storage/sqlite"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/sqlitetemplate"
)

// keyCounter makes uniqueKey collision-proof even under `go test -count=N`,
// which reruns every test function N times inside the same process (and
// thus against the same package-level template registry) rather than in
// separate processes.
var keyCounter atomic.Int64

// uniqueKey returns a key that is guaranteed unused by any other Path call
// in this test binary run, so each test exercises its own fresh template
// registry entry regardless of t.Parallel() interleaving or -count reruns.
func uniqueKey(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), keyCounter.Add(1))
}

// TestPath_SameKeyMigratesOnce is behavior ① from the F0925-05 acceptance
// table: the same key hit by many concurrent callers must still run migrate
// exactly once.
func TestPath_SameKeyMigratesOnce(t *testing.T) {
	t.Parallel()
	key := uniqueKey(t)
	var calls atomic.Int32
	migrate := func(_ context.Context, path string) error {
		calls.Add(1)
		return os.WriteFile(path, []byte("template"), 0o600)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	wg.Add(goroutines)
	paths := make([]string, goroutines)
	for i := range goroutines {
		go func(i int) {
			defer wg.Done()
			paths[i] = sqlitetemplate.Path(t, key, migrate, fmt.Sprintf("copy-%d.db", i))
		}(i)
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("migrator called %d times across %d concurrent callers, want exactly 1", got, goroutines)
	}
	for i, p := range paths {
		if p == "" {
			t.Errorf("caller %d: got empty path", i)
		}
	}
}

// TestPath_DistinctKeysIsolated is behavior ② from the F0925-05 acceptance
// table: two different keys build two independent templates, each with its
// own migrator call and its own content.
func TestPath_DistinctKeysIsolated(t *testing.T) {
	t.Parallel()
	keyA := uniqueKey(t) + "-a"
	keyB := uniqueKey(t) + "-b"
	var callsA, callsB atomic.Int32
	migrateA := func(_ context.Context, path string) error {
		callsA.Add(1)
		return os.WriteFile(path, []byte("content-a"), 0o600)
	}
	migrateB := func(_ context.Context, path string) error {
		callsB.Add(1)
		return os.WriteFile(path, []byte("content-b"), 0o600)
	}

	pathA := sqlitetemplate.Path(t, keyA, migrateA, "a.db")
	pathB := sqlitetemplate.Path(t, keyB, migrateB, "b.db")

	if callsA.Load() != 1 || callsB.Load() != 1 {
		t.Fatalf("migrator call counts = (%d, %d), want (1, 1)", callsA.Load(), callsB.Load())
	}
	contentA, err := os.ReadFile(pathA) //nolint:gosec // G304: pathA is sqlitetemplate.Path's own t.TempDir() output
	if err != nil {
		t.Fatalf("read %s: %v", pathA, err)
	}
	contentB, err := os.ReadFile(pathB) //nolint:gosec // G304: pathB is sqlitetemplate.Path's own t.TempDir() output
	if err != nil {
		t.Fatalf("read %s: %v", pathB, err)
	}
	if string(contentA) != "content-a" || string(contentB) != "content-b" {
		t.Fatalf("got content (%q, %q), want (%q, %q)", contentA, contentB, "content-a", "content-b")
	}
}

// TestPath_CopiesAreIndependent is behavior ③ from the F0925-05 acceptance
// table: two copies handed out for the same key are independent files —
// writing to one must not be visible through the other.
func TestPath_CopiesAreIndependent(t *testing.T) {
	t.Parallel()
	key := uniqueKey(t)
	migrate := func(_ context.Context, path string) error {
		return os.WriteFile(path, []byte("template"), 0o600)
	}

	copy1 := sqlitetemplate.Path(t, key, migrate, "copy1.db")
	copy2 := sqlitetemplate.Path(t, key, migrate, "copy2.db")

	if copy1 == copy2 {
		t.Fatalf("both callers got the same path %q", copy1)
	}
	if err := os.WriteFile(copy1, []byte("mutated"), 0o600); err != nil {
		t.Fatalf("write %s: %v", copy1, err)
	}
	content2, err := os.ReadFile(copy2) //nolint:gosec // G304: copy2 is sqlitetemplate.Path's own t.TempDir() output
	if err != nil {
		t.Fatalf("read %s: %v", copy2, err)
	}
	if string(content2) != "template" {
		t.Fatalf("copy2 content = %q after mutating copy1, want unchanged %q", content2, "template")
	}
}

// fatalRecordingTB embeds a real testing.TB (to satisfy testing.TB's
// unexported method, which no external type can implement directly) and
// overrides only Fatalf, so TestPath_MigratorErrorFailsEveryCaller can
// observe that Path called Fatalf without actually failing the outer test.
// It never calls the embedded real TB's Fail/FailNow/Fatalf/Error, so it
// never trips those methods' "must be called from the test's own goroutine"
// restriction — Fatalf here does its own bookkeeping and its own
// runtime.Goexit, matching *testing.T's real stop-this-goroutine semantics
// closely enough for Path's control flow (the `return copyTemplate(...)`
// line right after t.Fatalf must never execute) without touching t itself.
type fatalRecordingTB struct {
	testing.TB
	fatalHit atomic.Bool
}

func (f *fatalRecordingTB) Fatalf(format string, args ...any) {
	f.fatalHit.Store(true)
	runtime.Goexit()
}

// TestPath_MigratorErrorFailsEveryCaller is behavior ④ from the F0925-05
// acceptance table: when migrate fails, every caller of that key — not just
// the one that happened to trigger the run — must Fatalf, including a
// concurrent second caller that only ever sees the already-cached error.
func TestPath_MigratorErrorFailsEveryCaller(t *testing.T) {
	t.Parallel()
	key := uniqueKey(t)
	var migrateCalls atomic.Int32
	failing := func(_ context.Context, _ string) error {
		migrateCalls.Add(1)
		return errors.New("boom")
	}

	const callers = 2
	recorders := make([]*fatalRecordingTB, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := range callers {
		recorders[i] = &fatalRecordingTB{TB: t}
		go func(fake *fatalRecordingTB) {
			defer wg.Done()
			// Path calls fake.Fatalf on error, which runtime.Goexits this
			// goroutine — must run in its own goroutine so the Goexit only
			// unwinds this caller, not the outer parallel test.
			_ = sqlitetemplate.Path(fake, key, failing, "copy.db")
		}(recorders[i])
	}
	wg.Wait()

	for i, r := range recorders {
		if !r.fatalHit.Load() {
			t.Errorf("caller %d: Path did not call Fatalf after migrator error", i)
		}
	}
	if got := migrateCalls.Load(); got != 1 {
		t.Errorf("migrator called %d times, want exactly 1 (the error is cached, not retried per caller)", got)
	}
}

// TestPath_CopiesWALSibling is behavior ⑤ from the F0925-05 acceptance
// table: a "-wal" sibling alongside the template's own file is copied
// alongside the copy too.
func TestPath_CopiesWALSibling(t *testing.T) {
	t.Parallel()
	key := uniqueKey(t)
	migrate := func(_ context.Context, path string) error {
		if err := os.WriteFile(path, []byte("main"), 0o600); err != nil {
			return err
		}
		return os.WriteFile(path+"-wal", []byte("wal"), 0o600)
	}

	copyPath := sqlitetemplate.Path(t, key, migrate, "copy.db")

	walContent, err := os.ReadFile(copyPath + "-wal") //nolint:gosec // G304: copyPath is sqlitetemplate.Path's own t.TempDir() output
	if err != nil {
		t.Fatalf("read %s: %v", copyPath+"-wal", err)
	}
	if string(walContent) != "wal" {
		t.Fatalf("wal sibling content = %q, want %q", walContent, "wal")
	}
	if _, err := os.Stat(copyPath + "-shm"); !os.IsNotExist(err) {
		t.Fatalf("unexpected -shm sibling present (stat err=%v), no -shm was ever written for this key", err)
	}
}

// TestSchemaHelpers_MatchRealMigration exercises SchemaSnapshot and
// SchemaVersion directly against a real golang-migrate run, using
// internal/storage/sqlite's own Open as the Migrator (see the package doc
// comment above for why importing it here is safe). Not one of the five
// F0925-05 acceptance-table behaviors — internal/mcp's
// TestMigratedSQLiteTemplate_MatchesFreshMigration is the acceptance-level
// proof for these two functions — but every new exported function in this
// package should have direct coverage of its own too.
func TestSchemaHelpers_MatchRealMigration(t *testing.T) {
	t.Parallel()
	key := uniqueKey(t)
	migrate := func(ctx context.Context, path string) error {
		db, err := wbtsqlite.Open(ctx, path, "")
		if err != nil {
			return err
		}
		return db.Close()
	}

	copyPath := sqlitetemplate.Path(t, key, migrate, "real.db")

	snapshot := sqlitetemplate.SchemaSnapshot(t, copyPath)
	if snapshot == "" {
		t.Fatal("SchemaSnapshot returned an empty snapshot for a real migrated database")
	}
	if !strings.Contains(snapshot, "table") {
		t.Errorf("snapshot does not mention any table: %q", snapshot)
	}
	version := sqlitetemplate.SchemaVersion(t, copyPath)
	if version <= 0 {
		t.Errorf("SchemaVersion = %d, want > 0 after a real migration run", version)
	}
}
