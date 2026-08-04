// Package storage exposes the backend selection plumbing for wayneblacktea.
//
// The personal-OS goal of friend-grade self-hosting drives a pluggable
// storage layer: the canonical PostgreSQL deployment for Wayne's instance,
// and a zero-infra SQLite backend (modernc.org/sqlite, pure-Go, no CGo)
// for friends installing locally with one binary + one .db file.
//
// Both backends are runnable today via NewServerStores in factory.go.
// EnsureSupported below permits both BackendPostgres and BackendSQLite; the
// only failure mode it still guards against is an unknown STORAGE_BACKEND
// value (e.g. "mysql"), which we want to reject at startup rather than
// halfway through the first request.
//
// Per-domain Go interfaces (`<domain>.StoreIface`) under each domain
// package — see internal/gtd/iface.go, internal/decision/iface.go, etc. —
// keep handler / MCP code backend-agnostic; both Postgres- and SQLite-backed
// Store types satisfy those interfaces via compile-time `var _ Iface = ...`
// assertions, and ServerStores in server_stores.go bundles the seven of them.
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

// ErrInvalidBackend is returned by BackendFromEnv when the value is set but
// is neither "postgres" nor "sqlite".
var ErrInvalidBackend = errors.New("STORAGE_BACKEND must be 'postgres' or 'sqlite'")

// BackendFor resolves the Backend from explicit rawBackend/dsn values,
// mirroring BackendFromEnv's resolution order without touching process env:
//
//  1. rawBackend is set (after trimming) → use its value (postgres|sqlite).
//  2. rawBackend is unset and dsn is set → BackendPostgres. This lets
//     Railway / Heroku / Docker deployments work without requiring an extra
//     STORAGE_BACKEND variable when a DSN is already present.
//  3. rawBackend is unset and dsn is unset → BackendSQLite (local-first
//     default for zero-infra installs).
//
// Callers that read from process env directly should prefer BackendFromEnv;
// BackendFor exists so callers with an explicit DSN (e.g. a fallback-file
// value that was never written to os.Environ) can resolve the same way
// without mutating env first. See backend-security-design.md §4.2.
func BackendFor(rawBackend, dsn string) (Backend, error) {
	raw := strings.TrimSpace(rawBackend)
	if raw == "" {
		if strings.TrimSpace(dsn) != "" {
			return BackendPostgres, nil
		}
		return BackendSQLite, nil
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

// BackendFromEnv reads the STORAGE_BACKEND and DATABASE_URL environment
// variables and returns the resolved Backend. See BackendFor for the
// resolution order; this is a thin env-reading wrapper around it.
func BackendFromEnv() (Backend, error) {
	return BackendFor(os.Getenv("STORAGE_BACKEND"), os.Getenv("DATABASE_URL"))
}

// EnsureSupported returns nil when the given backend is one we ship a real
// implementation for, and ErrInvalidBackend (wrapped) for any unknown value.
//
// As of SQLite v2 cmd dispatch (this commit) both BackendPostgres and
// BackendSQLite are runnable; the only failure mode is an unknown enum value
// (e.g. "mysql") that bypassed BackendFromEnv. We keep the function so callers
// can re-validate after constructing a Backend by hand (tests, future
// migration tooling) without re-implementing the switch.
func EnsureSupported(b Backend) error {
	switch b {
	case BackendPostgres, BackendSQLite:
		return nil
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
