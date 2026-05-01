package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/arch"
	"github.com/google/uuid"
)

// ArchStore is the SQLite-backed implementation of arch.StoreIface.
type ArchStore struct {
	db *DB
}

// NewArchStore wraps an open DB into an ArchStore.
func NewArchStore(d *DB) *ArchStore {
	return &ArchStore{db: d}
}

var _ arch.StoreIface = (*ArchStore)(nil)

// UpsertSnapshot inserts or updates the architecture snapshot for the given slug.
func (s *ArchStore) UpsertSnapshot(ctx context.Context, p arch.UpsertParams) (*arch.Snapshot, error) {
	if p.FileMap == nil {
		p.FileMap = map[string]string{}
	}
	fileMapJSON, err := json.Marshal(p.FileMap)
	if err != nil {
		return nil, fmt.Errorf("arch: marshaling file_map: %w", err)
	}

	id := uuid.New().String()
	now := nowRFC3339()

	const q = `
INSERT INTO project_arch (id, slug, summary, file_map, last_commit_sha, updated_at)
VALUES (?1, ?2, ?3, ?4, ?5, ?6)
ON CONFLICT (slug) DO UPDATE
  SET summary         = excluded.summary,
      file_map        = excluded.file_map,
      last_commit_sha = excluded.last_commit_sha,
      updated_at      = ?6`

	if _, err := s.db.conn.ExecContext(ctx, q, id, p.Slug, p.Summary, string(fileMapJSON), p.LastCommitSHA, now); err != nil {
		return nil, fmt.Errorf("arch: upserting snapshot for %q: %w", p.Slug, err)
	}

	return s.getBySlug(ctx, p.Slug)
}

// GetSnapshot returns the snapshot for the given slug.
func (s *ArchStore) GetSnapshot(ctx context.Context, slug string) (*arch.Snapshot, error) {
	return s.getBySlug(ctx, slug)
}

func (s *ArchStore) getBySlug(ctx context.Context, slug string) (*arch.Snapshot, error) {
	const q = `
SELECT id, slug, summary, file_map, last_commit_sha, updated_at
FROM project_arch
WHERE slug = ?1
LIMIT 1`

	var (
		snap       arch.Snapshot
		fileMapStr string
		updatedStr sql.NullString
	)
	row := s.db.conn.QueryRowContext(ctx, q, slug)
	if err := row.Scan(&snap.ID, &snap.Slug, &snap.Summary, &fileMapStr, &snap.LastCommitSHA, &updatedStr); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, arch.ErrNotFound
		}
		return nil, fmt.Errorf("arch: getting snapshot for %q: %w", slug, err)
	}
	if err := json.Unmarshal([]byte(fileMapStr), &snap.FileMap); err != nil {
		return nil, fmt.Errorf("arch: unmarshaling file_map for %q: %w", slug, err)
	}
	if updatedStr.Valid {
		if t, err := time.Parse(time.RFC3339Nano, updatedStr.String); err == nil {
			snap.UpdatedAt = t
		}
	}
	return &snap, nil
}
