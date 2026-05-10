package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/discipline"
	"github.com/google/uuid"
)

// DisciplineStore is the SQLite-backed implementation of discipline.Store.
type DisciplineStore struct {
	db *DB
}

// NewDisciplineStore wraps an open DB into a DisciplineStore.
func NewDisciplineStore(d *DB) *DisciplineStore {
	return &DisciplineStore{db: d}
}

// Compile-time guarantee against drift from discipline.Store.
var _ discipline.Store = (*DisciplineStore)(nil)

// Insert records a single discipline event. WorkspaceID falls back to the
// DB's configured workspace when the param value is nil.
func (s *DisciplineStore) Insert(ctx context.Context, p discipline.InsertParams) error {
	const q = `INSERT INTO discipline_events
		(session_id, repo_name, tool_name, is_mutating, observed_at, linked_decision_id, workspace_id)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)`

	wsArg := nullStringFromUUID(p.WorkspaceID)
	if wsArg == nil {
		wsArg = s.db.workspaceArg()
	}

	repoArg := nullStringFromText(p.RepoName)
	mutating := 0
	if p.IsMutating {
		mutating = 1
	}

	_, err := s.db.conn.ExecContext(ctx, q,
		p.SessionID,
		repoArg,
		p.ToolName,
		mutating,
		nowRFC3339(),
		nullStringFromUUID(p.LinkedDecisionID),
		wsArg,
	)
	if err != nil {
		return errWrap("DisciplineStore.Insert", err)
	}
	return nil
}

// nullStringFromText returns nil for an empty string and the value otherwise,
// so SQLite stores NULL instead of an empty string when the column is optional.
func nullStringFromText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const disciplineSelectCols = `id, session_id, repo_name, tool_name, is_mutating, observed_at, linked_decision_id, workspace_id`

// RecentMutating returns mutating events from the configured workspace
// observed at or after `since`, newest first, capped at limit.
//
// Scoping: when the DB has a workspaceID set, only rows with that exact
// workspace_id are returned. When the DB has no workspaceID (legacy
// single-tenant mode), only rows whose workspace_id IS NULL are returned.
// The two are disjoint — there is no fallback that lets an unscoped store
// see scoped rows or vice-versa. Mirrors PgStore.RecentMutating.
func (s *DisciplineStore) RecentMutating(ctx context.Context, since time.Time, limit int) ([]discipline.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `SELECT ` + disciplineSelectCols + ` FROM discipline_events
		WHERE is_mutating = 1
		  AND observed_at >= ?1
		  AND (workspace_id = ?2 OR (?2 IS NULL AND workspace_id IS NULL))
		ORDER BY observed_at DESC
		LIMIT ?3`

	rows, err := s.db.conn.QueryContext(ctx, q,
		since.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		s.db.workspaceArg(),
		limit,
	)
	if err != nil {
		return nil, errWrap("DisciplineStore.RecentMutating", err)
	}
	defer func() { _ = rows.Close() }()

	var out []discipline.Event
	for rows.Next() {
		ev, err := scanDisciplineRow(rows.Scan)
		if err != nil {
			return nil, errWrap("DisciplineStore.RecentMutating scan", err)
		}
		out = append(out, ev)
	}
	return out, errWrap("DisciplineStore.RecentMutating iter", rows.Err())
}

// RecentDecisionTimes returns observed_at timestamps of log_decision /
// confirm_plan events for the given session at or after `since`, newest first.
//
// Scoping mirrors RecentMutating: scoped DB sees only its own workspace_id;
// unscoped DB sees only NULL workspace_id rows. The two are disjoint.
func (s *DisciplineStore) RecentDecisionTimes(ctx context.Context, sessionID string, since time.Time) ([]time.Time, error) {
	const q = `SELECT observed_at FROM discipline_events
		WHERE session_id = ?1
		  AND tool_name IN ('log_decision', 'confirm_plan')
		  AND observed_at >= ?2
		  AND (workspace_id = ?3 OR (?3 IS NULL AND workspace_id IS NULL))
		ORDER BY observed_at DESC
		LIMIT 200`

	rows, err := s.db.conn.QueryContext(ctx, q,
		sessionID,
		since.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		s.db.workspaceArg(),
	)
	if err != nil {
		return nil, errWrap("DisciplineStore.RecentDecisionTimes", err)
	}
	defer func() { _ = rows.Close() }()

	var out []time.Time
	for rows.Next() {
		var ns sql.NullString
		if err := rows.Scan(&ns); err != nil {
			return nil, errWrap("DisciplineStore.RecentDecisionTimes scan", err)
		}
		if t := parseTimestamptz(ns); t.Valid {
			out = append(out, t.Time)
		}
	}
	return out, errWrap("DisciplineStore.RecentDecisionTimes iter", rows.Err())
}

func scanDisciplineRow(scan func(...any) error) (discipline.Event, error) {
	var (
		id          int64
		sessionID   string
		repoNS      sql.NullString
		toolName    string
		isMutating  int64
		observedNS  sql.NullString
		linkedNS    sql.NullString
		workspaceNS sql.NullString
	)
	err := scan(
		&id,
		&sessionID,
		&repoNS,
		&toolName,
		&isMutating,
		&observedNS,
		&linkedNS,
		&workspaceNS,
	)
	if err != nil {
		return discipline.Event{}, fmt.Errorf("scanning discipline event: %w", err)
	}
	ev := discipline.Event{
		ID:         id,
		SessionID:  sessionID,
		ToolName:   toolName,
		IsMutating: isMutating != 0,
	}
	if repoNS.Valid {
		ev.RepoName = repoNS.String
	}
	if t := parseTimestamptz(observedNS); t.Valid {
		ev.ObservedAt = t.Time
	}
	if linkedNS.Valid {
		if id, err := uuid.Parse(linkedNS.String); err == nil {
			ev.LinkedDecisionID = &id
		}
	}
	if workspaceNS.Valid {
		if id, err := uuid.Parse(workspaceNS.String); err == nil {
			ev.WorkspaceID = &id
		}
	}
	return ev, nil
}
