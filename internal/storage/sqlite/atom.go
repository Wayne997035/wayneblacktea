package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wayne997035/wayneblacktea/internal/atom"
	"github.com/google/uuid"
)

// AtomStore is the SQLite-backed implementation of atom.StoreIface.
type AtomStore struct {
	db *DB
}

// NewAtomStore wraps an open DB into an AtomStore.
func NewAtomStore(d *DB) *AtomStore {
	return &AtomStore{db: d}
}

// Compile-time guarantee against drift from atom.StoreIface.
var _ atom.StoreIface = (*AtomStore)(nil)

const atomSelectCols = `id, workspace_id, parent_table, parent_id, content, keywords, tags, created_at`

const (
	maxTraverseDepth = 5
	maxTraverseAtoms = 50
)

type atomRawRow struct {
	idStr       string
	wsIDNS      sql.NullString
	parentTable string
	parentIDStr string
	content     string
	keywordsStr string
	tagsStr     string
	createdNS   sql.NullString
}

func scanAtomRow(scan func(...any) error) (atom.Atom, error) {
	var r atomRawRow
	err := scan(
		&r.idStr,
		&r.wsIDNS,
		&r.parentTable,
		&r.parentIDStr,
		&r.content,
		&r.keywordsStr,
		&r.tagsStr,
		&r.createdNS,
	)
	if err != nil {
		return atom.Atom{}, err
	}
	return parseAtomRow(r), nil
}

func parseAtomRow(r atomRawRow) atom.Atom {
	a := atom.Atom{
		ParentTable: r.parentTable,
		Content:     r.content,
	}
	if id, err := uuid.Parse(r.idStr); err == nil {
		a.ID = id
	}
	if r.wsIDNS.Valid {
		if id, err := uuid.Parse(r.wsIDNS.String); err == nil {
			a.WorkspaceID = &id
		}
	}
	if id, err := uuid.Parse(r.parentIDStr); err == nil {
		a.ParentID = id
	}
	if t := parseTimestamptz(r.createdNS); t.Valid {
		a.CreatedAt = t.Time
	}
	a.Keywords = parseStringSliceSQLite(r.keywordsStr)
	a.Tags = parseStringSliceSQLite(r.tagsStr)
	return a
}

// AddAtom inserts a new atom and returns the persisted record.
func (s *AtomStore) AddAtom(ctx context.Context, p atom.AddAtomParams) (*atom.Atom, error) {
	id := uuid.New()
	keywords := encodeStrings(p.Keywords)
	tags := encodeStrings(p.Tags)
	now := nowRFC3339()

	const q = `INSERT INTO memory_atoms
		(id, workspace_id, parent_table, parent_id, content, keywords, tags, created_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)`
	_, err := s.db.conn.ExecContext(ctx, q,
		id.String(),
		s.db.workspaceArg(),
		p.ParentTable,
		p.ParentID.String(),
		p.Content,
		keywords,
		tags,
		now,
	)
	if err != nil {
		return nil, errWrap("AtomStore.AddAtom", err)
	}
	return s.getByID(ctx, id)
}

// AddLink inserts a directed link. INSERT OR IGNORE makes it idempotent.
func (s *AtomStore) AddLink(ctx context.Context, p atom.AddLinkParams) error {
	const q = `INSERT OR IGNORE INTO memory_links
		(from_atom_id, to_atom_id, link_type, confidence)
		VALUES (?1, ?2, ?3, ?4)`
	_, err := s.db.conn.ExecContext(ctx, q,
		p.FromAtomID.String(),
		p.ToAtomID.String(),
		p.LinkType,
		p.Confidence,
	)
	if err != nil {
		return errWrap("AtomStore.AddLink", err)
	}
	return nil
}

// ListByParent returns all atoms for a given parent table + id.
func (s *AtomStore) ListByParent(ctx context.Context, parentTable string, parentID uuid.UUID) ([]atom.Atom, error) {
	const q = `SELECT ` + atomSelectCols + ` FROM memory_atoms
		WHERE parent_table = ?1
		  AND parent_id = ?2
		ORDER BY created_at ASC`
	rows, err := s.db.conn.QueryContext(ctx, q, parentTable, parentID.String())
	if err != nil {
		return nil, errWrap("AtomStore.ListByParent", err)
	}
	defer func() { _ = rows.Close() }()

	var out []atom.Atom
	for rows.Next() {
		a, err := scanAtomRow(rows.Scan)
		if err != nil {
			return nil, errWrap("AtomStore.ListByParent scan", err)
		}
		out = append(out, a)
	}
	return out, errWrap("AtomStore.ListByParent iter", rows.Err())
}

