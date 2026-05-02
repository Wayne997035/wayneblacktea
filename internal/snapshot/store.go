package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed implementation of StoreIface.
type Store struct {
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given pgxpool, optionally scoped to
// workspaceID. Pass nil for the legacy single-workspace mode.
func NewStore(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	var ws pgtype.UUID
	if workspaceID != nil {
		ws = pgtype.UUID{Bytes: [16]byte(*workspaceID), Valid: true}
	}
	return &Store{pool: pool, workspaceID: ws}
}

var _ StoreIface = (*Store)(nil)

// Write inserts a new snapshot row.
func (s *Store) Write(ctx context.Context, p WriteParams) (*Snapshot, error) {
	src := p.Source
	if src == "" {
		src = "auto-status-snapshot"
	}

	decisionIDs := p.SourceDecisionIDs
	if decisionIDs == nil {
		decisionIDs = []uuid.UUID{}
	}
	idsJSON, err := json.Marshal(decisionIDs)
	if err != nil {
		return nil, fmt.Errorf("snapshot: marshaling source_decision_ids: %w", err)
	}

	var wsID pgtype.UUID
	if p.WorkspaceID != nil {
		wsID = pgtype.UUID{Bytes: [16]byte(*p.WorkspaceID), Valid: true}
	} else {
		wsID = s.workspaceID
	}

	const q = `
INSERT INTO project_status_snapshots
    (id, slug, workspace_id, generated_at, sprint_summary, gap_analysis,
     sota_catchup_pct, pending_summary, source_decision_ids, source)
VALUES
    (gen_random_uuid(), $1, $2, NOW(), $3, $4, $5, $6, $7, $8)
RETURNING id, slug, workspace_id, generated_at, sprint_summary, gap_analysis,
          sota_catchup_pct, pending_summary, source_decision_ids, source`

	return s.scanOne(s.pool.QueryRow(ctx, q,
		p.Slug, wsID, p.SprintSummary, p.GapAnalysis,
		p.SotaCatchupPct, p.PendingSummary, idsJSON, src,
	))
}

// LatestFresh returns the newest snapshot for slug whose generated_at is
// within maxAge of now. Returns ErrNotFound when none qualifies.
func (s *Store) LatestFresh(ctx context.Context, slug string, maxAge time.Duration) (*Snapshot, error) {
	cutoff := time.Now().Add(-maxAge)

	const q = `
SELECT id, slug, workspace_id, generated_at, sprint_summary, gap_analysis,
       sota_catchup_pct, pending_summary, source_decision_ids, source
FROM project_status_snapshots
WHERE slug = $1
  AND ($2::UUID IS NULL OR workspace_id = $2)
  AND generated_at >= $3
ORDER BY generated_at DESC
LIMIT 1`

	return s.scanOne(s.pool.QueryRow(ctx, q, slug, s.workspaceID, cutoff))
}

// LatestSlugs returns distinct slugs that have at least one snapshot, scoped
// to the configured workspace.
func (s *Store) LatestSlugs(ctx context.Context) ([]string, error) {
	const q = `
SELECT DISTINCT slug
FROM project_status_snapshots
WHERE ($1::UUID IS NULL OR workspace_id = $1)
ORDER BY slug`

	rows, err := s.pool.Query(ctx, q, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("snapshot: listing slugs: %w", err)
	}
	defer rows.Close()

	var slugs []string
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("snapshot: scanning slug: %w", err)
		}
		slugs = append(slugs, slug)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("snapshot: iterating slugs: %w", err)
	}
	return slugs, nil
}

func (s *Store) scanOne(row pgx.Row) (*Snapshot, error) {
	var (
		snap       Snapshot
		wsID       pgtype.UUID
		genAt      pgtype.Timestamptz
		sprintSum  pgtype.Text
		gapAnalys  pgtype.Text
		catchupPct pgtype.Int4
		pendSum    pgtype.Text
		idsJSON    []byte
	)
	err := row.Scan(
		&snap.ID, &snap.Slug, &wsID, &genAt,
		&sprintSum, &gapAnalys, &catchupPct, &pendSum,
		&idsJSON, &snap.Source,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("snapshot: scanning row: %w", err)
	}

	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes)
		snap.WorkspaceID = &id
	}
	if genAt.Valid {
		snap.GeneratedAt = genAt.Time
	}
	snap.SprintSummary = sprintSum.String
	snap.GapAnalysis = gapAnalys.String
	snap.SotaCatchupPct = int(catchupPct.Int32)
	snap.PendingSummary = pendSum.String

	if len(idsJSON) > 0 {
		if err := json.Unmarshal(idsJSON, &snap.SourceDecisionIDs); err != nil {
			return nil, fmt.Errorf("snapshot: unmarshaling source_decision_ids: %w", err)
		}
	}
	return &snap, nil
}
