package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/db"
	"github.com/Wayne997035/wayneblacktea/internal/decay"
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

// knowledgeSelectCols is the explicit column list for all read queries.
// Decay fields (importance, recall_count, last_recalled_at, base_lambda, archived_at)
// were added in migration 000019.
const knowledgeSelectCols = `id, type, title, content, url, tags,
	created_at, updated_at, source, learning_value, workspace_id,
	importance, recall_count, last_recalled_at, base_lambda, archived_at`

func scanKnowledgeItem(scan func(...any) error) (db.KnowledgeItem, error) {
	var (
		item                         db.KnowledgeItem
		idStr                        string
		urlNS, tagsNS, createdNS     sql.NullString
		updatedNS                    sql.NullString
		workspaceNS                  sql.NullString
		learningValueNullableInteger sql.NullInt32
		lastRecalledNS               sql.NullString
		archivedNS                   sql.NullString
	)
	err := scan(
		&idStr, &item.Type, &item.Title, &item.Content, &urlNS, &tagsNS,
		&createdNS, &updatedNS, &item.Source, &learningValueNullableInteger, &workspaceNS,
		&item.Importance, &item.RecallCount, &lastRecalledNS, &item.BaseLambda, &archivedNS,
	)
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
	item.LastRecalledAt = parseTimestamptz(lastRecalledNS)
	item.ArchivedAt = parseTimestamptz(archivedNS)
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

// knowledgeWithStrength bundles a knowledge item with its computed strength for
// in-memory sorting.
type knowledgeWithStrength struct {
	item     db.KnowledgeItem
	strength float64
}

// Search performs a portable LIKE search over title and content.
// Results are sorted app-side by Ebbinghaus strength so fresh high-importance
// items appear first. On each hit, recall_count is incremented atomically.
func (s *KnowledgeStore) Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error) {
	pattern := "%" + escapeLike(query) + "%"
	// Fetch more candidates than limit so the strength reordering has room.
	fetchLimit := limit * 3
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE (title LIKE ?1 ESCAPE '\' OR content LIKE ?1 ESCAPE '\')
		  AND archived_at IS NULL
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY
		  CASE WHEN title LIKE ?1 ESCAPE '\' THEN 0 ELSE 1 END,
		  created_at DESC, id DESC
		LIMIT ?3`
	items, err := s.list(ctx, "SearchKnowledge", q, pattern, s.db.workspaceArg(), fetchLimit)
	if err != nil {
		return nil, err
	}

	// Compute strength app-side (SQLite has no EXTRACT EPOCH).
	now := time.Now().UTC()
	ranked := make([]knowledgeWithStrength, 0, len(items))
	for _, item := range items {
		ageDays := computeAgeDays(item, now)
		str := decay.ComputeStrength(item.Importance, item.BaseLambda, ageDays, int(item.RecallCount))
		ranked = append(ranked, knowledgeWithStrength{item: item, strength: str})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].strength > ranked[j].strength
	})

	// Trim to limit.
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	// Bump recall atomically for returned items.
	out := make([]db.KnowledgeItem, 0, len(ranked))
	ids := make([]string, 0, len(ranked))
	for _, r := range ranked {
		out = append(out, r.item)
		ids = append(ids, r.item.ID.String())
	}
	s.bumpRecall(ctx, ids)
	return out, nil
}

// bumpRecall increments recall_count and sets last_recalled_at=NOW() atomically
// for each ID using individual UPDATE statements. SQLite has no array type so
// we update individually, but each is atomic. Errors are logged at warn level
// only — recall tracking is best-effort.
func (s *KnowledgeStore) bumpRecall(ctx context.Context, ids []string) {
	if len(ids) == 0 {
		return
	}
	now := sqliteNowMillis()
	const q = `UPDATE knowledge_items
		SET recall_count = recall_count + 1,
		    last_recalled_at = ?1
		WHERE id = ?2
		  AND (?3 IS NULL OR workspace_id = ?3)`
	for _, id := range ids {
		if _, err := s.db.conn.ExecContext(ctx, q, now, id, s.db.workspaceArg()); err != nil {
			slog.Warn("sqlite knowledge: bump recall failed", "id", id, "err", err)
		}
	}
}

// computeAgeDays computes the age in days since last recall (or creation if never recalled).
func computeAgeDays(item db.KnowledgeItem, now time.Time) float64 {
	if item.LastRecalledAt.Valid {
		diff := now.Sub(item.LastRecalledAt.Time)
		if diff < 0 {
			return 0
		}
		return diff.Hours() / 24.0
	}
	if item.CreatedAt.Valid {
		diff := now.Sub(item.CreatedAt.Time)
		if diff < 0 {
			return 0
		}
		return diff.Hours() / 24.0
	}
	return 0
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

// UpdateEmbedding writes the embedding bytes to the knowledge_items row
// matching id within the current workspace scope. Best-effort: returns nil
// when no row matches. Used by tests + the future Stop-hook integration that
// will populate knowledge_items.embedding alongside session_handoffs.
func (s *KnowledgeStore) UpdateEmbedding(ctx context.Context, id uuid.UUID, embedding []byte) error {
	const q = `UPDATE knowledge_items
		SET embedding = ?1
		WHERE id = ?2
		  AND (?3 IS NULL OR workspace_id = ?3)`
	if _, err := s.db.conn.ExecContext(ctx, q, embedding, id.String(), s.db.workspaceArg()); err != nil {
		return errWrap("UpdateKnowledgeEmbedding", err)
	}
	return nil
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
		// SECURITY/correctness: scan list MUST match knowledgeSelectCols (16 cols)
		// + trailing embedding BLOB = 17 destinations. Migration 000019 added
		// 5 decay columns (importance, recall_count, last_recalled_at,
		// base_lambda, archived_at); a 12-arg scan would silently fail every
		// row and return zero results (security audit C-1).
		var item db.KnowledgeItem
		var idStr string
		var urlNS, tagsNS, createdNS, updatedNS, workspaceNS sql.NullString
		var learningValue sql.NullInt32
		var lastRecalledNS, archivedNS sql.NullString
		var rawEmbed []byte
		if err := rows.Scan(
			&idStr, &item.Type, &item.Title, &item.Content, &urlNS, &tagsNS,
			&createdNS, &updatedNS, &item.Source, &learningValue, &workspaceNS,
			&item.Importance, &item.RecallCount, &lastRecalledNS, &item.BaseLambda, &archivedNS,
			&rawEmbed,
		); err != nil {
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
		item.LastRecalledAt = parseTimestamptz(lastRecalledNS)
		item.ArchivedAt = parseTimestamptz(archivedNS)

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

// SoftPruneDecayed implements decay.PrunerStore for the SQLite backend.
// It sets archived_at=NOW() on knowledge_items that are:
//   - not already archived (archived_at IS NULL)
//   - older than cutoff (created_at < cutoff, i.e. age > 90 days)
//   - Ebbinghaus strength (computed app-side) < strengthThreshold
//
// Decisions table is never touched.
func (s *KnowledgeStore) SoftPruneDecayed(ctx context.Context, cutoff time.Time, strengthThreshold float64) (int64, error) {
	cutoffStr := cutoff.UTC().Format(sqliteMillisLayout)
	// Fetch candidates older than cutoff that are not yet archived.
	const selQ = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE archived_at IS NULL
		  AND created_at < ?1
		  AND (?2 IS NULL OR workspace_id = ?2)`
	candidates, err := s.list(ctx, "SoftPruneDecayedSelect", selQ, cutoffStr, s.db.workspaceArg())
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC()
	nowStr := now.Format(sqliteMillisLayout)
	var pruned int64
	const updQ = `UPDATE knowledge_items SET archived_at = ?1
		WHERE id = ?2
		  AND (?3 IS NULL OR workspace_id = ?3)`
	for _, item := range candidates {
		ageDays := computeAgeDays(item, now)
		str := decay.ComputeStrength(item.Importance, item.BaseLambda, ageDays, int(item.RecallCount))
		if str >= strengthThreshold {
			continue
		}
		if _, err := s.db.conn.ExecContext(ctx, updQ, nowStr, item.ID.String(), s.db.workspaceArg()); err != nil {
			slog.Warn("sqlite knowledge: soft-prune update failed", "id", item.ID, "err", err)
			continue
		}
		pruned++
	}
	return pruned, nil
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
