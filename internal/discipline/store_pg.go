package discipline

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the Postgres-backed implementation of Store.
type PgStore struct {
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID // zero = unscoped (legacy single-tenant mode)
}

// NewPgStore returns a Postgres-backed Store scoped to workspaceID.
// workspaceID nil = unscoped writes (workspace_id is NULL in DB).
func NewPgStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *PgStore {
	return &PgStore{pool: pool, workspaceID: toPgUUID(workspaceID)}
}

// Compile-time guarantee that PgStore satisfies the Store interface.
var _ Store = (*PgStore)(nil)

func toPgUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

// Insert records a single discipline event. WorkspaceID falls back to the
// store's configured workspace when the param value is nil — matching the
// scoping pattern other domain stores use.
func (s *PgStore) Insert(ctx context.Context, p InsertParams) error {
	wsID := toPgUUID(p.WorkspaceID)
	if !wsID.Valid {
		wsID = s.workspaceID
	}
	repoArg := pgtype.Text{}
	if p.RepoName != "" {
		repoArg = pgtype.Text{String: p.RepoName, Valid: true}
	}
	linked := toPgUUID(p.LinkedDecisionID)

	const q = `
		INSERT INTO discipline_events
			(session_id, repo_name, tool_name, is_mutating, linked_decision_id, workspace_id)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := s.pool.Exec(ctx, q,
		p.SessionID,
		repoArg,
		p.ToolName,
		p.IsMutating,
		linked,
		wsID,
	); err != nil {
		return fmt.Errorf("inserting discipline event: %w", err)
	}
	return nil
}

const eventSelectCols = `id, session_id, repo_name, tool_name, is_mutating, observed_at, linked_decision_id, workspace_id`

// RecentMutating returns mutating events from the workspace observed at or
// after `since`, newest first, capped at limit.
func (s *PgStore) RecentMutating(ctx context.Context, since time.Time, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `SELECT ` + eventSelectCols + ` FROM discipline_events
		WHERE is_mutating = TRUE
		  AND observed_at >= $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY observed_at DESC
		LIMIT $3`
	rows, err := s.pool.Query(ctx, q, since, s.workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent mutating: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		ev, err := scanEventRow(rows)
		if err != nil {
			return nil, fmt.Errorf("query recent mutating scan: %w", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query recent mutating rows.Err: %w", err)
	}
	return out, nil
}

// RecentDecisionTimes returns observed_at timestamps of log_decision /
// confirm_plan events in the given session at or after `since`, newest first.
// We cap the row count at 200 to bound worst-case load even if a single
// session has thousands of decisions.
func (s *PgStore) RecentDecisionTimes(ctx context.Context, sessionID string, since time.Time) ([]time.Time, error) {
	const q = `SELECT observed_at FROM discipline_events
		WHERE session_id = $1
		  AND tool_name IN ('log_decision', 'confirm_plan')
		  AND observed_at >= $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY observed_at DESC
		LIMIT 200`
	rows, err := s.pool.Query(ctx, q, sessionID, since, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query recent decision times: %w", err)
	}
	defer rows.Close()

	var out []time.Time
	for rows.Next() {
		var ts pgtype.Timestamptz
		if err := rows.Scan(&ts); err != nil {
			return nil, fmt.Errorf("query recent decision times scan: %w", err)
		}
		if ts.Valid {
			out = append(out, ts.Time)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query recent decision times rows.Err: %w", err)
	}
	return out, nil
}

func scanEventRow(rows pgx.Rows) (Event, error) {
	var (
		ev         Event
		repoName   pgtype.Text
		observedAt pgtype.Timestamptz
		linked     pgtype.UUID
		wsID       pgtype.UUID
	)
	err := rows.Scan(
		&ev.ID,
		&ev.SessionID,
		&repoName,
		&ev.ToolName,
		&ev.IsMutating,
		&observedAt,
		&linked,
		&wsID,
	)
	if err != nil {
		return Event{}, fmt.Errorf("scanning discipline event: %w", err)
	}
	if repoName.Valid {
		ev.RepoName = repoName.String
	}
	if observedAt.Valid {
		ev.ObservedAt = observedAt.Time
	}
	if linked.Valid {
		id := uuid.UUID(linked.Bytes)
		ev.LinkedDecisionID = &id
	}
	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes)
		ev.WorkspaceID = &id
	}
	return ev, nil
}
