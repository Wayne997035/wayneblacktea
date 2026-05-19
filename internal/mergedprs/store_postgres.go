package mergedprs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the Postgres-backed implementation of Store.
type PgStore struct {
	pool        *pgxpool.Pool
	workspaceID *uuid.UUID
}

// NewPgStore creates a Postgres-backed Store. workspaceID may be nil for
// legacy unscoped mode (in which case every row is matched).
func NewPgStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *PgStore {
	return &PgStore{pool: pool, workspaceID: workspaceID}
}

// Compile-time guarantee.
var _ Store = (*PgStore)(nil)

func (s *PgStore) wsArg() any {
	if s.workspaceID == nil {
		return nil
	}
	return s.workspaceID
}

// Upsert inserts a row keyed by URL. On conflict the row is refreshed so the
// observed_at moves forward (used by reconcile replay to "saw it again now").
func (s *PgStore) Upsert(ctx context.Context, p UpsertParams) error {
	if p.URL == "" {
		return errors.New("mergedprs.PgStore.Upsert: url is required")
	}
	if p.Repo == "" {
		return errors.New("mergedprs.PgStore.Upsert: repo is required")
	}
	ws := p.WorkspaceID
	if ws == nil {
		ws = s.workspaceID
	}
	var wsArg any
	if ws != nil {
		wsArg = ws
	}
	merged := p.MergedAt
	if merged.IsZero() {
		merged = time.Now().UTC()
	}

	const q = `INSERT INTO merged_prs_observed
		(workspace_id, repo, url, head_ref, title, body_excerpt, merged_at)
		VALUES ($1, $2, $3, NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), $7)
		ON CONFLICT (url) DO UPDATE SET
			observed_at  = NOW(),
			repo         = EXCLUDED.repo,
			head_ref     = EXCLUDED.head_ref,
			title        = EXCLUDED.title,
			body_excerpt = EXCLUDED.body_excerpt,
			merged_at    = EXCLUDED.merged_at,
			workspace_id = COALESCE(EXCLUDED.workspace_id, merged_prs_observed.workspace_id)`
	if _, err := s.pool.Exec(ctx, q,
		wsArg, p.Repo, p.URL, p.HeadRef, p.Title, p.BodyExcerpt, merged,
	); err != nil {
		return fmt.Errorf("mergedprs.PgStore.Upsert: %w", err)
	}
	return nil
}

// RecentObservedSince returns rows with observed_at >= since.
func (s *PgStore) RecentObservedSince(
	ctx context.Context,
	since time.Time,
	workspaceID *uuid.UUID,
) ([]Observed, error) {
	ws := s.wsArg()
	if workspaceID != nil {
		ws = workspaceID
	}
	const q = `SELECT id::text, workspace_id::text, repo, url, head_ref, title,
		body_excerpt, merged_at, observed_at
		FROM merged_prs_observed
		WHERE observed_at >= $1
		  AND ($2::uuid IS NULL OR workspace_id = $2::uuid)
		ORDER BY observed_at DESC`
	rows, err := s.pool.Query(ctx, q, since, ws)
	if err != nil {
		return nil, fmt.Errorf("mergedprs.PgStore.RecentObservedSince: %w", err)
	}
	defer rows.Close()

	out := make([]Observed, 0)
	for rows.Next() {
		var (
			idStr           string
			wsIDStr         *string
			repo, url       string
			headRef, title  *string
			bodyExcerpt     *string
			mergedAt, obsAt time.Time
		)
		if err := rows.Scan(&idStr, &wsIDStr, &repo, &url, &headRef, &title,
			&bodyExcerpt, &mergedAt, &obsAt); err != nil {
			return nil, fmt.Errorf("mergedprs.PgStore.RecentObservedSince scan: %w", err)
		}
		o := Observed{
			Repo:       repo,
			URL:        url,
			MergedAt:   mergedAt.UTC(),
			ObservedAt: obsAt.UTC(),
		}
		o.ID, _ = uuid.Parse(idStr)
		if wsIDStr != nil {
			if id, e := uuid.Parse(*wsIDStr); e == nil {
				o.WorkspaceID = &id
			}
		}
		if headRef != nil {
			o.HeadRef = *headRef
		}
		if title != nil {
			o.Title = *title
		}
		if bodyExcerpt != nil {
			o.BodyExcerpt = *bodyExcerpt
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mergedprs.PgStore.RecentObservedSince iter: %w", err)
	}
	return out, nil
}

// PruneOlderThan deletes rows older than olderThan ago. The duration is
// converted to a parameterised timestamp (NOT interpolated into SQL) so the
// query plan stays stable and there's zero injection surface.
func (s *PgStore) PruneOlderThan(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	const q = `DELETE FROM merged_prs_observed WHERE observed_at < $1`
	tag, err := s.pool.Exec(ctx, q, cutoff)
	if err != nil {
		return 0, fmt.Errorf("mergedprs.PgStore.PruneOlderThan: %w", err)
	}
	return tag.RowsAffected(), nil
}
