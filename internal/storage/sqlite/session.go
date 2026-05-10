package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
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
// ORDER BY created_at DESC, rowid DESC: rowid is SQLite's monotonically-increasing
// internal row counter and acts as a stable tiebreaker when two rows share the same
// created_at millisecond (possible after the timestamp format change to .000).
func (s *SessionStore) LatestHandoff(ctx context.Context) (*db.SessionHandoff, error) {
	const q = `SELECT ` + sessionHandoffsSelectCols + ` FROM session_handoffs
		WHERE resolved_at IS NULL
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC, rowid DESC
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

// UpdateEmbeddingByID writes the embedding bytes to the session handoff with
// the given ID (SQLite version), scoped to workspace_id.  Best-effort.
func (s *SessionStore) UpdateEmbeddingByID(ctx context.Context, id uuid.UUID, embedding []byte) error {
	const q = `UPDATE session_handoffs SET embedding = ?1
		WHERE id = ?2
		  AND (?3 IS NULL OR workspace_id = ?3)`
	_, err := s.db.conn.ExecContext(ctx, q, embedding, id.String(), s.db.workspaceArg())
	if err != nil {
		return errWrap("UpdateEmbeddingByID", err)
	}
	return nil
}

// UpdateEmbedding writes the embedding bytes to the most recent unresolved
// session handoff (SQLite version, uses BLOB column).  Best-effort.
func (s *SessionStore) UpdateEmbedding(ctx context.Context, embedding []byte) error {
	const q = `UPDATE session_handoffs
		SET embedding = ?1
		WHERE id = (
			SELECT id FROM session_handoffs
			WHERE resolved_at IS NULL
			  AND (?2 IS NULL OR workspace_id = ?2)
			ORDER BY created_at DESC
			LIMIT 1
		)`
	_, err := s.db.conn.ExecContext(ctx, q, embedding, s.db.workspaceArg())
	if err != nil {
		return errWrap("UpdateEmbedding", err)
	}
	return nil
}

// SearchByCosine returns the top-limit session handoffs most similar to
// queryEmbedding.  SQLite has no pgvector — brute-force Go-side cosine scan.
//
// SECURITY: filtered by workspace_id — no cross-workspace data returned.
func (s *SessionStore) SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.SessionHandoff, error) {
	if len(queryEmbedding) == 0 || limit <= 0 {
		return nil, nil
	}
	// Fetch up to 200 recent handoffs that have an embedding.
	const q = `SELECT id, intent, summary_text, embedding FROM session_handoffs
		WHERE embedding IS NOT NULL
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC
		LIMIT 200`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg())
	if err != nil {
		return nil, errWrap("SearchByCosine", err)
	}
	defer func() { _ = rows.Close() }()

	type scored struct {
		h   db.SessionHandoff
		sim float64
	}
	var candidates []scored
	for rows.Next() {
		var idStr string
		var intent string
		var summaryNS sql.NullString
		var rawEmbed []byte
		if err := rows.Scan(&idStr, &intent, &summaryNS, &rawEmbed); err != nil {
			continue
		}
		vec := localai.DeserializeEmbedding(rawEmbed)
		if vec == nil {
			continue
		}
		h := db.SessionHandoff{Intent: intent}
		if id, parseErr := uuid.Parse(idStr); parseErr == nil {
			h.ID = id
		}
		if summaryNS.Valid {
			h.ContextSummary = pgtypeText(summaryNS.String, true)
		}
		candidates = append(candidates, scored{h: h, sim: localai.CosineSimilarity(queryEmbedding, vec)})
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("SearchByCosine iter", err)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].sim > candidates[j].sim
	})
	if limit < len(candidates) {
		candidates = candidates[:limit]
	}
	result := make([]db.SessionHandoff, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, c.h)
	}
	return result, nil
}

// HandoffsByRepo returns recent handoffs whose repo_name matches repoName,
// scoped to the configured workspace, newest first. SQLite parity with
// session.Store.HandoffsByRepo.
func (s *SessionStore) HandoffsByRepo(ctx context.Context, repoName string, limit int) ([]db.SessionHandoff, error) {
	const q = `SELECT ` + sessionHandoffsSelectCols + ` FROM session_handoffs
		WHERE repo_name = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, repoName, s.db.workspaceArg(), limit)
	if err != nil {
		return nil, errWrap("HandoffsByRepo", err)
	}
	defer func() { _ = rows.Close() }()

	var out []db.SessionHandoff
	for rows.Next() {
		h, err := scanSessionHandoff(rows.Scan)
		if err != nil {
			return nil, errWrap("HandoffsByRepo scan", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("HandoffsByRepo iter", err)
	}
	return out, nil
}

// HandoffsSince returns handoffs created or resolved on or after since,
// scoped to the configured workspace. Used by the timeline aggregator.
func (s *SessionStore) HandoffsSince(ctx context.Context, since time.Time, limit int) ([]db.SessionHandoff, error) {
	sinceStr := since.UTC().Format(sqliteMillisLayout)
	const q = `SELECT ` + sessionHandoffsSelectCols + ` FROM session_handoffs
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND (created_at >= ?2 OR (resolved_at IS NOT NULL AND resolved_at >= ?2))
		ORDER BY created_at DESC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, s.db.workspaceArg(), sinceStr, limit)
	if err != nil {
		return nil, errWrap("HandoffsSince", err)
	}
	defer func() { _ = rows.Close() }()

	var out []db.SessionHandoff
	for rows.Next() {
		h, err := scanSessionHandoff(rows.Scan)
		if err != nil {
			return nil, errWrap("HandoffsSince scan", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("HandoffsSince iter", err)
	}
	return out, nil
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
