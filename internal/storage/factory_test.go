package storage_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

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

	if stores.GTD() == nil {
		t.Errorf("GTD() returned nil")
	}
	if stores.Workspace() == nil {
		t.Errorf("Workspace() returned nil")
	}
	if stores.Decision() == nil {
		t.Errorf("Decision() returned nil")
	}
	if stores.Session() == nil {
		t.Errorf("Session() returned nil")
	}
	if stores.Knowledge() == nil {
		t.Errorf("Knowledge() returned nil")
	}
	if stores.Learning() == nil {
		t.Errorf("Learning() returned nil")
	}
	if stores.Proposal() == nil {
		t.Errorf("Proposal() returned nil")
	}
	if stores.PgxPool() != nil {
		t.Errorf("PgxPool() should be nil for sqlite backend")
	}
	if stores.PgGTD() != nil {
		t.Errorf("PgGTD() should be nil for sqlite backend")
	}
	if stores.PgProposal() != nil {
		t.Errorf("PgProposal() should be nil for sqlite backend")
	}
	if stores.PgLearning() != nil {
		t.Errorf("PgLearning() should be nil for sqlite backend")
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
