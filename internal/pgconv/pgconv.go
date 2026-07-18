// Package pgconv holds small Go-value <-> pgtype conversion helpers that were
// duplicated verbatim across the gtd, decision, session, workspace, vision,
// and proposal store.go files. Consolidated here so future backend changes
// (e.g. NULL-handling semantics) only need to happen in one place.
package pgconv

import (
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ToText converts a Go string into a pgtype.Text, treating the empty string
// as NULL (Valid: false).
func ToText(v string) pgtype.Text {
	return pgtype.Text{String: v, Valid: v != ""}
}

// ToUUID converts an optional *uuid.UUID into a pgtype.UUID. A nil pointer
// maps to NULL (Valid: false).
func ToUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// ToTimestamptz converts an optional *time.Time into a pgtype.Timestamptz. A
// nil pointer maps to NULL (Valid: false).
func ToTimestamptz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
