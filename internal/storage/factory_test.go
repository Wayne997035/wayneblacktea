package storage_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// TestNewServerStores_SQLite_HappyPath verifies the SQLite bundle wires every
// backend-agnostic accessor to a non-nil store and every Pg-prefixed
// concrete-store accessor (only meaningful on the Postgres bundle) to nil.
// Table-driven (rather than one `if` per accessor) so adding accessors —
// e.g. PgKnowledge/PgPlaybook/SqliteKnowledge/SqlitePlaybook for the ADR 0003
// accept-seam contract — doesn't keep pushing this function's cyclomatic
// complexity over the gocyclo threshold.
func TestNewServerStores_SQLite_HappyPath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wbt.db")
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: dbPath,
	})
	if err != nil {
		t.Fatalf("NewServerStores(sqlite): %v", err)
	}
	defer func() {
		if cerr := stores.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	}()

	wantNonNil := map[string]any{
		"GTD":             stores.GTD(),
		"Workspace":       stores.Workspace(),
		"Decision":        stores.Decision(),
		"Session":         stores.Session(),
		"Knowledge":       stores.Knowledge(),
		"Learning":        stores.Learning(),
		"Proposal":        stores.Proposal(),
		"SqliteKnowledge": stores.SqliteKnowledge(),
		"SqlitePlaybook":  stores.SqlitePlaybook(),
	}
	for name, v := range wantNonNil {
		if isNilStore(v) {
			t.Errorf("%s() returned nil", name)
		}
	}

	// PgGTD / PgProposal / PgLearning / PgKnowledge / PgPlaybook back
	// pgAcceptAdapter (ADR 0003 accept-seam contract) and other pgx-typed-tx
	// code paths — all must be nil on the SQLite bundle.
	wantNil := map[string]any{
		"PgxPool":     stores.PgxPool(),
		"PgGTD":       stores.PgGTD(),
		"PgProposal":  stores.PgProposal(),
		"PgLearning":  stores.PgLearning(),
		"PgKnowledge": stores.PgKnowledge(),
		"PgPlaybook":  stores.PgPlaybook(),
	}
	for name, v := range wantNil {
		if !isNilStore(v) {
			t.Errorf("%s() should be nil for sqlite backend, got %#v", name, v)
		}
	}
}

// isNilStore reports whether v holds a nil value, accounting for the typed
// nil that a `*wbtsqlite.KnowledgeStore(nil)` etc. becomes once boxed into
// an `any` — a plain `v == nil` comparison would be false for those.
func isNilStore(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		return rv.IsNil()
	default:
		return false
	}
}

func TestNewServerStores_SQLite_InMemory(t *testing.T) {
	// :memory: exercises the same code path with a transient DB and
	// verifies the schema bootstrap succeeds without filesystem writes.
	stores, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:    storage.BackendSQLite,
		SQLitePath: ":memory:",
	})
	if err != nil {
		t.Fatalf("NewServerStores(sqlite, :memory:): %v", err)
	}
	if err := stores.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestNewServerStores_SQLite_MissingPath(t *testing.T) {
	_, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend: storage.BackendSQLite,
	})
	if !errors.Is(err, storage.ErrMissingSQLitePath) {
		t.Errorf("expected ErrMissingSQLitePath, got %v", err)
	}
}

func TestNewServerStores_Postgres_MissingDSN(t *testing.T) {
	// We can validate the early DSN-required guard without hitting a real
	// Postgres server; the factory rejects an empty DSN before connecting.
	_, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend: storage.BackendPostgres,
	})
	if !errors.Is(err, storage.ErrMissingPostgresDSN) {
		t.Errorf("expected ErrMissingPostgresDSN, got %v", err)
	}
}

func TestNewServerStores_Postgres_BadDSN(t *testing.T) {
	// pgxpool.ParseConfig rejects malformed DSNs synchronously, which lets
	// us cover the postgres branch in CI without DB connectivity.
	_, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend:     storage.BackendPostgres,
		PostgresDSN: "not-a-dsn::::",
	})
	if err == nil {
		t.Fatalf("expected DSN parse error, got nil")
	}
}

func TestNewServerStores_UnknownBackend(t *testing.T) {
	_, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{
		Backend: storage.Backend("mysql"),
	})
	if !errors.Is(err, storage.ErrInvalidBackend) {
		t.Errorf("expected ErrInvalidBackend, got %v", err)
	}
}

func TestNewServerStores_DefaultBackendIsPostgres(t *testing.T) {
	// Empty Backend → postgres path → ErrMissingPostgresDSN, proving the
	// default branch is taken.
	_, err := storage.NewServerStores(context.Background(), storage.FactoryConfig{})
	if !errors.Is(err, storage.ErrMissingPostgresDSN) {
		t.Errorf("expected default to be postgres (got err %v)", err)
	}
}

func TestSQLitePathFromEnv_Default(t *testing.T) {
	t.Setenv("SQLITE_PATH", "")
	got := storage.SQLitePathFromEnv()
	if got != "./wayneblacktea.db" {
		t.Errorf("expected default ./wayneblacktea.db, got %q", got)
	}
}

func TestSQLitePathFromEnv_Override(t *testing.T) {
	t.Setenv("SQLITE_PATH", "  /tmp/custom.db  ")
	got := storage.SQLitePathFromEnv()
	if got != "/tmp/custom.db" {
		t.Errorf("expected /tmp/custom.db (trimmed), got %q", got)
	}
}
