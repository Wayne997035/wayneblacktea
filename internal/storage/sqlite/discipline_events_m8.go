package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/watchdog"
	"github.com/google/uuid"
)

// DisciplineEventM8Store is the SQLite-backed implementation of
// watchdog.DisciplineEventStoreIface for the discipline_events_m8 table.
type DisciplineEventM8Store struct {
	db *DB
}

// Compile-time assertion.
var _ watchdog.DisciplineEventStoreIface = (*DisciplineEventM8Store)(nil)

// NewDisciplineEventM8Store returns a DisciplineEventM8Store backed by an
// open SQLite DB.
func NewDisciplineEventM8Store(d *DB) *DisciplineEventM8Store {
	return &DisciplineEventM8Store{db: d}
}

// Insert records a new discipline_events_m8 row with an application-generated
// UUID (SQLite has no gen_random_uuid()).
func (s *DisciplineEventM8Store) Insert(ctx context.Context, p watchdog.InsertParams) error {
	detail := watchdog.NullDetail(p.Detail)
	sev := p.Severity
	if sev == "" {
		sev = watchdog.SeverityWarn
	}
	wsID := ""
	if p.WorkspaceID != nil {
		wsID = p.WorkspaceID.String()
	} else if s.db.workspaceID != "" {
		wsID = s.db.workspaceID
	}

	id := uuid.New().String()
	const q = `INSERT INTO discipline_events_m8 (id, workspace_id, event_type, severity, detail)
VALUES (?, ?, ?, ?, ?)`
	_, err := s.db.conn.ExecContext(ctx, q, id, nullableStr(wsID), string(p.EventType), string(sev), string(detail))
	if err != nil {
		return errWrap("DisciplineEventM8Store.Insert", err)
	}
	return nil
}

// ListUnresolved returns all open events (resolved_at IS NULL), optionally
// scoped to the given workspace UUID.
func (s *DisciplineEventM8Store) ListUnresolved(ctx context.Context, wsID *uuid.UUID) ([]watchdog.DisciplineEvent, error) {
	var rows *sql.Rows
	var err error

	const colList = `id, workspace_id, event_type, severity, detail, resolved_at, created_at`
	if wsID != nil {
		q := `SELECT ` + colList + ` FROM discipline_events_m8
			WHERE resolved_at IS NULL AND workspace_id = ?
			ORDER BY created_at DESC`
		rows, err = s.db.conn.QueryContext(ctx, q, wsID.String())
	} else {
		q := `SELECT ` + colList + ` FROM discipline_events_m8
			WHERE resolved_at IS NULL
			ORDER BY created_at DESC`
		rows, err = s.db.conn.QueryContext(ctx, q)
	}
	if err != nil {
		return nil, errWrap("DisciplineEventM8Store.ListUnresolved", err)
	}
	defer func() { _ = rows.Close() }()

	var out []watchdog.DisciplineEvent
	for rows.Next() {
		ev, scanErr := scanDisciplineEventM8Row(rows.Scan)
		if scanErr != nil {
			return nil, errWrap("DisciplineEventM8Store.ListUnresolved scan", scanErr)
		}
		out = append(out, ev)
	}
	return out, errWrap("DisciplineEventM8Store.ListUnresolved iter", rows.Err())
}

// MarkResolved sets resolved_at = NOW() for the given event.
func (s *DisciplineEventM8Store) MarkResolved(ctx context.Context, eventID uuid.UUID) error {
	const q = `UPDATE discipline_events_m8 SET resolved_at = ? WHERE id = ?`
	nowStr := time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	result, err := s.db.conn.ExecContext(ctx, q, nowStr, eventID.String())
	if err != nil {
		return errWrap("DisciplineEventM8Store.MarkResolved", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return errWrap("DisciplineEventM8Store.MarkResolved rows affected", err)
	}
	if n == 0 {
		return watchdog.ErrEventNotFound
	}
	return nil
}

// PruneOlderThan hard-deletes rows with created_at < cutoff.
func (s *DisciplineEventM8Store) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffStr := cutoff.UTC().Format("2006-01-02T15:04:05.000Z07:00")
	const q = `DELETE FROM discipline_events_m8 WHERE created_at < ?`
	result, err := s.db.conn.ExecContext(ctx, q, cutoffStr)
	if err != nil {
		return 0, errWrap("DisciplineEventM8Store.PruneOlderThan", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, errWrap("DisciplineEventM8Store.PruneOlderThan rows affected", err)
	}
	return n, nil
}

// scanDisciplineEventM8Row reads a discipline_events_m8 row using the provided
// scan function.
func scanDisciplineEventM8Row(scan func(...any) error) (watchdog.DisciplineEvent, error) {
	var (
		idStr        string
		wsIDNS       sql.NullString
		eventTypeStr string
		severityStr  string
		detailStr    string
		resolvedNS   sql.NullString
		createdStr   string
	)
	if err := scan(&idStr, &wsIDNS, &eventTypeStr, &severityStr, &detailStr, &resolvedNS, &createdStr); err != nil {
		return watchdog.DisciplineEvent{}, fmt.Errorf("scanning discipline_events_m8: %w", err)
	}
	ev := watchdog.DisciplineEvent{
		EventType: watchdog.EventType(eventTypeStr),
		Severity:  watchdog.Severity(severityStr),
	}
	if id, err := uuid.Parse(idStr); err == nil {
		ev.ID = id
	}
	if wsIDNS.Valid && wsIDNS.String != "" {
		if id, err := uuid.Parse(wsIDNS.String); err == nil {
			ev.WorkspaceID = &id
		}
	}
	if detailStr != "" {
		ev.Detail = json.RawMessage(detailStr)
	} else {
		ev.Detail = json.RawMessage("null")
	}
	if resolvedNS.Valid && resolvedNS.String != "" {
		if t, err := time.Parse("2006-01-02T15:04:05.000Z07:00", resolvedNS.String); err == nil {
			ev.ResolvedAt = &t
		}
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000Z07:00", createdStr); err == nil {
		ev.CreatedAt = t
	}
	return ev, nil
}

// nullableStr returns a sql.NullString for use in query parameters.
func nullableStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}
