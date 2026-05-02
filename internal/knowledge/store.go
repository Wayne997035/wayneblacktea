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
// Decay fields (importance, recall_count, last_recalled_at, base_lambda, archived_at) added in
// migration 000019.
const selectCols = `id, type, title, content, url, tags, created_at, updated_at, source, learning_value,
	importance, recall_count, last_recalled_at, base_lambda, archived_at`

// scanKnowledgeItem scans a row (15 columns, no embedding) into db.KnowledgeItem.
func scanKnowledgeItem(scan func(...any) error) (db.KnowledgeItem, error) {
	var i db.KnowledgeItem
	err := scan(
		&i.ID, &i.Type, &i.Title, &i.Content,
		&i.Url, &i.Tags,
		&i.CreatedAt, &i.UpdatedAt,
		&i.Source, &i.LearningValue,
		&i.Importance, &i.RecallCount, &i.LastRecalledAt, &i.BaseLambda, &i.ArchivedAt,
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
// using Reciprocal Rank Fusion. Results are ordered by strength × similarity DESC
// (Ebbinghaus decay weighting). On each hit, recall_count is incremented and
// last_recalled_at is set atomically.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error) {
	// strength formula inline: importance * exp(-base_lambda*(1-importance*0.8)*age_days) * (1+recall_count*0.2)
	// ts_rank is the similarity signal for FTS. Combined: ORDER BY strength * ts_rank DESC.
	const ftsQ = `SELECT ` + selectCols + `,
		GREATEST(0.0, LEAST(1.0,
			importance
			* EXP(-base_lambda * (1.0 - importance * 0.8)
				* EXTRACT(EPOCH FROM (NOW() - COALESCE(last_recalled_at, created_at))) / 86400.0)
			* (1.0 + recall_count * 0.2)
		)) AS strength,
		ts_rank(to_tsvector('english', title || ' ' || content), plainto_tsquery('english', $1)) AS sim
		FROM knowledge_items
		WHERE to_tsvector('english', title || ' ' || content) @@ plainto_tsquery('english', $1)
		  AND archived_at IS NULL
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY strength * sim DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, ftsQ, query, int32(limit), s.workspaceID) //nolint:gosec // G115: caller guarantees positive int32
	if err != nil {
		return nil, fmt.Errorf("FTS search: %w", err)
	}
	defer rows.Close()

	var ftsItems []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItemWithScore(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning FTS result: %w", err)
		}
		ftsItems = append(ftsItems, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating FTS results: %w", err)
	}

	// Bump recall atomically for returned items.
	s.bumpRecall(ctx, ftsItems)

	// Vector search: only if embedding client has an API key and query is > 3 words.
	words := strings.Fields(query)
	if s.embed == nil || len(words) <= 3 {
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

	merged := mergeRRF(ftsItems, vecItems, limit)
	// Bump recall for any items returned only from vector search.
	s.bumpRecall(ctx, vecItems)
	return merged, nil
}

// vectorSearch executes a raw vector similarity query (only rows with non-null
// embeddings within the current workspace scope). Results are ordered by
// strength × cosine_similarity DESC so fresh high-importance items rank higher.
func (s *Store) vectorSearch(ctx context.Context, vec []float32, limit int) ([]db.KnowledgeItem, error) {
	v := pgvector.NewVector(vec)
	// cosine similarity = 1 - distance; strength multiplied for combined ranking.
	const q = `SELECT ` + selectCols + `,
		GREATEST(0.0, LEAST(1.0,
			importance
			* EXP(-base_lambda * (1.0 - importance * 0.8)
				* EXTRACT(EPOCH FROM (NOW() - COALESCE(last_recalled_at, created_at))) / 86400.0)
			* (1.0 + recall_count * 0.2)
		)) AS strength,
		(1.0 - (embedding <=> $1::vector)) AS sim
		FROM knowledge_items
		WHERE embedding IS NOT NULL
		  AND archived_at IS NULL
		  AND ($3::uuid IS NULL OR workspace_id = $3)
		ORDER BY strength * (1.0 - (embedding <=> $1::vector)) DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, q, v, limit, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("vector search query: %w", err)
	}
	defer rows.Close()

	var items []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItemWithScore(rows.Scan)
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

// scanKnowledgeItemWithScore scans a row that includes two trailing score
// columns (strength FLOAT, sim FLOAT) appended by the strength-ranked query.
// The score columns are consumed but not stored in the model (used only for ORDER BY).
func scanKnowledgeItemWithScore(scan func(...any) error) (db.KnowledgeItem, error) {
	var (
		i             db.KnowledgeItem
		strength, sim float64
	)
	err := scan(
		&i.ID, &i.Type, &i.Title, &i.Content,
		&i.Url, &i.Tags,
		&i.CreatedAt, &i.UpdatedAt,
		&i.Source, &i.LearningValue,
		&i.Importance, &i.RecallCount, &i.LastRecalledAt, &i.BaseLambda, &i.ArchivedAt,
		&strength, &sim,
	)
	return i, err
}

// bumpRecall increments recall_count and sets last_recalled_at=NOW() for all
// returned items using a single batched UPDATE. Each update is atomic via SQL;
// this prevents race conditions between concurrent searches on the same row.
// Errors are logged at warn level only — recall tracking is best-effort.
func (s *Store) bumpRecall(ctx context.Context, items []db.KnowledgeItem) {
	if len(items) == 0 {
		return
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID.String())
	}
	// Use ANY($1::uuid[]) for an atomic single-statement UPDATE.
	const q = `UPDATE knowledge_items
		SET recall_count = recall_count + 1,
		    last_recalled_at = NOW()
		WHERE id = ANY($1::uuid[])
		  AND ($2::uuid IS NULL OR workspace_id = $2)`
	if _, err := s.pool.Exec(ctx, q, ids, s.workspaceID); err != nil {
		slog.Warn("knowledge: bump recall failed", "count", len(items), "err", err)
	}
}

