package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

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
	// Foreign keys are off by default in SQLite. We keep this PRAGMA ON as
	// defence-in-depth in case migration 000026.down.sql is rolled back at
	// runtime and historical FK declarations resurface; the app itself no
	// longer relies on FK behaviour (red line #9 — referential integrity in
	// code, not in the DB; see sql/queries/gtd.sql DeleteTask comment and
	// internal/gtd/store.go DeleteTask).
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
	// applyColumnUpgrades adds columns introduced after an existing DB was created.
	// schema.sql uses CREATE TABLE IF NOT EXISTS — new columns in the definition
	// are silently skipped on existing tables. Each ALTER here is idempotent:
	// "duplicate column name" errors are ignored so the call is safe on both
	// fresh and pre-existing databases.
	if err := applyColumnUpgrades(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite apply column upgrades: %w", err)
	}
	// Rebuilds the FTS5 inverted index unconditionally from knowledge_items; at personal scale completes in <1 ms.
	if _, err := conn.ExecContext(ctx, `INSERT INTO knowledge_items_fts(knowledge_items_fts) VALUES('rebuild')`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sqlite fts5 rebuild: %w", err)
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

// BeginTx starts a new database transaction scoped to ctx. The caller is
// responsible for calling Commit or Rollback on the returned *sql.Tx.
// Exported so multi-store cross-domain operations (e.g. the confirm_proposal
// accept path) can wrap several store writes in one atomic SQLite transaction.
func (d *DB) BeginTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite BeginTx: %w", err)
	}
	return tx, nil
}

// ExecContext executes a query on the underlying connection. Exported so
// integration tests in sibling packages can insert fixture rows (e.g. parent
// tasks / sessions for cascade-cleanup tests) without depending on production
// write paths.
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

// SqlConn returns the underlying *sql.DB. Callers should prefer the typed store
// methods; this accessor exists for domain stores (like completioncandidate)
// that need sql.Rows-based list queries not covered by ExecContext/QueryRowContext.
// The *sql.DB is the same connection pool shared by all stores — no extra connections
// are opened. SQLite max-conns is 1 (set in Open), so concurrent writers are
// serialised automatically.
func (d *DB) SqlConn() *sql.DB {
	return d.conn
}

// WorkspaceID returns the configured workspace UUID string, or "" when operating
// in legacy unscoped mode.
func (d *DB) WorkspaceID() string {
	return d.workspaceID
}

// columnUpgrade describes a single idempotent ALTER TABLE ADD COLUMN operation.
type columnUpgrade struct {
	table      string
	column     string
	definition string // column type + constraints, e.g. "TEXT NOT NULL DEFAULT '[]'"
}

// columnUpgrades lists columns added to existing tables after the initial
// schema.sql was shipped. Each entry mirrors a migrations/sqlite/000NNN_*.up.sql
// file that performs the same ALTER TABLE. ORDER MATTERS: apply in ascending
// migration number order so dependencies are satisfied.
var columnUpgrades = []columnUpgrade{
	// migration 000063: related_rule_ids on outcomes (TEXT JSON array, default empty).
	// Fresh DBs already have this via schema.sql CREATE TABLE IF NOT EXISTS; this
	// ALTER is needed only for DBs created before 000063 was merged.
	{table: "outcomes", column: "related_rule_ids", definition: "TEXT NOT NULL DEFAULT '[]'"},
	// migration 000064: embedding provider metadata on session_handoffs, decisions,
	// project_status_snapshots. Nullable columns; existing rows keep NULL and are
	// treated as 'hashed' by SearchByCosine provider-filter logic.
	{table: "session_handoffs", column: "embedding_provider", definition: "TEXT"},
	{table: "session_handoffs", column: "embedding_model", definition: "TEXT"},
	{table: "session_handoffs", column: "embedding_dim", definition: "INTEGER"},
	{table: "decisions", column: "embedding_provider", definition: "TEXT"},
	{table: "decisions", column: "embedding_model", definition: "TEXT"},
	{table: "decisions", column: "embedding_dim", definition: "INTEGER"},
	{table: "project_status_snapshots", column: "embedding_provider", definition: "TEXT"},
	{table: "project_status_snapshots", column: "embedding_model", definition: "TEXT"},
	{table: "project_status_snapshots", column: "embedding_dim", definition: "INTEGER"},
}

// applyColumnUpgrades idempotently adds columns to existing tables.
// SQLite does not support ALTER TABLE ADD COLUMN IF NOT EXISTS, so we detect
// "duplicate column name" errors by string-matching the error message and treat
// them as success. All other errors are returned as failures.
func applyColumnUpgrades(ctx context.Context, conn *sql.DB) error {
	for _, u := range columnUpgrades {
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", u.table, u.column, u.definition)
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			// SQLite returns "duplicate column name: <col>" when the column exists.
			// Treat this as idempotent success — the column is already present.
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
			return fmt.Errorf("adding column %s.%s: %w", u.table, u.column, err)
		}
	}
	return nil
}