// Traverse performs iterative BFS from startAtomID up to min(depth, 5) hops.
func (s *AtomStore) Traverse(ctx context.Context, startAtomID uuid.UUID, depth int) (*atom.TraverseResult, error) {
	if depth > maxTraverseDepth {
		depth = maxTraverseDepth
	}
	if depth < 1 {
		depth = 1
	}

	visited := make(map[uuid.UUID]bool)
	queue := []uuid.UUID{startAtomID}
	visited[startAtomID] = true

	result := &atom.TraverseResult{
		Atoms: make([]atom.Atom, 0),
		Links: make([]atom.Link, 0),
	}

	for hop := 0; hop < depth && len(queue) > 0 && len(result.Atoms) < maxTraverseAtoms; hop++ {
		nextQueue, err := s.traverseHop(ctx, queue, result, visited)
		if err != nil {
			return nil, err
		}
		queue = nextQueue
	}

	// Fetch remaining queued atoms from last hop.
	for _, atomID := range queue {
		if len(result.Atoms) >= maxTraverseAtoms {
			break
		}
		a, err := s.getByID(ctx, atomID)
		if err != nil {
			continue
		}
		result.Atoms = append(result.Atoms, *a)
	}

	return result, nil
}

// traverseHop processes one BFS level, returning the next-level queue.
func (s *AtomStore) traverseHop(
	ctx context.Context,
	queue []uuid.UUID,
	result *atom.TraverseResult,
	visited map[uuid.UUID]bool,
) ([]uuid.UUID, error) {
	nextQueue := make([]uuid.UUID, 0)
	for _, atomID := range queue {
		if len(result.Atoms) >= maxTraverseAtoms {
			break
		}
		a, err := s.getByID(ctx, atomID)
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

// Search returns atoms whose content, keywords, or tags match query (LIKE).
func (s *AtomStore) Search(ctx context.Context, workspaceID *uuid.UUID, query string, limit int) ([]atom.Atom, error) {
	if limit <= 0 {
		limit = 10
	}

	pattern := "%" + escapeLike(query) + "%"
	var wsArg any
	if workspaceID != nil {
		wsArg = workspaceID.String()
	}

	q := `SELECT ` + atomSelectCols + ` FROM memory_atoms
		WHERE (?1 IS NULL OR workspace_id = ?1)
		  AND (content LIKE ?2 ESCAPE '\' OR keywords LIKE ?2 ESCAPE '\' OR tags LIKE ?2 ESCAPE '\')
		ORDER BY created_at DESC
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, q, wsArg, pattern, limit)
	if err != nil {
		return nil, errWrap("AtomStore.Search", err)
	}
	defer func() { _ = rows.Close() }()

	var out []atom.Atom
	for rows.Next() {
		a, err := scanAtomRow(rows.Scan)
		if err != nil {
			return nil, errWrap("AtomStore.Search scan", err)
		}
		out = append(out, a)
	}
	return out, errWrap("AtomStore.Search iter", rows.Err())
}

func (s *AtomStore) getByID(ctx context.Context, id uuid.UUID) (*atom.Atom, error) {
	const q = `SELECT ` + atomSelectCols + ` FROM memory_atoms
		WHERE id = ?1
		LIMIT 1`
	row := s.db.conn.QueryRowContext(ctx, q, id.String())
	a, err := scanAtomRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, atom.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("AtomStore.getByID", err)
	}
	return &a, nil
}

func (s *AtomStore) linksFrom(ctx context.Context, fromID uuid.UUID) ([]atom.Link, error) {
	const q = `SELECT from_atom_id, to_atom_id, link_type, confidence, created_at
		FROM memory_links WHERE from_atom_id = ?1`
	rows, err := s.db.conn.QueryContext(ctx, q, fromID.String())
	if err != nil {
		return nil, errWrap("AtomStore.linksFrom", err)
	}
	defer func() { _ = rows.Close() }()

	var out []atom.Link
	for rows.Next() {
		var (
			l         atom.Link
			fromStr   string
			toStr     string
			createdNS sql.NullString
		)
		if err := rows.Scan(&fromStr, &toStr, &l.LinkType, &l.Confidence, &createdNS); err != nil {
			return nil, errWrap("AtomStore.linksFrom scan", err)
		}
		if id, err := uuid.Parse(fromStr); err == nil {
			l.FromAtomID = id
		}
		if id, err := uuid.Parse(toStr); err == nil {
			l.ToAtomID = id
		}
		if t := parseTimestamptz(createdNS); t.Valid {
			l.CreatedAt = t.Time
		}
		out = append(out, l)
	}
	return out, errWrap("AtomStore.linksFrom iter", rows.Err())
}
