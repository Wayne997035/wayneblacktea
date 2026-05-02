package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/session"
	"github.com/google/uuid"
)

// SessionStore is the SQLite-backed implementation of session.StoreIface.
type SessionStore struct {
	db *DB
}

// NewSessionStore wraps an open DB into a SessionStore.
func NewSessionStore(d *DB) *SessionStore {
	return &SessionStore{db: d}
}

var _ session.StoreIface = (*SessionStore)(nil)

const sessionHandoffsSelectCols = `id, workspace_id, project_id, repo_name, intent,
	context_summary, resolved_at, created_at`

func scanSessionHandoff(scan func(...any) error) (db.SessionHandoff, error) {
	var (
		h                                  db.SessionHandoff
		idStr                              string
		workspaceIDNS, projectIDNS, repoNS sql.NullString
		summaryNS, resolvedNS, createdNS   sql.NullString
	)
	err := scan(&idStr, &workspaceIDNS, &projectIDNS, &repoNS, &h.Intent,
		&summaryNS, &resolvedNS, &createdNS)
	if err != nil {
		return db.SessionHandoff{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		h.ID = id
	}
	h.WorkspaceID = pgtypeUUID(nsString(workspaceIDNS))
	h.ProjectID = pgtypeUUID(nsString(projectIDNS))
	h.RepoName = pgtypeText(repoNS.String, repoNS.Valid)
	h.ContextSummary = pgtypeText(summaryNS.String, summaryNS.Valid)
	h.ResolvedAt = parseTimestamptz(resolvedNS)
	h.CreatedAt = parseTimestamptz(createdNS)
	return h, nil
}

// SetHandoff records a new session handoff for the next session to pick up.
func (s *SessionStore) SetHandoff(ctx context.Context, p session.HandoffParams) (*db.SessionHandoff, error) {
	id := uuid.New()
	const q = `INSERT INTO session_handoffs
		(id, workspace_id, project_id, repo_name, intent, context_summary, created_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)`
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), nullStringFromUUID(p.ProjectID),
		nullStringIfEmpty(p.RepoName), p.Intent, nullStringIfEmpty(p.ContextSummary),
		nowRFC3339())
	if err != nil {
		return nil, errWrap("SetHandoff", err)
	}
	return s.handoffByID(ctx, id)
}

// LatestHandoff returns the most recent unresolved handoff, or session.ErrNotFound.
func (s *SessionStore) LatestHandoff(ctx context.Context) (*db.SessionHandoff, error) {
	const q = `SELECT ` + sessionHandoffsSelectCols + ` FROM session_handoffs
		WHERE resolved_at IS NULL
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, s.db.workspaceArg())
	h, err := scanSessionHandoff(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("LatestHandoff", err)
	}
	return &h, nil
}

// Resolve marks a handoff as resolved so it will not appear in future queries.
func (s *SessionStore) Resolve(ctx context.Context, id uuid.UUID) error {
	const q = `UPDATE session_handoffs
		SET resolved_at = ?2
		WHERE id = ?1
		  AND resolved_at IS NULL
		  AND (?3 IS NULL OR workspace_id = ?3)`
	res, err := s.db.conn.ExecContext(ctx, q, id.String(), nowRFC3339(), s.db.workspaceArg())
	if err != nil {
		return errWrap("Resolve", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return session.ErrNotFound
	}
	return nil
}

// UpdateSummary writes summary to the most recent unresolved handoff's
// summary_text column. Best-effort: 0 rows affected (no unresolved handoff) is
// not an error.
func (s *SessionStore) UpdateSummary(ctx context.Context, summary string) error {
	const q = `UPDATE session_handoffs
		SET summary_text = ?1
		WHERE id = (
			SELECT id FROM session_handoffs
			WHERE resolved_at IS NULL
			  AND (?2 IS NULL OR workspace_id = ?2)
			ORDER BY created_at DESC
			LIMIT 1
		)`
	_, err := s.db.conn.ExecContext(ctx, q, summary, s.db.workspaceArg())
	if err != nil {
		return errWrap("UpdateSummary", err)
	}
	return nil
}

func (s *SessionStore) handoffByID(ctx context.Context, id uuid.UUID) (*db.SessionHandoff, error) {
	const q = `SELECT ` + sessionHandoffsSelectCols + ` FROM session_handoffs
		WHERE id = ?1 LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	h, err := scanSessionHandoff(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, session.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("handoffByID", err)
	}
	return &h, nil
}
