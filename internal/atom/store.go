package atom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxTraverseDepth = 5
	maxTraverseAtoms = 50
)

// Store is the Postgres-backed implementation of StoreIface.
type Store struct {
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID // zero = unscoped (single-tenant legacy mode)
}

// New returns a Store backed by the given pool, scoped to the optional workspaceID.
// nil workspaceID = legacy unscoped mode.
func New(pool *pgxpool.Pool, workspaceID *uuid.UUID) *Store {
	return &Store{pool: pool, workspaceID: toUUID(workspaceID)}
}

func toUUID(id *uuid.UUID) pgtype.UUID {
	if id == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(*id), Valid: true}
}

func jsonArray(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

func parseStringSlice(raw []byte) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	if out == nil {
		return []string{}
	}
	return out
}

const atomSelectCols = `id, workspace_id, parent_table, parent_id, content, keywords, tags, created_at`

// scanAtomRow reads a full memory_atoms row into an Atom.
func scanAtomRow(rows pgx.Rows) (*Atom, error) {
	var (
		a           Atom
		wsID        pgtype.UUID
		parentID    pgtype.UUID
		keywordsRaw []byte
		tagsRaw     []byte
		createdAt   pgtype.Timestamptz
	)
	err := rows.Scan(
		&a.ID,
		&wsID,
		&a.ParentTable,
		&parentID,
		&a.Content,
		&keywordsRaw,
		&tagsRaw,
		&createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning atom: %w", err)
	}
	if wsID.Valid {
		id := uuid.UUID(wsID.Bytes)
		a.WorkspaceID = &id
	}
	if parentID.Valid {
		a.ParentID = uuid.UUID(parentID.Bytes)
	}
	if createdAt.Valid {
		a.CreatedAt = createdAt.Time
	}
	a.Keywords = parseStringSlice(keywordsRaw)
	a.Tags = parseStringSlice(tagsRaw)
	return &a, nil
}

// scanLinkRow reads a full memory_links row into a Link.
func scanLinkRow(rows pgx.Rows) (*Link, error) {
	var (
		l         Link
		createdAt pgtype.Timestamptz
	)
	err := rows.Scan(
		&l.FromAtomID,
		&l.ToAtomID,
		&l.LinkType,
		&l.Confidence,
		&createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning link: %w", err)
	}
	if createdAt.Valid {
		l.CreatedAt = createdAt.Time
	}
	return &l, nil
}

// AddAtom inserts a new atom and returns the persisted record.
func (s *Store) AddAtom(ctx context.Context, p AddAtomParams) (*Atom, error) {
	id := uuid.New()
	keywords := jsonArray(p.Keywords)
	tags := jsonArray(p.Tags)

	const q = `
		INSERT INTO memory_atoms
			(id, workspace_id, parent_table, parent_id, content, keywords, tags)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb)
		RETURNING ` + atomSelectCols

	rows, err := s.pool.Query(ctx, q,
		id,
		toUUID(p.WorkspaceID),
		p.ParentTable,
		p.ParentID,
		p.Content,
		keywords,
		tags,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting atom: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, fmt.Errorf("inserting atom: no row returned")
	}
	a, err := scanAtomRow(rows)
	if err != nil {
		return nil, fmt.Errorf("inserting atom scan: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inserting atom rows.Err: %w", err)
	}
	return a, nil
}

// AddLink inserts a directed link. ON CONFLICT DO NOTHING so repeated calls are idempotent.
func (s *Store) AddLink(ctx context.Context, p AddLinkParams) error {
	const q = `
		INSERT INTO memory_links (from_atom_id, to_atom_id, link_type, confidence)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (from_atom_id, to_atom_id, link_type) DO NOTHING`

	_, err := s.pool.Exec(ctx, q,
		p.FromAtomID,
		p.ToAtomID,
		p.LinkType,
		p.Confidence,
	)
	if err != nil {
		return fmt.Errorf("inserting link: %w", err)
	}
	return nil
}

