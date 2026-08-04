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
		name        string
		envVal      string
		databaseURL string // set DATABASE_URL to simulate cloud envs
		want        storage.Backend
		wantErr     bool
		errIs       error
	}{
		{
			name:        "unset STORAGE_BACKEND, no DATABASE_URL → SQLite default",
			envVal:      "",
			databaseURL: "",
			want:        storage.BackendSQLite,
		},
		{
			name:        "unset STORAGE_BACKEND, DATABASE_URL set → BackendPostgres (auto-detect)",
			envVal:      "",
			databaseURL: "postgres://localhost/testdb",
			want:        storage.BackendPostgres,
		},
		{
			name:        "explicit sqlite overrides DATABASE_URL",
			envVal:      "sqlite",
			databaseURL: "postgres://localhost/testdb",
			want:        storage.BackendSQLite,
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
			t.Setenv("DATABASE_URL", tc.databaseURL)
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

// TestBackendFromEnv_MatchesBackendFor is the A5a equivalence check: for
// every case in TestBackendFromEnv's table, calling BackendFor directly with
// the same (STORAGE_BACKEND, DATABASE_URL) pair as explicit arguments MUST
// produce the byte-identical result to BackendFromEnv reading them from
// process env — proving BackendFromEnv's extraction into a thin env-reading
// wrapper changed nothing observable.
func TestBackendFromEnv_MatchesBackendFor(t *testing.T) {
	tests := []struct {
		name        string
		envVal      string
		databaseURL string
		want        storage.Backend
		wantErr     bool
		errIs       error
	}{
		{
			name:        "STORAGE_BACKEND=postgres",
			envVal:      "postgres",
			databaseURL: "postgres://localhost/testdb",
			want:        storage.BackendPostgres,
		},
		{
			name:        "STORAGE_BACKEND=sqlite",
			envVal:      "sqlite",
			databaseURL: "postgres://localhost/testdb",
			want:        storage.BackendSQLite,
		},
		{
			name:        "STORAGE_BACKEND=mysql (invalid)",
			envVal:      "mysql",
			databaseURL: "",
			wantErr:     true,
			errIs:       storage.ErrInvalidBackend,
		},
		{
			name:        "STORAGE_BACKEND empty, DATABASE_URL set → postgres",
			envVal:      "",
			databaseURL: "postgres://localhost/testdb",
			want:        storage.BackendPostgres,
		},
		{
			name:        "STORAGE_BACKEND empty, DATABASE_URL empty → sqlite",
			envVal:      "",
			databaseURL: "",
			want:        storage.BackendSQLite,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STORAGE_BACKEND", tc.envVal)
			t.Setenv("DATABASE_URL", tc.databaseURL)

			envGot, envErr := storage.BackendFromEnv()
			forGot, forErr := storage.BackendFor(tc.envVal, tc.databaseURL)

			if envGot != forGot {
				t.Errorf("BackendFromEnv() = %q, BackendFor() = %q; want equal", envGot, forGot)
			}
			if (envErr == nil) != (forErr == nil) {
				t.Fatalf("BackendFromEnv() err = %v, BackendFor() err = %v; want both nil or both non-nil", envErr, forErr)
			}

			if tc.wantErr {
				if envErr == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errIs != nil {
					if !errors.Is(envErr, tc.errIs) {
						t.Errorf("BackendFromEnv() error: expected errors.Is(%v), got %v", tc.errIs, envErr)
					}
					if !errors.Is(forErr, tc.errIs) {
						t.Errorf("BackendFor() error: expected errors.Is(%v), got %v", tc.errIs, forErr)
					}
				}
				return
			}
			if envErr != nil {
				t.Fatalf("unexpected error: %v", envErr)
			}
			if envGot != tc.want {
				t.Errorf("got %q, want %q", envGot, tc.want)
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
