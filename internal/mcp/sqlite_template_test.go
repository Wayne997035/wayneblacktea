package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/Wayne997035/wayneblacktea/internal/testutil/sqlitetemplate"
)

// sqliteTemplateKey identifies this package's shared migrate-once SQLite
// template in [F0925-05] sqlitetemplate's cross-package template registry.
// [F0925-06]
const sqliteTemplateKey = "internal/mcp"

// newMigratedSQLitePath returns the path to a fresh, independent copy of
// this package's shared, already-migrated SQLite template. See [F0925-05]
// sqlitetemplate.Path for the migrate-once mechanics this now delegates to
// (formerly a package-local sync.Once implementation — see [F0925-04] for
// why the migrate-once design exists at all). name is used only as the
// destination file's basename (for readability when inspecting a failing
// test's temp dir) — it does not need to be globally unique since
// sqlitetemplate.Path's t.TempDir() already is. [F0925-06]
func newMigratedSQLitePath(t *testing.T, name string) string {
	t.Helper()
	return sqlitetemplate.Path(t, sqliteTemplateKey, migrateSQLiteTemplate, name)
}

// NewMigratedSQLitePath is the exported entry point to newMigratedSQLitePath
// for this package's external (mcp_test) test files: go test links them
// into the same test binary as this package's own _test.go files, but an
// unexported identifier still can't cross that package boundary. Both entry
// points share the same sqliteTemplateKey template, so migration still runs
// at most once per test binary regardless of which one callers use.
// [F0925-06]
func NewMigratedSQLitePath(t *testing.T, name string) string {
	t.Helper()
	return newMigratedSQLitePath(t, name)
}

// migrateSQLiteTemplate is this package's sqlitetemplate.Migrator: it builds
// a fully-migrated SQLite file via storage.NewServerStores and closes every
// store handle before returning, matching what every real caller of this
// package's SQLite backend does at startup. [F0925-06]
func migrateSQLiteTemplate(ctx context.Context, path string) error {
	stores, err := storage.NewServerStores(ctx, storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: path,
	})
	if err != nil {
		return fmt.Errorf("migrate sqlite template: %w", err)
	}
	if err := stores.Close(); err != nil {
		return fmt.Errorf("close sqlite template: %w", err)
	}
	return nil
}

// TestMigratedSQLiteTemplate_MatchesFreshMigration is the correctness proof
// for [F0925-04]/[F0925-05]: a copy handed out by newMigratedSQLitePath must
// be schema-identical to a DB that went through storage.NewServerStores on a
// brand new file — otherwise the speedup would be silently trading away
// migration correctness. [F0925-06] moved the schema-inspection helpers
// this relies on into sqlitetemplate.SchemaSnapshot/SchemaVersion; see their
// doc comments for why they bypass wbtsqlite.Open.
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

	copySchema := sqlitetemplate.SchemaSnapshot(t, copyPath)
	freshSchema := sqlitetemplate.SchemaSnapshot(t, freshPath)
	if copySchema != freshSchema {
		t.Errorf("template copy sqlite_master diverges from fresh migration:\n--- copy ---\n%s\n--- fresh ---\n%s",
			copySchema, freshSchema)
	}

	copyVersion := sqlitetemplate.SchemaVersion(t, copyPath)
	freshVersion := sqlitetemplate.SchemaVersion(t, freshPath)
	if copyVersion != freshVersion {
		t.Errorf("template copy schema_migrations version %d != fresh migration version %d", copyVersion, freshVersion)
	}
}
