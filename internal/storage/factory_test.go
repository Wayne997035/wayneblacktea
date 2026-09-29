// Package storage's own tests. This file is package storage (white-box),
// not storage_test: TestPrunerOrNil (F0929-65) needs direct access to the
// unexported prunerOrNil helper, which an external _test package cannot
// reach.
package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/Wayne997035/wayneblacktea/internal/learning"
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
	stores, err := NewServerStores(context.Background(), FactoryConfig{
		Backend:    BackendSQLite,
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
	stores, err := NewServerStores(context.Background(), FactoryConfig{
		Backend:    BackendSQLite,
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
	_, err := NewServerStores(context.Background(), FactoryConfig{
		Backend: BackendSQLite,
	})
	if !errors.Is(err, ErrMissingSQLitePath) {
		t.Errorf("expected ErrMissingSQLitePath, got %v", err)
	}
}

func TestNewServerStores_Postgres_MissingDSN(t *testing.T) {
	// We can validate the early DSN-required guard without hitting a real
	// Postgres server; the factory rejects an empty DSN before connecting.
	_, err := NewServerStores(context.Background(), FactoryConfig{
		Backend: BackendPostgres,
	})
	if !errors.Is(err, ErrMissingPostgresDSN) {
		t.Errorf("expected ErrMissingPostgresDSN, got %v", err)
	}
}

func TestNewServerStores_Postgres_BadDSN(t *testing.T) {
	// pgxpool.ParseConfig rejects malformed DSNs synchronously, which lets
	// us cover the postgres branch in CI without DB connectivity.
	_, err := NewServerStores(context.Background(), FactoryConfig{
		Backend:     BackendPostgres,
		PostgresDSN: "not-a-dsn::::",
	})
	if err == nil {
		t.Fatalf("expected DSN parse error, got nil")
	}
}

func TestNewServerStores_UnknownBackend(t *testing.T) {
	_, err := NewServerStores(context.Background(), FactoryConfig{
		Backend: Backend("mysql"),
	})
	if !errors.Is(err, ErrInvalidBackend) {
		t.Errorf("expected ErrInvalidBackend, got %v", err)
	}
}

func TestNewServerStores_DefaultBackendIsPostgres(t *testing.T) {
	// Empty Backend → postgres path → ErrMissingPostgresDSN, proving the
	// default branch is taken.
	_, err := NewServerStores(context.Background(), FactoryConfig{})
	if !errors.Is(err, ErrMissingPostgresDSN) {
		t.Errorf("expected default to be postgres (got err %v)", err)
	}
}

func TestSQLitePathFromEnv_Default(t *testing.T) {
	t.Setenv("SQLITE_PATH", "")
	got := SQLitePathFromEnv()
	if got != "./wayneblacktea.db" {
		t.Errorf("expected default ./wayneblacktea.db, got %q", got)
	}
}

func TestSQLitePathFromEnv_Override(t *testing.T) {
	t.Setenv("SQLITE_PATH", "  /tmp/custom.db  ")
	got := SQLitePathFromEnv()
	if got != "/tmp/custom.db" {
		t.Errorf("expected /tmp/custom.db (trimmed), got %q", got)
	}
}

// fakeKnowledgeStoreNoPruner/fakeLearningStoreNoPruner embed their StoreIface
// as a nil interface and override nothing — they satisfy the interface for
// the compiler but panic if any method is actually called. That's fine here:
// prunerOrNil only performs a type assertion against decay.PrunerStore, it
// never calls a StoreIface method. Same embedded-nil-interface convention as
// internal/cli/doctor_cmd_test.go's fakeErrStore. [F0929-65]
type (
	fakeKnowledgeStoreNoPruner struct{ knowledge.StoreIface }
	fakeLearningStoreNoPruner  struct{ learning.StoreIface }
)

// TestPrunerOrNil covers all 4 acceptance rows for F0929-65: a real backend
// store (even a typed-nil pointer, since the assertion is a static type
// check) narrows to a non-nil decay.PrunerStore, while a fake that
// implements the domain StoreIface but not decay.PrunerStore narrows to nil.
func TestPrunerOrNil(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		wantNil bool
	}{
		{"typed-nil *knowledge.Store implements PrunerStore", (*knowledge.Store)(nil), false},
		{"typed-nil *learning.Store implements PrunerStore", (*learning.Store)(nil), false},
		{"fakeKnowledgeStoreNoPruner does not implement PrunerStore", &fakeKnowledgeStoreNoPruner{}, true},
		{"fakeLearningStoreNoPruner does not implement PrunerStore", &fakeLearningStoreNoPruner{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := prunerOrNil(tc.in)
			if tc.wantNil && got != nil {
				t.Errorf("prunerOrNil(%T) = %#v, want nil", tc.in, got)
			}
			if !tc.wantNil && got == nil {
				t.Errorf("prunerOrNil(%T) = nil, want non-nil", tc.in)
			}
		})
	}
}
