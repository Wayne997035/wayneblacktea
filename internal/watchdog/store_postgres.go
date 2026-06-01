package watchdog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgDisciplineEventStore is the Postgres-backed implementation of
// DisciplineEventStoreIface for the discipline_events_m8 table.
type PgDisciplineEventStore struct {
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID // zero = unscoped (legacy single-tenant mode)
}

// Compile-time assertion.
var _ DisciplineEventStoreIface = (*PgDisciplineEventStore)(nil)

// NewPgDisciplineEventStore returns a PgDisciplineEventStore backed by pool,
// scoped to the optional workspaceID (nil = unscoped).
func NewPgDisciplineEventStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *PgDisciplineEventStore {
	s := &PgDisciplineEventStore{pool: pool}
	if workspaceID != nil {
		s.workspaceID = pgtype.UUID{Bytes: [16]byte(*workspaceID), Valid: true}
	}
	return s
}

// Insert records a new discipline_events_m8 row.
func (s *PgDisciplineEventStore) Insert(ctx context.Context, p InsertParams) error {
	detail := NullDetail(p.Detail)
	sev := p.Severity
	if sev == "" {
		sev = SeverityWarn
	}

	// Resolve workspace: explicit param overrides the store-level scope,
	// otherwise use the store's configured workspace.
	wsID := s.workspaceID
	if p.WorkspaceID != nil {
		wsID = pgtype.UUID{Bytes: [16]byte(*p.WorkspaceID), Valid: true}
	}

	const q = `
INSERT INTO discipline_events_m8 (workspace_id, event_type, severity, detail)
VALUES ($1, $2, $3, $4)`

	_, err := s.pool.Exec(ctx, q, wsID, string(p.EventType), string(sev), []byte(detail))
	if err != nil {
		return fmt.Errorf("PgDisciplineEventStore.Insert: %w", err)
	}
	return nil
}

// ListUnresolved returns all open events (resolved_at IS NULL).
// When wsID is nil, all workspaces are returned.
func (s *PgDisciplineEventStore) ListUnresolved(ctx context.Context, wsID *uuid.UUID) ([]DisciplineEvent, error) {
	var rows pgx.Rows
	var err error

	if wsID != nil {
		const q = `
SELECT id, workspace_id, event_type, severity, detail, resolved_at, created_at
FROM discipline_events_m8
WHERE resolved_at IS NULL AND workspace_id = $1
ORDER BY created_at DESC`
		wsUUID := pgtype.UUID{Bytes: [16]byte(*wsID), Valid: true}
		rows, err = s.pool.Query(ctx, q, wsUUID)
	} else {
		const q = `
SELECT id, workspace_id, event_type, severity, detail, resolved_at, created_at
FROM discipline_events_m8
WHERE resolved_at IS NULL
ORDER BY created_at DESC`
		rows, err = s.pool.Query(ctx, q)
	}
	if err != nil {
		return nil, fmt.Errorf("PgDisciplineEventStore.ListUnresolved: %w", err)
	}
	defer rows.Close()

	var out []DisciplineEvent
	for rows.Next() {
		ev, scanErr := scanDisciplineEvent(rows.Scan)
		if scanErr != nil {
			return nil, fmt.Errorf("PgDisciplineEventStore.ListUnresolved scan: %w", scanErr)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PgDisciplineEventStore.ListUnresolved iter: %w", err)
	}
	return out, nil
}

// MarkResolved sets resolved_at = NOW() for the given event.
func (s *PgDisciplineEventStore) MarkResolved(ctx context.Context, eventID uuid.UUID) error {
	const q = `UPDATE discipline_events_m8 SET resolved_at = NOW() WHERE id = $1`
	tag, err := s.pool.Exec(ctx, q, pgtype.UUID{Bytes: [16]byte(eventID), Valid: true})
	if err != nil {
		return fmt.Errorf("PgDisciplineEventStore.MarkResolved: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEventNotFound
	}
	return nil
}

// PruneOlderThan hard-deletes rows with created_at < cutoff.
func (s *PgDisciplineEventStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	const q = `DELETE FROM discipline_events_m8 WHERE created_at < $1`
	tag, err := s.pool.Exec(ctx, q, pgtype.Timestamptz{Time: cutoff.UTC(), Valid: true})
	if err != nil {
		return 0, fmt.Errorf("PgDisciplineEventStore.PruneOlderThan: %w", err)
	}
	return tag.RowsAffected(), nil
}

// scanDisciplineEvent scans a single discipline_events_m8 row using the
// provided scan function (compatible with pgx.Rows.Scan).
func scanDisciplineEvent(scan func(...any) error) (DisciplineEvent, error) {
	var (
		ev           DisciplineEvent
		id           pgtype.UUID
		wsID         pgtype.UUID
		eventTypeStr string
		severityStr  string
		detailRaw    []byte
		resolvedAt   pgtype.Timestamptz
		createdAt    pgtype.Timestamptz
	)
	if err := scan(&id, &wsID, &eventTypeStr, &severityStr, &detailRaw, &resolvedAt, &createdAt); err != nil {
		return DisciplineEvent{}, fmt.Errorf("scanning discipline_events_m8: %w", err)
	}
	ev.ID = uuid.UUID(id.Bytes)
	if wsID.Valid {
		u := uuid.UUID(wsID.Bytes)
		ev.WorkspaceID = &u
	}
	ev.EventType = EventType(eventTypeStr)
	ev.Severity = Severity(severityStr)
	if len(detailRaw) > 0 {
		ev.Detail = json.RawMessage(detailRaw)
	} else {
		ev.Detail = json.RawMessage("null")
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		ev.ResolvedAt = &t
	}
	if createdAt.Valid {
		ev.CreatedAt = createdAt.Time
	}
	return ev, nil
}
