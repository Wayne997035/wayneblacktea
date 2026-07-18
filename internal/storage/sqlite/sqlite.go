// Package sqlite is the SQLite-backed implementation of the wayneblacktea
// storage interfaces, intended for friend-grade self-hosting (one binary +
// one .db file, no Postgres server).
//
// Schema evolution (as of E1, 2026-07-18): Open() applies the embedded
// migrations/sqlite/*.sql migration set via golang-migrate at connection
// time — see migrate.go. schema.sql is retired as the runtime authority and
// kept only as a historical test fixture (see its own header comment); the
// canonical target schema snapshot is testdata/schema_golden.sql.
//
// Driver: modernc.org/sqlite (pure Go, no CGo) so cross-compilation to
// Linux/macOS/arm64 stays painless.
package sqlite

import (
	_ "embed"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrNotImplemented is returned by stub stores in this package whose backend
// implementation is still pending. Test for it with errors.Is.
var ErrNotImplemented = errors.New("sqlite store: not yet implemented in this build")

// jsonNullLiteral / jsonNullText are aliases for the JSON null literal stored
// in TEXT columns that represent optional JSONB values.
const (
	jsonNullLiteral = "null"
	jsonNullText    = "null"
)

// schemaSQL is the historical, pre-E1 DDL — NO LONGER applied at DB.Open
// time (see migrate.go/runMigrations for the current mechanism). Kept
// package-private and used only by two test helpers that need to reconstruct
// what a pre-migration-runner install looked like; see schema.sql's own
// header comment for details. Not exported: there are no other callers, and
// exporting it would invite new code to depend on a frozen historical
// snapshot instead of the real migration chain.
//
//go:embed schema.sql
var schemaSQL string

// pgtypeUUID converts a SQLite TEXT UUID column value into pgtype.UUID so the
// SQLite stores can return the same db.* model types the Postgres stores do.
// Empty string → invalid (NULL). Malformed → invalid (caller treats as NULL).
func pgtypeUUID(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(id), Valid: true}
}

// pgtypeText wraps a possibly-empty string into pgtype.Text. SQLite columns
// are nullable; we treat NULL and the empty string as the same thing on
// scan, returning Valid=false for both.
func pgtypeText(s string, valid bool) pgtype.Text {
	if !valid || s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// errWrap wraps a SQLite driver error with a stable prefix so the caller can
// errors.Is against pgx-style sentinels (gtd.ErrNotFound, etc.) without us
// reaching for them directly here.
func errWrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("sqlite %s: %w", op, err)
}
