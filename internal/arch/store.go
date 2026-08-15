package arch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the Postgres-backed implementation of StoreIface.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store backed by the given pgxpool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var _ StoreIface = (*Store)(nil)

// UpsertSnapshot inserts or updates the architecture snapshot for the given slug.
func (s *Store) UpsertSnapshot(ctx context.Context, p UpsertParams) (*Snapshot, error) {
	// fileMapArg carries UpsertParams.FileMap's patch semantics into SQL.
	// nil flows through as a bound SQL NULL for $4; the query below checks
	// the raw parameter ($4::jsonb IS NULL), not EXCLUDED.file_map (which
	// is never NULL — the VALUES-clause COALESCE already defaulted it to
	// {} for the INSERT branch). That is what lets "caller omitted
	// file_map" resolve to "keep the stored value" on UPDATE while still
	// giving a brand-new row a valid {} default.
	var fileMapArg any
	if p.FileMap != nil {
		b, err := json.Marshal(*p.FileMap)
		if err != nil {
			return nil, fmt.Errorf("arch: marshaling file_map: %w", err)
		}
		fileMapArg = b
	}

	const q = `
INSERT INTO project_arch (id, slug, summary, file_map, last_commit_sha, updated_at)
VALUES ($1, $2, $3, COALESCE($4::jsonb, '{}'::jsonb), $5, NOW())
ON CONFLICT (slug) DO UPDATE
  SET summary         = EXCLUDED.summary,
      file_map        = CASE WHEN $4::jsonb IS NULL THEN project_arch.file_map ELSE EXCLUDED.file_map END,
      last_commit_sha = EXCLUDED.last_commit_sha,
      updated_at      = NOW()
RETURNING id, slug, summary, file_map, last_commit_sha, updated_at`

	id := uuid.New()
	sha := p.LastCommitSHA

	var (
		snap        Snapshot
		fileMapRaw  []byte
		updatedAtPG time.Time
	)
	row := s.pool.QueryRow(ctx, q, id, p.Slug, p.Summary, fileMapArg, sha)
	if err := row.Scan(&snap.ID, &snap.Slug, &snap.Summary, &fileMapRaw, &snap.LastCommitSHA, &updatedAtPG); err != nil {
		return nil, fmt.Errorf("arch: upserting snapshot for %q: %w", p.Slug, err)
	}
	snap.UpdatedAt = updatedAtPG
	if err := json.Unmarshal(fileMapRaw, &snap.FileMap); err != nil {
		return nil, fmt.Errorf("arch: unmarshaling file_map for %q: %w", p.Slug, err)
	}
	return &snap, nil
}

// GetSnapshot returns the snapshot for the given slug.
func (s *Store) GetSnapshot(ctx context.Context, slug string) (*Snapshot, error) {
	const q = `
SELECT id, slug, summary, file_map, last_commit_sha, updated_at
FROM project_arch
WHERE slug = $1
LIMIT 1`

	var (
		snap        Snapshot
		fileMapRaw  []byte
		updatedAtPG time.Time
	)
	row := s.pool.QueryRow(ctx, q, slug)
	if err := row.Scan(&snap.ID, &snap.Slug, &snap.Summary, &fileMapRaw, &snap.LastCommitSHA, &updatedAtPG); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("arch: getting snapshot for %q: %w", slug, err)
	}
	snap.UpdatedAt = updatedAtPG
	if err := json.Unmarshal(fileMapRaw, &snap.FileMap); err != nil {
		return nil, fmt.Errorf("arch: unmarshaling file_map for %q: %w", slug, err)
	}
	return &snap, nil
}
