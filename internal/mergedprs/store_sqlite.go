package mergedprs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// SQLiteStore is the SQLite-backed implementation of Store.
type SQLiteStore struct {
	db          *sql.DB
	workspaceID string // empty = unscoped (legacy single-tenant)
}

// NewSQLiteStore creates a SQLiteStore.
func NewSQLiteStore(db *sql.DB, workspaceID string) *SQLiteStore {
	return &SQLiteStore{db: db, workspaceID: workspaceID}
}

// Compile-time guarantee.
var _ Store = (*SQLiteStore)(nil)

func nowRFC3339() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// Upsert mirrors the PG semantics — primary key is url (UNIQUE INDEX) so
// ON CONFLICT (url) updates the existing row in place.
func (s *SQLiteStore) Upsert(ctx context.Context, p UpsertParams) error {
	if p.URL == "" {
		return errors.New("mergedprs.SQLiteStore.Upsert: url is required")
	}
	if p.Repo == "" {
		return errors.New("mergedprs.SQLiteStore.Upsert: repo is required")
	}
	wsStr := s.workspaceID
	if p.WorkspaceID != nil {
		wsStr = p.WorkspaceID.String()
	}
	var wsArg any
	if wsStr != "" {
		wsArg = wsStr
	}
	merged := p.MergedAt
	if merged.IsZero() {
		merged = time.Now().UTC()
	}
	mergedStr := merged.UTC().Format("2006-01-02T15:04:05.000Z07:00")

	var headRefArg, titleArg, bodyExcerptArg any
	if p.HeadRef != "" {
		headRefArg = p.HeadRef
	}
	if p.Title != "" {
		titleArg = p.Title
	}
	if p.BodyExcerpt != "" {
		bodyExcerptArg = p.BodyExcerpt
	}

	id := uuid.New().String()
	// Requires SQLite >= 3.24 (released 2018-06) for ON CONFLICT DO UPDATE
	// UPSERT syntax. modernc.org/sqlite bundles SQLite 3.45+ so production
	// is guaranteed; tests use the same driver.
	const q = `INSERT INTO merged_prs_observed
		(id, workspace_id, repo, url, head_ref, title, body_excerpt, merged_at, observed_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9)
		ON CONFLICT (url) DO UPDATE SET
			observed_at  = excluded.observed_at,
			repo         = excluded.repo,
			head_ref     = excluded.head_ref,
			title        = excluded.title,
			body_excerpt = excluded.body_excerpt,
			merged_at    = excluded.merged_at,
			workspace_id = COALESCE(excluded.workspace_id, merged_prs_observed.workspace_id)`
	if _, err := s.db.ExecContext(ctx, q,
		id, wsArg, p.Repo, p.URL, headRefArg, titleArg, bodyExcerptArg, mergedStr, nowRFC3339(),
	); err != nil {
		return fmt.Errorf("mergedprs.SQLiteStore.Upsert: %w", err)
	}
	return nil
}

// RecentObservedSince returns rows whose observed_at >= since.
func (s *SQLiteStore) RecentObservedSince(
	ctx context.Context,
	since time.Time,
	workspaceID *uuid.UUID,
) ([]Observed, error) {
	wsStr := s.workspaceID
	if workspaceID != nil {
		wsStr = workspaceID.String()
	}
	var wsArg any
	if wsStr != "" {
		wsArg = wsStr
	}
	cutoff := since.UTC().Format(time.RFC3339Nano)

	const q = `SELECT id, workspace_id, repo, url, head_ref, title,
		body_excerpt, merged_at, observed_at
		FROM merged_prs_observed
		WHERE observed_at >= ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY observed_at DESC`
	rows, err := s.db.QueryContext(ctx, q, cutoff, wsArg)
	if err != nil {
		return nil, fmt.Errorf("mergedprs.SQLiteStore.RecentObservedSince: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]Observed, 0)
	for rows.Next() {
		var (
			idStr, repo, url, mergedAtStr, obsAtStr string
			wsIDNS, headRefNS, titleNS, bodyNS      sql.NullString
		)
		if err := rows.Scan(&idStr, &wsIDNS, &repo, &url, &headRefNS, &titleNS,
			&bodyNS, &mergedAtStr, &obsAtStr); err != nil {
			return nil, fmt.Errorf("mergedprs.SQLiteStore.RecentObservedSince scan: %w", err)
		}
		o := Observed{
			Repo:       repo,
			URL:        url,
			MergedAt:   parseTime(mergedAtStr),
			ObservedAt: parseTime(obsAtStr),
		}
		o.ID, _ = uuid.Parse(idStr)
		if wsIDNS.Valid && wsIDNS.String != "" {
			if id, e := uuid.Parse(wsIDNS.String); e == nil {
				o.WorkspaceID = &id
			}
		}
		if headRefNS.Valid {
			o.HeadRef = headRefNS.String
		}
		if titleNS.Valid {
			o.Title = titleNS.String
		}
		if bodyNS.Valid {
			o.BodyExcerpt = bodyNS.String
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mergedprs.SQLiteStore.RecentObservedSince iter: %w", err)
	}
	return out, nil
}

// PruneOlderThan deletes rows older than olderThan ago. cutoff is computed
// in Go and passed as a parameter — no string interpolation, no injection.
func (s *SQLiteStore) PruneOlderThan(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	const q = `DELETE FROM merged_prs_observed WHERE observed_at < ?1`
	res, err := s.db.ExecContext(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("mergedprs.SQLiteStore.PruneOlderThan: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