// SoftPruneDecayed implements decay.PrunerStore. It sets archived_at=NOW() on
// knowledge_items that are:
//   - not already archived (archived_at IS NULL)
//   - older than cutoff (created_at < cutoff, i.e. age > 90 days)
//   - Ebbinghaus strength < strengthThreshold
//
// Decisions table is never touched by this method.
func (s *Store) SoftPruneDecayed(ctx context.Context, cutoff time.Time, strengthThreshold float64) (int64, error) {
	const q = `UPDATE knowledge_items
		SET archived_at = NOW()
		WHERE archived_at IS NULL
		  AND created_at < $1
		  AND GREATEST(0.0, LEAST(1.0,
			importance
			* EXP(-base_lambda * (1.0 - importance * 0.8)
				* EXTRACT(EPOCH FROM (NOW() - COALESCE(last_recalled_at, created_at))) / 86400.0)
			* (1.0 + recall_count * 0.2)
		  )) < $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)`
	tag, err := s.pool.Exec(ctx, q, cutoff, strengthThreshold, s.workspaceID)
	if err != nil {
		return 0, fmt.Errorf("soft prune knowledge_items: %w", err)
	}
	return tag.RowsAffected(), nil
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

// SearchByCosine returns the top-limit knowledge items whose embeddings are most
// similar to queryEmbedding, filtered by workspace_id.  Delegates to the
// existing vectorSearch method.
//
// SECURITY: filtered by workspace_id via vectorSearch → no cross-workspace data.
func (s *Store) SearchByCosine(ctx context.Context, queryEmbedding []float32, limit int) ([]db.KnowledgeItem, error) {
	if len(queryEmbedding) == 0 || limit <= 0 {
		return nil, nil
	}
	return s.vectorSearch(ctx, queryEmbedding, limit)
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
