// Package sqlite is the SQLite-backed implementation of the wayneblacktea
// storage interfaces, intended for friend-grade self-hosting (one binary +
// one .db file, no Postgres server).
//
// Status (SQLite v2, this commit): 7 stores done.
//   - GTD store: fully implemented. Create/list/update/delete goals,
//     projects, tasks, and activity log all work, including the workspace
//     scoping contract.
//   - Session store: fully implemented. Set/Latest/Resolve session
//     handoffs honour the same workspace scoping pattern.
//   - Decision, workspace, knowledge, learning, and proposal stores are
//     implemented with the same workspace scoping pattern.
//   - Knowledge vector search and FTS5 are deferred; SQLite uses LIKE search
//     fallback for now.
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

//go:embed schema.sql
var schemaSQL string

// Schema returns the raw SQLite DDL applied at DB.Open time. Exposed for
// tests that need to seed an in-memory DB.
func Schema() string { return schemaSQL }

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
