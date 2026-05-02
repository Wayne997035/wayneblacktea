package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // Postgres pgx/v5 driver
	"github.com/golang-migrate/migrate/v4/source/iofs"

	migrationfs "github.com/Wayne997035/wayneblacktea/migrations"
)

// RunMigrations applies all pending Postgres migrations using the migrations/
// directory embedded at compile time. It is a no-op when the environment
// variable WBT_AUTO_MIGRATE is set to "false".
//
// Fail-fast design: if migrations fail, the returned error causes the server
// to abort startup. This prevents running against a stale schema.
//
// SQLite backend: the sqlite package manages its own schema via schema.sql;
// RunMigrations is Postgres-only and must not be called for SQLite.
func RunMigrations(ctx context.Context, dsn string) error {
	if os.Getenv("WBT_AUTO_MIGRATE") == "false" {
		slog.InfoContext(ctx, "migrate: auto-migrate disabled via WBT_AUTO_MIGRATE=false — skipping")
		return nil
	}

	src, err := iofs.New(migrationfs.FS, ".")
	if err != nil {
		return fmt.Errorf("migrate: load embedded source: %w", err)
	}

	// golang-migrate's pgx/v5 driver registers under the "pgx5://" scheme.
	pgxDSN := toPgx5DSN(dsn)

	m, err := migrate.NewWithSourceInstance("iofs", src, pgxDSN)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			slog.Warn("migrate: close source error", "error", srcErr)
		}
		if dbErr != nil {
			slog.Warn("migrate: close db error", "error", dbErr)
		}
	}()

	// Capture current version before applying to log the transition.
	// Version() returns (uint, bool, error); discarding error is intentional —
	// ErrNilVersion means no migrations applied yet (fresh DB), which is fine.
	before, _, _ := m.Version()

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			slog.InfoContext(ctx, "migrate: no pending migrations — schema is up to date")
			return nil
		}
		return fmt.Errorf("migrate: apply: %w", err)
	}

	after, _, _ := m.Version() // best-effort; version is informational only
	slog.InfoContext(ctx, "migrate: applied pending migrations",
		"from_version", before,
		"to_version", after,
	)
	return nil
}

// toPgx5DSN converts a postgres:// or postgresql:// DSN to the pgx5:// scheme
// required by the golang-migrate pgx/v5 database driver.
func toPgx5DSN(dsn string) string {
	for _, prefix := range []string{"postgresql://", "postgres://"} {
		if strings.HasPrefix(dsn, prefix) {
			return "pgx5://" + dsn[len(prefix):]
		}
	}
	return dsn
}
