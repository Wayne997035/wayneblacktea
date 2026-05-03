package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"
)

// DB is the package-internal connection wrapper. It holds the *sql.DB plus
// the optional workspace UUID (echoing the Postgres stores' Init-time
// scoping). Stores share this and add their own typed methods on top.
type DB struct {
	conn        *sql.DB
	workspaceID string // empty = legacy unscoped mode
}

// Open creates a new DB by opening dsn (e.g. "file:wbt.db" or ":memory:")
// and applying the embedded schema idempotently. workspaceID may be empty.
func Open(ctx context.Context, dsn, workspaceID string) (*DB, error) {
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open %q: %w", dsn, err)
	}
	// Serialise all writes through a single connection. SQLite has no
	// multi-writer concurrency; a pool > 1 triggers SQLITE_BUSY under concurrent
	// goroutines. Readers coexist via WAL mode set below.
	conn.SetMaxOpenConns(1)
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite ping %q: %w", dsn, err)
	}
	// Foreign keys are off by default in SQLite. Enable per-connection.
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite enable FK: %w", err)
	}
	// WAL mode lets readers run concurrently with the single writer.
	// In-memory databases silently stay in "memory" mode (no-op, no error).
	if _, err := conn.ExecContext(ctx, `PRAGMA journal_mode=WAL`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite WAL mode: %w", err)
	}
	// busy_timeout: wait up to 5 s when another OS-level writer holds the lock.
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout=5000`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite busy timeout: %w", err)
	}
	if _, err := conn.ExecContext(ctx, schemaSQL); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite apply schema: %w", err)
	}
	return &DB{conn: conn, workspaceID: workspaceID}, nil
}

// Close releases the underlying connection.
func (d *DB) Close() error {
	if d == nil || d.conn == nil {
		return nil
	}
	return d.conn.Close() //nolint:wrapcheck // pass-through
}

// workspaceArg returns either the configured workspace UUID string or an
// empty interface representing SQL NULL — used as the `?` arg backing
// `(?1 IS NULL OR workspace_id = ?1)` predicates.
func (d *DB) workspaceArg() any {
	if d.workspaceID == "" {
		return nil
	}
	return d.workspaceID
}

// ExecContext executes a query on the underlying connection. Exported so
// integration tests in sibling packages can insert fixture rows (e.g. tasks
// to satisfy FK constraints) without depending on production write paths.
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) error {
	_, err := d.conn.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("sqlite ExecContext: %w", err)
	}
	return nil
}

// QueryRowContext is a thin wrapper exposed for sibling-package tests to
// assert post-condition state (e.g. that a referential cleanup performed by
// a service-layer DeleteTask actually NULL'd a column / removed a row).
// Production code paths inside the package use s.db.conn directly.
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.conn.QueryRowContext(ctx, query, args...)
}
