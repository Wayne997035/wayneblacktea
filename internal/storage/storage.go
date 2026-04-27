// Package storage exposes the backend selection plumbing for wayneblacktea.
//
// The personal-OS goal of friend-grade self-hosting drives a pluggable
// storage layer: the canonical PostgreSQL deployment for Wayne's instance,
// and a future zero-infra SQLite backend (sqlite-vec for vectors, FTS5 for
// full-text) for friends installing locally.
//
// Phase C (this commit) ships the architectural scaffolding:
//   - Backend enum + STORAGE_BACKEND env reading (this file).
//   - Per-domain Go interfaces (`<domain>.StoreIface`) under each domain
//     package — see internal/gtd/iface.go, internal/decision/iface.go, etc.
//     Existing Postgres-backed Store structs satisfy those interfaces via
//     compile-time `var _ Iface = (*Store)(nil)` assertions.
//
// What is NOT in Phase C: a runnable SQLite implementation. STORAGE_BACKEND=
// sqlite returns ErrSQLiteNotImplemented at startup. Implementing the SQLite
// stores requires (a) pure-Go SQLite driver decision (modernc.org/sqlite vs
// mattn/go-sqlite3 with sqlite-vec), (b) UUID and JSONB column adaptation,
// and (c) FTS5/sqlite-vec wiring for the knowledge domain. Those are tracked
// as a follow-up task and are non-trivial.
package storage

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Backend selects the underlying database engine each domain Store talks to.
type Backend string

const (
	// BackendPostgres uses pgxpool against a PostgreSQL server (Aiven,
	// Railway, local docker, …). The canonical deployment.
	BackendPostgres Backend = "postgres"
	// BackendSQLite uses a local file-backed SQLite database. Reserved for
	// the upcoming friend-grade self-host path; not yet implemented.
	BackendSQLite Backend = "sqlite"
)

// ErrSQLiteNotImplemented is returned at startup when STORAGE_BACKEND=sqlite
// is requested. Replace with a real implementation under
// internal/storage/sqlite/ before removing this sentinel.
var ErrSQLiteNotImplemented = errors.New(
	"STORAGE_BACKEND=sqlite is not yet implemented; use postgres or leave the env unset",
)

// ErrInvalidBackend is returned by BackendFromEnv when the value is set but
// is neither "postgres" nor "sqlite".
var ErrInvalidBackend = errors.New("STORAGE_BACKEND must be 'postgres' or 'sqlite'")

// BackendFromEnv reads the STORAGE_BACKEND environment variable and returns
// the resolved Backend. Empty / unset → BackendPostgres (the default).
func BackendFromEnv() (Backend, error) {
	raw := strings.TrimSpace(os.Getenv("STORAGE_BACKEND"))
	if raw == "" {
		return BackendPostgres, nil
	}
	switch Backend(raw) {
	case BackendPostgres:
		return BackendPostgres, nil
	case BackendSQLite:
		return BackendSQLite, nil
	default:
		return "", fmt.Errorf("%w: got %q", ErrInvalidBackend, raw)
	}
}

// EnsureSupported returns nil when the given backend is fully implemented
// today, and an actionable error when it is not. Call this at startup right
// after BackendFromEnv so the process fails fast with a clear message rather
// than mid-request when an unimplemented store is invoked.
func EnsureSupported(b Backend) error {
	switch b {
	case BackendPostgres:
		return nil
	case BackendSQLite:
		return ErrSQLiteNotImplemented
	default:
		return fmt.Errorf("%w: got %q", ErrInvalidBackend, string(b))
	}
}

// ResolveFromEnv combines BackendFromEnv + EnsureSupported into a single call
// for entry-point binaries. Returns the resolved backend or a wrapped error
// suitable for log.Fatal at startup.
func ResolveFromEnv() (Backend, error) {
	b, err := BackendFromEnv()
	if err != nil {
		return "", fmt.Errorf("resolving STORAGE_BACKEND: %w", err)
	}
	if err := EnsureSupported(b); err != nil {
		return "", fmt.Errorf("STORAGE_BACKEND=%s: %w", b, err)
	}
	return b, nil
}
