package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
	// Accept "false", "0", "FALSE", etc. via strconv.ParseBool.
	// Empty string = enabled (default). Invalid values are treated as enabled (fail-safe).
	if v := os.Getenv("WBT_AUTO_MIGRATE"); v != "" {
		if disabled, err := strconv.ParseBool(v); err == nil && !disabled {
			slog.InfoContext(ctx, "migrate: auto-migrate disabled via WBT_AUTO_MIGRATE — skipping")
			return nil
		}
	}

	// golang-migrate's pgconn driver reads PGSSLROOTCERT from the environment and
	// unconditionally calls os.ReadFile on it. When the variable holds inline PEM
	// content (cloud deploys without file mounting), ReadFile fails with "file name
	// too long". Write the PEM to a temp file for the duration of the migration.
	cleanup, err := materializePGSSLROOTCERT()
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	defer cleanup()

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

// materializePGSSLROOTCERT writes inline PEM in PGSSLROOTCERT to a temp file
// and redirects the env var to that path. Returns a no-op cleanup when
// PGSSLROOTCERT is already a file path or unset. The caller MUST call cleanup()
// (via defer) to restore the original value and remove the temp file.
//
// This is needed because pgconn (used by both pgxpool and golang-migrate's pgx/v5
// driver) unconditionally calls os.ReadFile(PGSSLROOTCERT); cloud platforms like
// Railway have no file-mounting and store the CA cert as an env var with inline PEM.
func materializePGSSLROOTCERT() (cleanup func(), err error) {
	pgsslrootcert := os.Getenv("PGSSLROOTCERT")
	if !strings.HasPrefix(strings.TrimSpace(pgsslrootcert), pemCertPrefix) {
		return func() {}, nil
	}
	tmp, err := os.CreateTemp("", "pgsslrootcert*.pem")
	if err != nil {
		return nil, fmt.Errorf("write temp CA cert: %w", err)
	}
	if _, err := tmp.WriteString(strings.TrimSpace(pgsslrootcert)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("write temp CA cert: %w", err)
	}
	_ = tmp.Close()
	if err := os.Setenv("PGSSLROOTCERT", tmp.Name()); err != nil {
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("set PGSSLROOTCERT temp path: %w", err)
	}
	return func() {
		_ = os.Setenv("PGSSLROOTCERT", pgsslrootcert)
		_ = os.Remove(tmp.Name())
	}, nil
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
