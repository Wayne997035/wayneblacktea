package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
	"github.com/waynechen/wayneblacktea/internal/db"
	"github.com/waynechen/wayneblacktea/internal/search"
)

// Store handles all database operations for the Knowledge bounded context.
type Store struct {
	q     *db.Queries
	embed *search.EmbeddingClient
	pool  *pgxpool.Pool
}

// NewStore returns a Store backed by the given connection pool.
func NewStore(pool *pgxpool.Pool, embed *search.EmbeddingClient) *Store {
	return &Store{
		q:     db.New(pool),
		embed: embed,
		pool:  pool,
	}
}

// AddItem creates the knowledge item, then asynchronously generates and stores its embedding.
func (s *Store) AddItem(ctx context.Context, p AddItemParams) (*db.KnowledgeItem, error) {
	var url pgtype.Text
	if p.URL != "" {
		url = pgtype.Text{String: p.URL, Valid: true}
	}
	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}

	item, err := s.q.CreateKnowledgeItem(ctx, db.CreateKnowledgeItemParams{
		Type:    p.Type,
		Title:   p.Title,
		Content: p.Content,
		Url:     url,
		Tags:    tags,
	})
	if err != nil {
		return nil, fmt.Errorf("creating knowledge item: %w", err)
	}

	// Asynchronously generate and store the embedding.
	// context.Background() is intentional: the embedding must outlive the HTTP request context.
	//nolint:gosec // G118: background context required — embedding goroutine must not be cancelled by request
	go func() {
		embedCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		text := item.Title + " " + item.Content
		vec, err := s.embed.Embed(embedCtx, text)
		if err != nil {
			slog.Warn("embedding generation failed", "id", item.ID, "err", err)
			return
		}
		if vec == nil {
			return // GEMINI_API_KEY not set, skip silently
		}

		if err := s.q.UpdateKnowledgeEmbedding(embedCtx, db.UpdateKnowledgeEmbeddingParams{
			ID:        item.ID,
			Embedding: pgvector.NewVector(vec),
		}); err != nil {
			slog.Warn("storing embedding failed", "id", item.ID, "err", err)
		}
	}()

	return &item, nil
}

// Search performs full-text search. If an embedding client is available and the query
// has more than 3 words, it also performs vector similarity search and merges results
// using Reciprocal Rank Fusion.
func (s *Store) Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error) {
	ftsResults, err := s.q.SearchKnowledgeFTS(ctx, db.SearchKnowledgeFTSParams{
		PlaintoTsquery: query,
		Limit:          int32(limit), //nolint:gosec // limit is bounded by caller (max 100)
	})
	if err != nil {
		return nil, fmt.Errorf("FTS search: %w", err)
	}

	ftsItems := make([]db.KnowledgeItem, 0, len(ftsResults))
	for _, r := range ftsResults {
		ftsItems = append(ftsItems, db.KnowledgeItem{
			ID:        r.ID,
			Type:      r.Type,
			Title:     r.Title,
			Content:   r.Content,
			Url:       r.Url,
			Tags:      r.Tags,
			Embedding: r.Embedding,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
		})
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

// vectorSearch executes a raw vector similarity query.
func (s *Store) vectorSearch(ctx context.Context, vec []float32, limit int) ([]db.KnowledgeItem, error) {
	v := pgvector.NewVector(vec)
	query := `SELECT id, type, title, content, url, tags, embedding, created_at, updated_at
		FROM knowledge_items
		WHERE embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2`

	rows, err := s.pool.Query(ctx, query, v, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search query: %w", err)
	}
	defer rows.Close()

	var items []db.KnowledgeItem
	for rows.Next() {
		var i db.KnowledgeItem
		if err := rows.Scan(
			&i.ID, &i.Type, &i.Title, &i.Content,
			&i.Url, &i.Tags, &i.Embedding,
			&i.CreatedAt, &i.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning vector search result: %w", err)
		}
		items = append(items, i)
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

	// Sort by descending RRF score.
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
	items, err := s.q.ListKnowledge(ctx, db.ListKnowledgeParams{
		Limit:  int32(limit),  //nolint:gosec // limit is bounded by caller (max 100)
		Offset: int32(offset), //nolint:gosec // offset is a non-negative pagination value
	})
	if err != nil {
		return nil, fmt.Errorf("listing knowledge items: %w", err)
	}
	if items == nil {
		return []db.KnowledgeItem{}, nil
	}
	return items, nil
}

// GetByID returns a single knowledge item by ID.
func (s *Store) GetByID(ctx context.Context, id uuid.UUID) (*db.KnowledgeItem, error) {
	item, err := s.q.GetKnowledgeByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("getting knowledge item %s: %w", id, err)
	}
	return &item, nil
}
