package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/knowledge"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// KnowledgeStore is the SQLite-backed implementation of knowledge.StoreIface.
type KnowledgeStore struct {
	db *DB
}

// NewKnowledgeStore wraps an open DB into a KnowledgeStore.
func NewKnowledgeStore(d *DB) *KnowledgeStore {
	return &KnowledgeStore{db: d}
}

var _ knowledge.StoreIface = (*KnowledgeStore)(nil)

const knowledgeSelectCols = `id, type, title, content, url, tags,
	created_at, updated_at, source, learning_value, workspace_id`

func scanKnowledgeItem(scan func(...any) error) (db.KnowledgeItem, error) {
	var (
		item                         db.KnowledgeItem
		idStr                        string
		urlNS, tagsNS, createdNS     sql.NullString
		updatedNS                    sql.NullString
		workspaceNS                  sql.NullString
		learningValueNullableInteger sql.NullInt32
	)
	err := scan(&idStr, &item.Type, &item.Title, &item.Content, &urlNS, &tagsNS,
		&createdNS, &updatedNS, &item.Source, &learningValueNullableInteger, &workspaceNS)
	if err != nil {
		return db.KnowledgeItem{}, err
	}
	if id, err := uuid.Parse(idStr); err == nil {
		item.ID = id
	}
	tags, err := decodeStringSlice(tagsNS)
	if err != nil {
		return db.KnowledgeItem{}, err
	}
	item.Url = pgtypeText(urlNS.String, urlNS.Valid)
	item.Tags = tags
	item.CreatedAt = parseTimestamptz(createdNS)
	item.UpdatedAt = parseTimestamptz(updatedNS)
	if learningValueNullableInteger.Valid {
		item.LearningValue = pgtype.Int4{Int32: learningValueNullableInteger.Int32, Valid: true}
	}
	item.WorkspaceID = pgtypeUUID(nsString(workspaceNS))
	return item, nil
}

// AddItem creates a knowledge item using LIKE/search-only SQLite v2 semantics.
func (s *KnowledgeStore) AddItem(ctx context.Context, p knowledge.AddItemParams) (*db.KnowledgeItem, error) {
	if err := s.urlDedupCheck(ctx, p.URL); err != nil {
		return nil, err
	}

	tagsJSON, err := encodeStringSlice(p.Tags)
	if err != nil {
		return nil, err
	}
	source := p.Source
	if source == "" {
		source = "manual"
	}
	var learningValue any
	if p.LearningValue > 0 {
		learningValue = p.LearningValue
	}

	id := uuid.New()
	now := sqliteNowMillis()
	const q = `INSERT INTO knowledge_items
		(id, workspace_id, type, title, content, url, tags, source, learning_value, created_at, updated_at)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?10)`
	_, err = s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), p.Type, p.Title, p.Content,
		nullStringIfEmpty(p.URL), tagsJSON, source, learningValue, now)
	if err != nil {
		return nil, errWrap("AddKnowledgeItem", err)
	}
	return s.GetByID(ctx, id)
}

func (s *KnowledgeStore) urlDedupCheck(ctx context.Context, url string) error {
	if url == "" {
		return nil
	}
	const q = `SELECT title FROM knowledge_items
		WHERE url = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	var title string
	err := s.db.conn.QueryRowContext(ctx, q, url, s.db.workspaceArg()).Scan(&title)
	if err == nil {
		return knowledge.ErrDuplicate{ExistingTitle: title, Similarity: 1.0}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return errWrap("knowledge url dedup", err)
}

// escapeLike escapes LIKE wildcards in user input so they are treated as
// literals when used with SQLite ESCAPE '\'. The outer % wildcards are added
// by the caller after escaping.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// Search performs a portable LIKE search over title and content.
func (s *KnowledgeStore) Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error) {
	pattern := "%" + escapeLike(query) + "%"
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE (title LIKE ?1 ESCAPE '\' OR content LIKE ?1 ESCAPE '\')
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY
		  CASE WHEN title LIKE ?1 ESCAPE '\' THEN 0 ELSE 1 END,
		  created_at DESC, id DESC
		LIMIT ?3`
	return s.list(ctx, "SearchKnowledge", q, pattern, s.db.workspaceArg(), limit)
}

// List returns knowledge items ordered by creation date.
func (s *KnowledgeStore) List(ctx context.Context, limit, offset int) ([]db.KnowledgeItem, error) {
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC, id DESC
		LIMIT ?2 OFFSET ?3`
	return s.list(ctx, "ListKnowledge", q, s.db.workspaceArg(), limit, offset)
}

// GetByID returns a single knowledge item by ID within the workspace scope.
func (s *KnowledgeStore) GetByID(ctx context.Context, id uuid.UUID) (*db.KnowledgeItem, error) {
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		LIMIT 1`
	item, err := scanKnowledgeItem(s.db.conn.QueryRowContext(ctx, q, id.String(), s.db.workspaceArg()).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, knowledge.ErrNotFound
	}
	if err != nil {
		return nil, errWrap("GetKnowledgeByID", err)
	}
	return &item, nil
}

// SearchByCosine returns the top-limit knowledge items most similar to queryEmbedding.
// SQLite has no pgvector — brute-force Go-side cosine scan.
// knowledge_items.embedding is stored as BLOB (serialized float32 LE).
//
// SECURITY: filtered by workspace_id — no cross-workspace data returned.
func (s *KnowledgeStore) SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.KnowledgeItem, error) {
	if len(queryEmbedding) == 0 || limit <= 0 {
		return nil, nil
	}
	// knowledge_items.embedding is a BLOB in SQLite (Postgres uses pgvector type).
	const q = `SELECT ` + knowledgeSelectCols + `, embedding FROM knowledge_items
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
		item db.KnowledgeItem
		sim  float64
	}
	var candidates []scored
	for rows.Next() {
		var item db.KnowledgeItem
		var idStr string
		var urlNS, tagsNS, createdNS, updatedNS, workspaceNS sql.NullString
		var learningValue sql.NullInt32
		var rawEmbed []byte
		if err := rows.Scan(&idStr, &item.Type, &item.Title, &item.Content, &urlNS, &tagsNS,
			&createdNS, &updatedNS, &item.Source, &learningValue, &workspaceNS, &rawEmbed); err != nil {
			continue
		}
		if id, parseErr := uuid.Parse(idStr); parseErr == nil {
			item.ID = id
		}
		tags, _ := decodeStringSlice(tagsNS)
		item.Tags = tags
		item.Url = pgtypeText(urlNS.String, urlNS.Valid)
		item.CreatedAt = parseTimestamptz(createdNS)
		item.UpdatedAt = parseTimestamptz(updatedNS)
		if learningValue.Valid {
			item.LearningValue = pgtype.Int4{Int32: learningValue.Int32, Valid: true}
		}
		item.WorkspaceID = pgtypeUUID(nsString(workspaceNS))

		vec := localai.DeserializeEmbedding(rawEmbed)
		if vec == nil {
			continue
		}
		candidates = append(candidates, scored{item: item, sim: localai.CosineSimilarity(queryEmbedding, vec)})
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
	result := make([]db.KnowledgeItem, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, c.item)
	}
	return result, nil
}

func (s *KnowledgeStore) list(ctx context.Context, op, q string, args ...any) ([]db.KnowledgeItem, error) {
	rows, err := s.db.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, errWrap(op, err)
	}
	defer func() { _ = rows.Close() }()
	var out []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItem(rows.Scan)
		if err != nil {
			return nil, errWrap(op+" scan", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap(op+" iter", err)
	}
	if out == nil {
		return []db.KnowledgeItem{}, nil
	}
	return out, nil
}
