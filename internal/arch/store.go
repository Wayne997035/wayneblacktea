package arch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// summaryArg/fileMapArg carry UpsertParams' patch semantics into SQL:
	// nil flows through as a bound SQL NULL, and the query below checks the
	// raw parameter (`$N IS NULL`), not EXCLUDED.<col> (which is never NULL
	// — the VALUES-clause COALESCE already defaulted it for the INSERT
	// branch). That is what lets "caller omitted the field" resolve to
	// "keep the stored value" on UPDATE while still giving a brand-new row
	// a valid zero-value default. Previously only file_map had this
	// protection; summary got it in security review PR #157 round 3
	// (stringArg folding "absent" and "present but empty" together).
	//
	// last_commit_sha does NOT get this protection (m-R11, GTD 25537a73,
	// decision 0d1a41fc): see UpsertParams.LastCommitSHA's doc comment in
	// arch.go for the full reasoning — in short, this column has no
	// self-healing write path other than "the next upsert overwrites it",
	// and the mandatory protocol write-back list (mcpInstructions,
	// server.go) never includes it, so patch semantics here meant an
	// injected value could never be forced clean. shaArg is still built
	// with the same nil-passthrough shape as the other two (for symmetry
	// and so the INSERT-branch COALESCE default still works), but the
	// UPDATE branch below writes EXCLUDED.last_commit_sha unconditionally,
	// with no CASE WHEN guard.
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
		fileMapArg = b
	}
	var shaArg any
	if p.LastCommitSHA != nil {
		shaArg = *p.LastCommitSHA
	}

	// priorSHA is read BEFORE the write, purely for the diagnostic warning
	// below — it never gates the write decision (m-R11 dispatch point ④:
	// no shape-based heuristics deciding whether to overwrite). Best-effort:
	// a missing row or a read error just means priorSHA stays "" and no
	// warning fires; the upsert itself still proceeds unconditionally.
	var priorSHA string
	if p.LastCommitSHA == nil {
		if prior, err := s.GetSnapshot(ctx, p.Slug); err == nil {
			priorSHA = prior.LastCommitSHA
		}
	}

	const q = `
INSERT INTO project_arch (id, slug, summary, file_map, last_commit_sha, updated_at)
VALUES ($1, $2, COALESCE($3, ''), COALESCE($4::jsonb, '{}'::jsonb), COALESCE($5, ''), NOW())
ON CONFLICT (slug) DO UPDATE
  SET summary         = CASE WHEN $3 IS NULL THEN project_arch.summary ELSE EXCLUDED.summary END,
      file_map        = CASE WHEN $4::jsonb IS NULL THEN project_arch.file_map ELSE EXCLUDED.file_map END,
      last_commit_sha = EXCLUDED.last_commit_sha,
      updated_at      = NOW()
RETURNING id, slug, summary, file_map, last_commit_sha, updated_at`

	id := uuid.New()

	var (
		snap        Snapshot
		fileMapRaw  []byte
		updatedAtPG time.Time
	)
	row := s.pool.QueryRow(ctx, q, id, p.Slug, summaryArg, fileMapArg, shaArg)
	if err := row.Scan(&snap.ID, &snap.Slug, &snap.Summary, &fileMapRaw, &snap.LastCommitSHA, &updatedAtPG); err != nil {
		return nil, fmt.Errorf("arch: upserting snapshot for %q: %w", p.Slug, err)
	}
	snap.UpdatedAt = updatedAtPG
	if err := json.Unmarshal(fileMapRaw, &snap.FileMap); err != nil {
		return nil, fmt.Errorf("arch: unmarshaling file_map for %q: %w", p.Slug, err)
	}

	// Observability for the accepted trade-off of unconditional overwrite:
	// this call just cleared a non-empty last_commit_sha to "" because the
	// caller omitted the field entirely. That is by design (self-healing a
	// poisoned value), but it is also how a legitimately hand-maintained,
	// non-git-HEAD value in this column (e.g. a multi-repo `name:sha`
	// convention) would silently disappear on the next routine call. Never
	// gates the write (see comment above); only makes the loss observable.
	if p.LastCommitSHA == nil && priorSHA != "" {
		slog.Warn("arch: last_commit_sha auto-cleared by upsert that omitted it",
			"slug", p.Slug, "prior_len", len(priorSHA))
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
