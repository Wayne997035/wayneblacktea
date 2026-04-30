package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/search"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// Store handles all database operations for the Knowledge bounded context.
type Store struct {
	q           *db.Queries
	embed       *search.EmbeddingClient
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given connection pool, scoped to the
// optional workspace. nil workspaceID = legacy unscoped mode.
func NewStore(pool *pgxpool.Pool, embed *search.EmbeddingClient, workspaceID *uuid.UUID) *Store {
	var ws pgtype.UUID
	if workspaceID != nil {
		ws = pgtype.UUID{Bytes: [16]byte(*workspaceID), Valid: true}
	}
	return &Store{
		q:           db.New(pool),
		embed:       embed,
		pool:        pool,
		workspaceID: ws,
	}
}

// selectCols is the explicit column list for all read queries.
// embedding is intentionally excluded: rows may have NULL embedding (async generation),
// and pgvector-go v0.3.0 DecodeBinary panics on empty bytes even with a pointer scan destination.
const selectCols = `id, type, title, content, url, tags, created_at, updated_at, source, learning_value`

// scanKnowledgeItem scans a row (10 columns, no embedding) into db.KnowledgeItem.
func scanKnowledgeItem(scan func(...any) error) (db.KnowledgeItem, error) {
	var i db.KnowledgeItem
	err := scan(
		&i.ID, &i.Type, &i.Title, &i.Content,
		&i.Url, &i.Tags,
		&i.CreatedAt, &i.UpdatedAt,
		&i.Source, &i.LearningValue,
	)
	return i, err
}

// dedupSimilarityThreshold is the cosine similarity threshold above which an incoming
// item is considered a duplicate of an existing one. 0.88 chosen for personal-use recall.
const dedupSimilarityThreshold = 0.88

// AddItem creates the knowledge item and synchronously generates and stores its embedding.
// If an embedding client is available:
//  1. URL exact-match check (fast, no Gemini call needed).
//  2. Vector cosine similarity check (similarity >= 0.88 → ErrDuplicate).
//  3. INSERT, then immediately store the embedding.
//
// Any Gemini error or nil vector (no API key) → dedup skipped, item inserted normally.
func (s *Store) AddItem(ctx context.Context, p AddItemParams) (*db.KnowledgeItem, error) {
	var itemURL pgtype.Text
	if p.URL != "" {
		itemURL = pgtype.Text{String: p.URL, Valid: true}
	}
	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	source := p.Source
	if source == "" {
		source = "manual"
	}
	var lv pgtype.Int4
	if p.LearningValue > 0 {
		lv = pgtype.Int4{Int32: int32(p.LearningValue), Valid: true} //nolint:gosec // G115: LearningValue is bounded 1-5 by caller
	}

	if err := s.urlDedupCheck(ctx, p.URL); err != nil {
		return nil, err
	}

	vec, err := s.embedAndCheckDup(ctx, p.Title+" "+p.Content)
	if err != nil {
		return nil, err
	}

	const q = `INSERT INTO knowledge_items (type, title, content, url, tags, source, learning_value, workspace_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + selectCols

	item, err := scanKnowledgeItem(func(args ...any) error {
		return s.pool.QueryRow(ctx, q,
			p.Type, p.Title, p.Content, itemURL, tags, source, lv, s.workspaceID,
		).Scan(args...)
	})
	if err != nil {
		return nil, fmt.Errorf("creating knowledge item: %w", err)
	}

	if vec != nil {
		if err := s.q.UpdateKnowledgeEmbedding(ctx, db.UpdateKnowledgeEmbeddingParams{
			ID:        item.ID,
			Embedding: pgvector.NewVector(vec),
		}); err != nil {
			slog.Warn("storing embedding failed", "id", item.ID, "err", err)
		}
	}

	return &item, nil
}

// urlDedupCheck returns ErrDuplicate if an item with the same URL already
// exists in the current workspace scope.
func (s *Store) urlDedupCheck(ctx context.Context, url string) error {
	if url == "" {
		return nil
	}
	const q = `SELECT title FROM knowledge_items
		WHERE url = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		LIMIT 1`
	var existingTitle string
	err := s.pool.QueryRow(ctx, q, url, s.workspaceID).Scan(&existingTitle)
	if err == nil {
		return ErrDuplicate{ExistingTitle: existingTitle, Similarity: 1.0}
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("url dedup check: %w", err)
	}
	return nil
}

// embedAndCheckDup computes the embedding for text and checks cosine similarity against
// existing items. Returns the vector for reuse (nil when embedding is unavailable).
// Embedding or DB errors are logged and treated as non-fatal — dedup is best-effort.
func (s *Store) embedAndCheckDup(ctx context.Context, text string) ([]float32, error) {
	if s.embed == nil {
		return nil, nil
	}
	embedCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	vec, err := s.embed.Embed(embedCtx, text)
	if err != nil {
		slog.Warn("embed failed during dedup, skipping similarity check", "err", err)
		return nil, nil
	}

	existingTitle, similarity, found, err := s.findSimilar(ctx, vec)
	if err != nil {
		slog.Warn("similarity check failed, skipping dedup", "err", err)
		return vec, nil
	}
	if found && similarity >= dedupSimilarityThreshold {
		return nil, ErrDuplicate{ExistingTitle: existingTitle, Similarity: similarity}
	}
	return vec, nil
}

// findSimilar returns the title and cosine similarity of the most similar
// stored item within the current workspace scope. Returns found=false when no
// items have embeddings yet (empty result → not a duplicate).
func (s *Store) findSimilar(ctx context.Context, vec []float32) (
	title string, similarity float64, found bool, err error,
) {
	const q = `SELECT title, 1 - (embedding <=> $1::vector) AS similarity
		FROM knowledge_items
		WHERE embedding IS NOT NULL
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY embedding <=> $1::vector
		LIMIT 1`

	v := pgvector.NewVector(vec)
	err = s.pool.QueryRow(ctx, q, v, s.workspaceID).Scan(&title, &similarity)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil // no items with embeddings yet
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("similarity query: %w", err)
	}
	return title, similarity, true, nil
}

// Search performs full-text search. If an embedding client is available and the query
// has more than 3 words, it also performs vector similarity search and merges results
// using Reciprocal Rank Fusion.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error) {
	const ftsQ = `SELECT ` + selectCols + `
		FROM knowledge_items
		WHERE to_tsvector('english', title || ' ' || content) @@ plainto_tsquery('english', $1)
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY ts_rank(to_tsvector('english', title || ' ' || content), plainto_tsquery('english', $1)) DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, ftsQ, query, int32(limit), s.workspaceID) //nolint:gosec // G115: caller guarantees positive int32
	if err != nil {
		return nil, fmt.Errorf("FTS search: %w", err)
	}
	defer rows.Close()

	var ftsItems []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning FTS result: %w", err)
		}
		ftsItems = append(ftsItems, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating FTS results: %w", err)
	}

	// Vector search: only if embedding client has an API key and query is > 3 words.
	words := strings.Fields(query)
	if len(words) <= 3 {
		return ftsItems, nil
	}

	vec, err := s.embed.Embed(ctx, query)
	if err != nil {
		slog.Warn("vector search embedding failed, returning FTS results only", "err", err)
		return ftsItems, nil
	}
	if vec == nil {
		return ftsItems, nil // no API key
	}

	vecItems, err := s.vectorSearch(ctx, vec, limit)
	if err != nil {
		slog.Warn("vector search failed, returning FTS results only", "err", err)
		return ftsItems, nil
	}

	return mergeRRF(ftsItems, vecItems, limit), nil
}

