package storage_test

import (
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

func TestBackendFromEnv_DefaultPostgres(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "")
	got, err := storage.BackendFromEnv()
	if err != nil {
		t.Fatalf("BackendFromEnv: %v", err)
	}
	if got != storage.BackendPostgres {
		t.Errorf("expected default=postgres, got %q", got)
	}
}

func TestBackendFromEnv_Postgres(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "postgres")
	got, _ := storage.BackendFromEnv()
	if got != storage.BackendPostgres {
		t.Errorf("expected postgres, got %q", got)
	}
}

func TestBackendFromEnv_SQLite(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "sqlite")
	got, err := storage.BackendFromEnv()
	if err != nil {
		t.Fatalf("BackendFromEnv: %v", err)
	}
	if got != storage.BackendSQLite {
		t.Errorf("expected sqlite, got %q", got)
	}
}

func TestBackendFromEnv_Invalid(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "mysql")
	_, err := storage.BackendFromEnv()
	if !errors.Is(err, storage.ErrInvalidBackend) {
		t.Errorf("expected ErrInvalidBackend, got %v", err)
	}
}

func TestEnsureSupported(t *testing.T) {
	if err := storage.EnsureSupported(storage.BackendPostgres); err != nil {
		t.Errorf("postgres should be supported, got %v", err)
	}
	if err := storage.EnsureSupported(storage.BackendSQLite); !errors.Is(err, storage.ErrSQLiteNotImplemented) {
		t.Errorf("sqlite should report not-implemented, got %v", err)
	}
	if err := storage.EnsureSupported("mysql"); !errors.Is(err, storage.ErrInvalidBackend) {
		t.Errorf("invalid backend should report ErrInvalidBackend, got %v", err)
	}
}

func TestBackendFromEnv_TrimsWhitespace(t *testing.T) {
	t.Setenv("STORAGE_BACKEND", "  postgres  ")
	got, err := storage.BackendFromEnv()
	if err != nil {
		t.Fatalf("BackendFromEnv: %v", err)
	}
	if got != storage.BackendPostgres {
		t.Errorf("expected trimmed postgres, got %q", got)
	}
}