// ListByParent returns all atoms for a given parent table + id.
func (s *Store) ListByParent(ctx context.Context, parentTable string, parentID uuid.UUID) ([]Atom, error) {
	const q = `SELECT ` + atomSelectCols + ` FROM memory_atoms
		WHERE parent_table = $1
		  AND parent_id = $2
		ORDER BY created_at ASC`

	rows, err := s.pool.Query(ctx, q, parentTable, parentID)
	if err != nil {
		return nil, fmt.Errorf("listing atoms by parent: %w", err)
	}
	defer rows.Close()

	var out []Atom
	for rows.Next() {
		a, err := scanAtomRow(rows)
		if err != nil {
			return nil, fmt.Errorf("listing atoms by parent scan: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listing atoms by parent rows.Err: %w", err)
	}
	return out, nil
}

// Traverse performs iterative BFS from startAtomID up to min(depth, 5) hops.
// Returns all visited atoms and the links that were followed.
// Total atoms returned is capped at maxTraverseAtoms (50).
func (s *Store) Traverse(ctx context.Context, startAtomID uuid.UUID, depth int) (*TraverseResult, error) {
	if depth > maxTraverseDepth {
		depth = maxTraverseDepth
	}
	if depth < 1 {
		depth = 1
	}

	visited := make(map[uuid.UUID]bool)
	queue := []uuid.UUID{startAtomID}
	visited[startAtomID] = true

	result := &TraverseResult{
		Atoms: make([]Atom, 0),
		Links: make([]Link, 0),
	}

	for hop := 0; hop < depth && len(queue) > 0 && len(result.Atoms) < maxTraverseAtoms; hop++ {
		nextQueue, err := s.traverseHop(ctx, queue, result, visited)
		if err != nil {
			return nil, err
		}
		queue = nextQueue
	}

	// Fetch remaining queued atoms (last hop nodes we haven't fetched yet).
	for _, atomID := range queue {
		if len(result.Atoms) >= maxTraverseAtoms {
			break
		}
		a, err := s.getAtomByID(ctx, atomID)
		if err != nil {
			continue
		}
		result.Atoms = append(result.Atoms, *a)
	}

	return result, nil
}

// traverseHop processes one BFS level, returning the next-level queue.
func (s *Store) traverseHop(
	ctx context.Context,
	queue []uuid.UUID,
	result *TraverseResult,
	visited map[uuid.UUID]bool,
) ([]uuid.UUID, error) {
	nextQueue := make([]uuid.UUID, 0)
	for _, atomID := range queue {
		if len(result.Atoms) >= maxTraverseAtoms {
			break
		}
		a, err := s.getAtomByID(ctx, atomID)
		if err != nil {
			continue
		}
		result.Atoms = append(result.Atoms, *a)

		links, err := s.linksFrom(ctx, atomID)
		if err != nil {
			return nil, fmt.Errorf("traverse links from %s: %w", atomID, err)
		}
		for _, l := range links {
			result.Links = append(result.Links, l)
			if !visited[l.ToAtomID] && len(result.Atoms)+len(nextQueue) < maxTraverseAtoms {
				visited[l.ToAtomID] = true
				nextQueue = append(nextQueue, l.ToAtomID)
			}
		}
	}
	return nextQueue, nil
}

// getAtomByID fetches a single atom by primary key, scoped to the store's workspace.
// M3: workspace predicate prevents cross-tenant atom access in future multi-tenant mode.
func (s *Store) getAtomByID(ctx context.Context, id uuid.UUID) (*Atom, error) {
	const q = `SELECT ` + atomSelectCols + ` FROM memory_atoms
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		LIMIT 1`
	rows, err := s.pool.Query(ctx, q, id, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("get atom %s: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	return scanAtomRow(rows)
}

// linksFrom returns outgoing links from a given atom, filtering targets to this workspace.
// M3: JOIN ensures BFS cannot traverse into atoms owned by other workspaces.
func (s *Store) linksFrom(ctx context.Context, fromID uuid.UUID) ([]Link, error) {
	const q = `SELECT ml.from_atom_id, ml.to_atom_id, ml.link_type, ml.confidence, ml.created_at
		FROM memory_links ml
		JOIN memory_atoms ma ON ma.id = ml.to_atom_id
		WHERE ml.from_atom_id = $1
		  AND ($2::uuid IS NULL OR ma.workspace_id = $2)`
	rows, err := s.pool.Query(ctx, q, fromID, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("links from %s: %w", fromID, err)
	}
	defer rows.Close()

	var out []Link
	for rows.Next() {
		l, err := scanLinkRow(rows)
		if err != nil {
			return nil, fmt.Errorf("links from %s scan: %w", fromID, err)
		}
		out = append(out, *l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("links from %s rows.Err: %w", fromID, err)
	}
	return out, nil
}

// Search returns atoms whose content, keywords, or tags match query via ILIKE.
// Scoped to workspaceID when non-nil.
func (s *Store) Search(ctx context.Context, workspaceID *uuid.UUID, query string, limit int) ([]Atom, error) {
	if limit <= 0 {
		limit = 10
	}

	pattern := "%" + escapeLikePostgres(query) + "%"
	wsUUID := toUUID(workspaceID)

	const q = `SELECT ` + atomSelectCols + ` FROM memory_atoms
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		  AND (content ILIKE $2 ESCAPE '\' OR keywords::text ILIKE $2 ESCAPE '\' OR tags::text ILIKE $2 ESCAPE '\')
		ORDER BY created_at DESC
		LIMIT $3`

	rows, err := s.pool.Query(ctx, q, wsUUID, pattern, limit)
	if err != nil {
		return nil, fmt.Errorf("searching atoms: %w", err)
	}
	defer rows.Close()

	var out []Atom
	for rows.Next() {
		a, err := scanAtomRow(rows)
		if err != nil {
			return nil, fmt.Errorf("searching atoms scan: %w", err)
		}
		out = append(out, *a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("searching atoms rows.Err: %w", err)
	}
	return out, nil
}

// PruneAtoms hard-deletes memory_atoms rows older than cutoff. Called by the
// daily decay pruner to enforce the 90-day TTL.
// Link rows referencing pruned atoms are deleted first (no FK cascade per
// project red-line #9; referential integrity is enforced in code).
func (s *Store) PruneAtoms(ctx context.Context, cutoff time.Time) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("pruning atoms begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const deleteLinks = `
		DELETE FROM memory_links
		WHERE from_atom_id IN (SELECT id FROM memory_atoms WHERE created_at < $1)
		   OR to_atom_id   IN (SELECT id FROM memory_atoms WHERE created_at < $1)`
	if _, err := tx.Exec(ctx, deleteLinks, cutoff); err != nil {
		return 0, fmt.Errorf("pruning atom links: %w", err)
	}

	const deleteAtoms = `DELETE FROM memory_atoms WHERE created_at < $1`
	tag, err := tx.Exec(ctx, deleteAtoms, cutoff)
	if err != nil {
		return 0, fmt.Errorf("pruning atoms: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("pruning atoms commit: %w", err)
	}
	return tag.RowsAffected(), nil
}

// escapeLikePostgres escapes Postgres LIKE/ILIKE metacharacters so user-supplied
// query strings are treated as literals. Pair with ESCAPE '\' in the SQL clause.
func escapeLikePostgres(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
