package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvector "github.com/pgvector/pgvector-go"
)

// Store handles all database operations for the Knowledge bounded context.
type Store struct {
	q           *db.Queries
	embed       ai.ContextEmbeddingProvider
	pool        *pgxpool.Pool
	workspaceID pgtype.UUID
}

// NewStore returns a Store backed by the given connection pool, scoped to the
// optional workspace. nil workspaceID = legacy unscoped mode.
// embed may be nil — embedding operations are skipped gracefully when absent.
func NewStore(pool *pgxpool.Pool, embed ai.ContextEmbeddingProvider, workspaceID *uuid.UUID) *Store {
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
// migration 000019. Hierarchy fields (parent_id, heading_path, heading_level) added in 000027.
// Cross-domain reference fields (project_id, task_id, decision_id) added in migration 000049.
const selectCols = `id, type, title, content, url, tags, created_at, updated_at, source, learning_value,
	importance, recall_count, last_recalled_at, base_lambda, archived_at,
	parent_id, heading_path, heading_level,
	project_id, task_id, decision_id`

// scanKnowledgeItem scans a row (21 columns, no embedding) into db.KnowledgeItem.
func scanKnowledgeItem(scan func(...any) error) (db.KnowledgeItem, error) {
	var i db.KnowledgeItem
	err := scan(
		&i.ID, &i.Type, &i.Title, &i.Content,
		&i.Url, &i.Tags,
		&i.CreatedAt, &i.UpdatedAt,
		&i.Source, &i.LearningValue,
		&i.Importance, &i.RecallCount, &i.LastRecalledAt, &i.BaseLambda, &i.ArchivedAt,
		&i.ParentID, &i.HeadingPath, &i.HeadingLevel,
		&i.ProjectID, &i.TaskID, &i.DecisionID,
	)
	return i, err
}

// dedupSimilarityThreshold is the cosine similarity threshold above which an incoming
// item is considered a duplicate of an existing one. 0.88 chosen for personal-use recall.
const dedupSimilarityThreshold = 0.88

// AddItem creates the knowledge item and synchronously generates and stores its embedding.
// If an embedding client is available:
//  1. URL exact-match check (fast, no Gemini call needed).
//  2. Vector cosine similarity check at the same heading_level (similarity >= 0.88 → ErrDuplicate).
//  3. INSERT, then immediately store the embedding.
//
// When p.Content contains ATX Markdown headings and p.ParentID is nil, a root row is
// inserted first, then each heading section is inserted as a child row (fan-out).
// Any Gemini error or nil vector (no API key) → dedup skipped, item inserted normally.
//
//nolint:gocyclo // fan-out+dedup+parent wiring is inherently branchy; extraction would add indirection
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

	// URL dedup only for top-level items.
	if p.ParentID == nil {
		if err := s.urlDedupCheck(ctx, p.URL); err != nil {
			return nil, err
		}
	}

	vec, err := s.embedAndCheckDupAtLevel(ctx, p.Title+" "+p.Content, p.HeadingLevel)
	if err != nil {
		return nil, err
	}

	// Build hierarchy pgtype values.
	var parentID pgtype.UUID
	if p.ParentID != nil {
		parentID = pgtype.UUID{Bytes: *p.ParentID, Valid: true}
	}
	var headingPath pgtype.Text
	if p.HeadingPath != "" {
		headingPath = pgtype.Text{String: p.HeadingPath, Valid: true}
	}
	var headingLevel pgtype.Int4
	if p.HeadingLevel > 0 || p.ParentID != nil {
		headingLevel = pgtype.Int4{Int32: int32(p.HeadingLevel), Valid: true} //nolint:gosec // G115: HeadingLevel is ATX depth 0-6
	}

	var projectID pgtype.UUID
	if p.ProjectID != nil {
		projectID = pgtype.UUID{Bytes: *p.ProjectID, Valid: true}
	}
	var taskID pgtype.UUID
	if p.TaskID != nil {
		taskID = pgtype.UUID{Bytes: *p.TaskID, Valid: true}
	}
	var decisionID pgtype.UUID
	if p.DecisionID != nil {
		decisionID = pgtype.UUID{Bytes: *p.DecisionID, Valid: true}
	}

	const q = `INSERT INTO knowledge_items
		(type, title, content, url, tags, source, learning_value, workspace_id,
		 parent_id, heading_path, heading_level,
		 project_id, task_id, decision_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING ` + selectCols

	item, err := scanKnowledgeItem(func(args ...any) error {
		return s.pool.QueryRow(ctx, q,
			p.Type, p.Title, p.Content, itemURL, tags, source, lv, s.workspaceID,
			parentID, headingPath, headingLevel,
			projectID, taskID, decisionID,
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

	// Fan-out: insert section children for top-level Markdown items.
	if p.ParentID == nil {
		if err := s.fanOutSections(ctx, &item, p); err != nil {
			slog.Warn("knowledge fan-out failed", "root_id", item.ID, "err", err)
		}
	}

	return &item, nil
}

// fanOutSections parses Markdown headings from p.Content and inserts a child
// knowledge item per section, with parent_id = root.ID.
func (s *Store) fanOutSections(ctx context.Context, root *db.KnowledgeItem, p AddItemParams) error {
	sections := ParseMarkdownSections(p.Content)
	if len(sections) == 0 {
		return nil
	}
	parentBytes := [16]byte(root.ID)
	for _, sec := range sections {
		childP := AddItemParams{
			Type:         p.Type,
			Title:        sec.Title,
			Content:      sec.Content,
			Tags:         p.Tags,
			Source:       p.Source,
			ParentID:     &parentBytes,
			HeadingPath:  sec.HeadingPath,
			HeadingLevel: sec.Level,
		}
		if _, err := s.AddItem(ctx, childP); err != nil {
			slog.Warn("knowledge fan-out child insert failed",
				"root_id", root.ID, "heading", sec.HeadingPath, "err", err)
		}
	}
	return nil
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

// embedAndCheckDupAtLevel computes the embedding for text and checks cosine
// similarity against existing items at the same heading_level. Dedup only
// compares same-level rows so section children do not falsely match root docs.
func (s *Store) embedAndCheckDupAtLevel(ctx context.Context, text string, level int) ([]float32, error) {
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

	existingTitle, similarity, found, err := s.findSimilarAtLevel(ctx, vec, level)
	if err != nil {
		slog.Warn("similarity check failed, skipping dedup", "err", err)
		return vec, nil
	}
	if found && similarity >= dedupSimilarityThreshold {
		return nil, ErrDuplicate{ExistingTitle: existingTitle, Similarity: similarity}
	}
	return vec, nil
}

// findSimilarAtLevel returns the title and cosine similarity of the most similar
// stored item at the given heading_level within the current workspace scope.
// Returns found=false when no items have embeddings at that level yet.
// heading_level=0 matches rows where heading_level IS NULL or heading_level=0
// (backward compat with pre-000027 rows).
func (s *Store) findSimilarAtLevel(ctx context.Context, vec []float32, level int) (
	title string, similarity float64, found bool, err error,
) {
	const q = `SELECT title, 1 - (embedding <=> $1::vector) AS similarity
		FROM knowledge_items
		WHERE embedding IS NOT NULL
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		  AND (COALESCE(heading_level, 0) = $3)
		ORDER BY embedding <=> $1::vector
		LIMIT 1`

	v := pgvector.NewVector(vec)
	//nolint:gosec // G115: level is ATX heading depth 0-6, not user-controlled
	err = s.pool.QueryRow(ctx, q, v, s.workspaceID, int32(level)).Scan(&title, &similarity)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil // no items with embeddings at this level yet
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
	// Subquery wrapper required: Postgres does not allow ORDER BY to reference SELECT-list
	// aliases (strength, sim) in the same query level — wrapping materialises them first.
	const ftsQ = `SELECT ` + selectCols + `, strength, sim FROM (
		SELECT ` + selectCols + `,
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
	) sub
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

// SearchCoarse searches only root rows (COALESCE(heading_level, 0) = 0 and
// parent_id IS NULL) for a coarse-grained overview search. Used by the
// search_knowledge MCP tool when mode="coarse".
func (s *Store) SearchCoarse(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error) {
	// Subquery wrapper required: Postgres does not allow ORDER BY to reference SELECT-list
	// aliases (strength, sim) in the same query level — wrapping materialises them first.
	const ftsQ = `SELECT ` + selectCols + `, strength, sim FROM (
		SELECT ` + selectCols + `,
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
		  AND parent_id IS NULL
		  AND COALESCE(heading_level, 0) = 0
		  AND ($3::uuid IS NULL OR workspace_id = $3)
	) sub
	ORDER BY strength * sim DESC
	LIMIT $2`

	rows, err := s.pool.Query(ctx, ftsQ, query, int32(limit), s.workspaceID) //nolint:gosec // G115: caller guarantees positive int32
	if err != nil {
		return nil, fmt.Errorf("coarse FTS search: %w", err)
	}
	defer rows.Close()

	var items []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItemWithScore(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning coarse FTS result: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating coarse FTS results: %w", err)
	}
	s.bumpRecall(ctx, items)
	return items, nil
}

// vectorSearch executes a raw vector similarity query (only rows with non-null
// embeddings within the current workspace scope). Results are ordered by
// strength × cosine_similarity DESC so fresh high-importance items rank higher.
func (s *Store) vectorSearch(ctx context.Context, vec []float32, limit int) ([]db.KnowledgeItem, error) {
	v := pgvector.NewVector(vec)
	// cosine similarity = 1 - distance; strength multiplied for combined ranking.
	// Subquery wrapper required: Postgres does not allow ORDER BY to reference SELECT-list
	// aliases (strength, sim) in the same query level — wrapping materialises them first.
	const q = `SELECT ` + selectCols + `, strength, sim FROM (
		SELECT ` + selectCols + `,
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
	) sub
	ORDER BY strength * sim DESC
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
		&i.ParentID, &i.HeadingPath, &i.HeadingLevel,
		&i.ProjectID, &i.TaskID, &i.DecisionID,
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

// ListByProjectID returns knowledge items associated with a given project ID.
// Results are ordered by created_at DESC and capped at limit rows.
// SECURITY: scoped to workspace_id.
//
//nolint:dupl // intentionally mirrors ListByTaskID; WHERE column differs (project_id vs task_id); extraction adds no value
func (s *Store) ListByProjectID(ctx context.Context, projectID uuid.UUID, limit int) ([]db.KnowledgeItem, error) {
	const q = `SELECT ` + selectCols + `
		FROM knowledge_items
		WHERE project_id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY created_at DESC
		LIMIT $3`
	rows, err := s.pool.Query(ctx, q, projectID, s.workspaceID, int32(limit)) //nolint:gosec // G115: limit positive by caller
	if err != nil {
		return nil, fmt.Errorf("listing knowledge by project_id %s: %w", projectID, err)
	}
	defer rows.Close()
	var items []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning knowledge item for project: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating knowledge items for project: %w", err)
	}
	if items == nil {
		return []db.KnowledgeItem{}, nil
	}
	return items, nil
}

// ListByTaskID returns knowledge items associated with a given task ID.
// Results are ordered by created_at DESC and capped at limit rows.
// SECURITY: scoped to workspace_id.
//
//nolint:dupl // intentionally mirrors ListByProjectID; WHERE column differs (task_id vs project_id); extraction adds no value
func (s *Store) ListByTaskID(ctx context.Context, taskID uuid.UUID, limit int) ([]db.KnowledgeItem, error) {
	const q = `SELECT ` + selectCols + `
		FROM knowledge_items
		WHERE task_id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY created_at DESC
		LIMIT $3`
	rows, err := s.pool.Query(ctx, q, taskID, s.workspaceID, int32(limit)) //nolint:gosec // G115: limit positive by caller
	if err != nil {
		return nil, fmt.Errorf("listing knowledge by task_id %s: %w", taskID, err)
	}
	defer rows.Close()
	var items []db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning knowledge item for task: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating knowledge items for task: %w", err)
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

// UpdateLearningValue sets the star-rating (1–5) for the given knowledge item.
// Returns ErrNotFound when no row matches within the workspace scope.
func (s *Store) UpdateLearningValue(ctx context.Context, id uuid.UUID, value int) error {
	const q = `UPDATE knowledge_items
		SET learning_value = $1, updated_at = NOW()
		WHERE id = $2
		  AND ($3::uuid IS NULL OR workspace_id = $3)`
	tag, err := s.pool.Exec(ctx, q, int32(value), id, s.workspaceID) //nolint:gosec // G115: value validated 1–5 by handler
	if err != nil {
		return fmt.Errorf("updating learning_value for %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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

// ListChildren returns direct child rows of parentID, ordered by heading_level
// then created_at. Used by navigate_knowledge and outline_knowledge MCP tools.
func (s *Store) ListChildren(ctx context.Context, parentID uuid.UUID) ([]*db.KnowledgeItem, error) {
	const q = `SELECT ` + selectCols + `
		FROM knowledge_items
		WHERE parent_id = $1
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		ORDER BY COALESCE(heading_level, 0) ASC, created_at ASC`

	rows, err := s.pool.Query(ctx, q, parentID, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing children of %s: %w", parentID, err)
	}
	defer rows.Close()

	var items []*db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning child item: %w", err)
		}
		cp := item
		items = append(items, &cp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating child items: %w", err)
	}
	if items == nil {
		return []*db.KnowledgeItem{}, nil
	}
	return items, nil
}

// ListRoots returns top-level knowledge items (parent_id IS NULL) within the
// workspace scope, ordered by creation date descending. Used by
// navigate_knowledge when no parent_id is supplied.
func (s *Store) ListRoots(ctx context.Context) ([]*db.KnowledgeItem, error) {
	const q = `SELECT ` + selectCols + `
		FROM knowledge_items
		WHERE parent_id IS NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC`

	rows, err := s.pool.Query(ctx, q, s.workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing root knowledge items: %w", err)
	}
	defer rows.Close()

	var items []*db.KnowledgeItem
	for rows.Next() {
		item, err := scanKnowledgeItem(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scanning root item: %w", err)
		}
		cp := item
		items = append(items, &cp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating root items: %w", err)
	}
	if items == nil {
		return []*db.KnowledgeItem{}, nil
	}
	return items, nil
}
