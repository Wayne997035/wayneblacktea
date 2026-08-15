package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// summaryArg/fileMapArg mirror the Postgres store's patch semantics
	// (see internal/arch/store.go): nil binds as SQL NULL, which the query
	// below checks directly (?N IS NULL), not excluded.<col> (always
	// non-NULL after the VALUES-clause COALESCE). That resolves "caller
	// omitted the field" to "keep the stored value" on the ON CONFLICT
	// branch, while still defaulting a brand-new row to its zero value.
	// Previously only file_map had this protection; see arch.UpsertParams
	// doc comment for why summary now does too (security review PR #157
	// round 3).
	//
	// last_commit_sha does NOT get this protection (m-R11, GTD 25537a73,
	// decision 0d1a41fc) — see arch.UpsertParams.LastCommitSHA's doc
	// comment and the Postgres store's mirrored comment for the reasoning.
	// shaArg keeps the same nil-passthrough shape as the other two (so the
	// INSERT-branch COALESCE default still applies to a brand-new row),
	// but the ON CONFLICT branch below writes excluded.last_commit_sha
	// unconditionally, with no CASE WHEN guard.
	var summaryArg any
	if p.Summary != nil {
		summaryArg = *p.Summary
	}
	var fileMapArg any
	if p.FileMap != nil {
		b, err := json.Marshal(*p.FileMap)
		if err != nil {
			return nil, fmt.Errorf("arch: marshaling file_map: %w", err)
		}
		fileMapArg = string(b)
	}
	var shaArg any
	if p.LastCommitSHA != nil {
		shaArg = *p.LastCommitSHA
	}

	// priorSHA is read BEFORE the write, purely for the diagnostic warning
	// below (mirrors internal/arch/store.go) — it never gates the write
	// decision. Best-effort: a missing row or read error leaves priorSHA
	// "" and no warning fires; the upsert still proceeds unconditionally.
	var priorSHA string
	if p.LastCommitSHA == nil {
		if prior, err := s.getBySlug(ctx, p.Slug); err == nil {
			priorSHA = prior.LastCommitSHA
		}
	}

	id := uuid.New().String()
	now := nowRFC3339()

	const q = `
INSERT INTO project_arch (id, slug, summary, file_map, last_commit_sha, updated_at)
VALUES (?1, ?2, COALESCE(?3, ''), COALESCE(?4, '{}'), COALESCE(?5, ''), ?6)
ON CONFLICT (slug) DO UPDATE
  SET summary         = CASE WHEN ?3 IS NULL THEN summary ELSE excluded.summary END,
      file_map        = CASE WHEN ?4 IS NULL THEN file_map ELSE excluded.file_map END,
      last_commit_sha = excluded.last_commit_sha,
      updated_at      = ?6`

	if _, err := s.db.conn.ExecContext(ctx, q, id, p.Slug, summaryArg, fileMapArg, shaArg, now); err != nil {
		return nil, fmt.Errorf("arch: upserting snapshot for %q: %w", p.Slug, err)
	}

	snap, err := s.getBySlug(ctx, p.Slug)
	if err != nil {
		return nil, err
	}

	// Observability for the accepted trade-off of unconditional overwrite —
	// see the mirrored comment in internal/arch/store.go for the full
	// rationale. Never gates the write; only makes the loss observable.
	if p.LastCommitSHA == nil && priorSHA != "" {
		slog.Warn("arch: last_commit_sha auto-cleared by upsert that omitted it",
			"slug", p.Slug, "prior_len", len(priorSHA))
	}

	return snap, nil
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
