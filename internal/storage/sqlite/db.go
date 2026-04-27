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
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite ping %q: %w", dsn, err)
	}
	// Foreign keys are off by default in SQLite. Enable per-connection.
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite enable FK: %w", err)
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