// vectorSearch executes a raw vector similarity query (only rows with non-null
// embeddings within the current workspace scope).
func (s *Store) vectorSearch(ctx context.Context, vec []float32, limit int) ([]db.KnowledgeItem, error) {
	v := pgvector.NewVector(vec)
	const q = `SELECT ` + selectCols + `
		FROM knowledge_items
		WHERE embedding IS NOT NULL
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY embedding <=> $1::vector
		LIMIT $2`

	rows, err := s.pool.Query(ctx, q, v, limit, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("vector search query: %w", err)
	}
	defer rows.Close()

	var items []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning vector search result: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating vector search results: %w", err)
	}
	return items, nil
}

// mergeRRF merges two ranked lists using Reciprocal Rank Fusion (k=60).
func mergeRRF(fts, vec []db.KnowledgeItem, limit int) []db.KnowledgeItem {
	const k = 60.0
	scores := make(map[uuid.UUID]float64)
	order := make([]uuid.UUID, 0)
	seen := make(map[uuid.UUID]bool)
	byID := make(map[uuid.UUID]db.KnowledgeItem)

	for rank, item := range fts {
		scores[item.ID] += 1.0 / (k + float64(rank+1))
		if !seen[item.ID] {
			order = append(order, item.ID)
			seen[item.ID] = true
		}
		byID[item.ID] = item
	}
	for rank, item := range vec {
		scores[item.ID] += 1.0 / (k + float64(rank+1))
		if !seen[item.ID] {
			order = append(order, item.ID)
			seen[item.ID] = true
		}
		byID[item.ID] = item
	}

	for i := 0; i < len(order)-1; i++ {
		for j := i + 1; j < len(order); j++ {
			if scores[order[j]] > scores[order[i]] {
				order[i], order[j] = order[j], order[i]
			}
		}
	}

	result := make([]db.KnowledgeItem, 0, limit)
	for _, id := range order {
		if len(result) >= limit {
			break
		}
		result = append(result, byID[id])
	}
	return result
}

// List returns knowledge items ordered by creation date.
func (s *Store) List(ctx context.Context, limit, offset int) ([]db.KnowledgeItem, error) {
	const q = `SELECT ` + selectCols + `
		FROM knowledge_items
		WHERE ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := s.pool.Query(ctx, q, int32(limit), int32(offset), s.workspaceID) //nolint:gosec // G115: fits int32 by caller contract
	if err != nil {
		return nil, fmt.Errorf("listing knowledge items: %w", err)
	}
	defer rows.Close()

	var items []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning knowledge item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating knowledge items: %w", err)
	}
	if items == nil {
		return []db.KnowledgeItem{}, nil
	}
	return items, nil
}

// GetByID returns a single knowledge item by ID within the current workspace
// scope. Returns ErrNotFound when the item does not exist or belongs to a
// different workspace.
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*db.KnowledgeItem, error) {
	const q = `SELECT ` + selectCols + `
		FROM knowledge_items
		WHERE id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)`
	item, err := scanKnowledgeItem(func(args ...any) error {
		return s.pool.QueryRow(ctx, q, id, s.workspaceID).Scan(args...)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting knowledge item %s: %w", id, err)
	}
	return &item, nil
}
