package storage_test

import (
	"errors"
	"testing"

	"github.com/Wayne997035/wayneblacktea/internal/storage"
)

// TestBackendFromEnv covers the five required cases.
// Subtests are run sequentially (not parallel) because t.Setenv is used;
// Go 1.26 panics if t.Parallel is combined with t.Setenv.
func TestBackendFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		envVal  string
		want    storage.Backend
		wantErr bool
		errIs   error
	}{
		{
			name:   "unset (empty string) → SQLite default",
			envVal: "",
			want:   storage.BackendSQLite,
		},
		{
			name:   "explicit sqlite → BackendSQLite",
			envVal: "sqlite",
			want:   storage.BackendSQLite,
		},
		{
			name:   "explicit postgres → BackendPostgres",
			envVal: "postgres",
			want:   storage.BackendPostgres,
		},
		{
			name:    "invalid value → ErrInvalidBackend",
			envVal:  "mysql",
			wantErr: true,
			errIs:   storage.ErrInvalidBackend,
		},
		{
			name:   "whitespace-padded postgres → trimmed to BackendPostgres",
			envVal: "  postgres  ",
			want:   storage.BackendPostgres,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STORAGE_BACKEND", tc.envVal)
			got, err := storage.BackendFromEnv()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errIs != nil && !errors.Is(err, tc.errIs) {
					t.Errorf("expected errors.Is(%v), got %v", tc.errIs, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("BackendFromEnv() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnsureSupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend storage.Backend
		wantErr bool
		errIs   error
	}{
		{
			name:    "postgres is supported",
			backend: storage.BackendPostgres,
		},
		{
			name:    "sqlite is supported",
			backend: storage.BackendSQLite,
		},
		{
			name:    "mysql is unsupported → ErrInvalidBackend",
			backend: "mysql",
			wantErr: true,
			errIs:   storage.ErrInvalidBackend,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := storage.EnsureSupported(tc.backend)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errIs != nil && !errors.Is(err, tc.errIs) {
					t.Errorf("expected errors.Is(%v), got %v", tc.errIs, err)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
