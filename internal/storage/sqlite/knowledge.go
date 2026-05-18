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
// were added in migration 000019. Hierarchy fields (parent_id, heading_path,
// heading_level) were added in schema.sql alongside migration 000027.
// Cross-domain reference fields (project_id, task_id, decision_id) added in migration 000049.
const knowledgeSelectCols = `id, type, title, content, url, tags,
	created_at, updated_at, source, learning_value, workspace_id,
	importance, recall_count, last_recalled_at, base_lambda, archived_at,
	parent_id, heading_path, heading_level,
	project_id, task_id, decision_id`

// knowledgeSelectColsKI is the same column list fully qualified with the "ki"
// table alias, for use in FTS5 JOIN queries where bare column names are
// ambiguous (knowledge_items_fts also exposes title and content columns).
const knowledgeSelectColsKI = `ki.id, ki.type, ki.title, ki.content, ki.url, ki.tags,
	ki.created_at, ki.updated_at, ki.source, ki.learning_value, ki.workspace_id,
	ki.importance, ki.recall_count, ki.last_recalled_at, ki.base_lambda, ki.archived_at,
	ki.parent_id, ki.heading_path, ki.heading_level,
	ki.project_id, ki.task_id, ki.decision_id`

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
		parentIDNS                   sql.NullString
		headingPathNS                sql.NullString
		headingLevelNS               sql.NullInt32
		projectIDNS, taskIDNS        sql.NullString
		decisionIDNS                 sql.NullString
	)
	err := scan(
		&idStr, &item.Type, &item.Title, &item.Content, &urlNS, &tagsNS,
		&createdNS, &updatedNS, &item.Source, &learningValueNullableInteger, &workspaceNS,
		&item.Importance, &item.RecallCount, &lastRecalledNS, &item.BaseLambda, &archivedNS,
		&parentIDNS, &headingPathNS, &headingLevelNS,
		&projectIDNS, &taskIDNS, &decisionIDNS,
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
	item.ParentID = pgtypeUUID(nsString(parentIDNS))
	if headingPathNS.Valid {
		item.HeadingPath = pgtype.Text{String: headingPathNS.String, Valid: true}
	}
	if headingLevelNS.Valid {
		item.HeadingLevel = pgtype.Int4{Int32: headingLevelNS.Int32, Valid: true}
	}
	item.ProjectID = pgtypeUUID(nsString(projectIDNS))
	item.TaskID = pgtypeUUID(nsString(taskIDNS))
	item.DecisionID = pgtypeUUID(nsString(decisionIDNS))
	return item, nil
}

// crossDomainArgs converts optional UUID pointers to SQLite-ready args (nil → NULL).
// Extracted to reduce cyclomatic complexity of AddItem (migration 000049 added 3 branches).
func crossDomainArgs(projectID, taskID, decisionID *[16]byte) (any, any, any) {
	var p, t, d any
	if projectID != nil {
		p = uuid.UUID(*projectID).String()
	}
	if taskID != nil {
		t = uuid.UUID(*taskID).String()
	}
	if decisionID != nil {
		d = uuid.UUID(*decisionID).String()
	}
	return p, t, d
}

// AddItem creates a knowledge item using LIKE/search-only SQLite v2 semantics.
// When p.Content contains ATX Markdown headings and p.ParentID is nil, it also
// inserts child rows for each section (fan-out). Fan-out failures are non-fatal.
func (s *KnowledgeStore) AddItem(ctx context.Context, p knowledge.AddItemParams) (*db.KnowledgeItem, error) {
	// URL dedup only for top-level items.
	if p.ParentID == nil {
		if err := s.urlDedupCheck(ctx, p.URL); err != nil {
			return nil, err
		}
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

	var parentIDArg any
	if p.ParentID != nil {
		parentIDArg = uuid.UUID(*p.ParentID).String()
	}
	var headingPathArg any
	if p.HeadingPath != "" {
		headingPathArg = p.HeadingPath
	}
	var headingLevelArg any
	if p.HeadingLevel > 0 || p.ParentID != nil {
		headingLevelArg = p.HeadingLevel
	}

	// Cross-domain reference fields (migration 000049).
	// No FK constraint (CLAUDE.md red-line §9); nil → NULL.
	projectIDArg, taskIDArg, decisionIDArg := crossDomainArgs(p.ProjectID, p.TaskID, p.DecisionID)

	const q = `INSERT INTO knowledge_items
		(id, workspace_id, type, title, content, url, tags, source, learning_value,
		 created_at, updated_at, parent_id, heading_path, heading_level,
		 project_id, task_id, decision_id)
		VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?10, ?11, ?12, ?13, ?14, ?15, ?16)`
	_, err = s.db.conn.ExecContext(ctx, q,
		id.String(), s.db.workspaceArg(), p.Type, p.Title, p.Content,
		nullStringIfEmpty(p.URL), tagsJSON, source, learningValue, now,
		parentIDArg, headingPathArg, headingLevelArg,
		projectIDArg, taskIDArg, decisionIDArg)
	if err != nil {
		return nil, errWrap("AddKnowledgeItem", err)
	}

	item, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// Fan-out: insert section children for top-level Markdown items.
	if p.ParentID == nil {
		s.fanOutSections(ctx, item, p)
	}

	return item, nil
}

// fanOutSections parses Markdown headings from p.Content and inserts a child
// knowledge item per section. Failures are logged at Warn only — the root item
// is already committed.
func (s *KnowledgeStore) fanOutSections(ctx context.Context, root *db.KnowledgeItem, p knowledge.AddItemParams) {
	sections := knowledge.ParseMarkdownSections(p.Content)
	if len(sections) == 0 {
		return
	}
	parentBytes := [16]byte(root.ID)
	for _, sec := range sections {
		childP := knowledge.AddItemParams{
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
			slog.Warn("sqlite knowledge fan-out child insert failed",
				"root_id", root.ID, "heading", sec.HeadingPath, "err", err)
		}
	}
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

// toFTS5Query converts a user search string to an FTS5 MATCH expression.
// Special characters are stripped; each word gets a * suffix for prefix
// matching so "conf" matches "configuration" and "config".
// Returns "" when no usable terms remain (caller should treat as empty result).
func toFTS5Query(q string) string {
	if len(q) > 500 {
		q = q[:500]
	}
	var terms []string
	for _, word := range strings.Fields(q) {
		clean := strings.Map(func(r rune) rune {
			switch r {
			case '"', '\'', '^', '*', ':', '-', '+', '(', ')', '.', '\\', '{', '}', 0:
				return -1
			}
			return r
		}, word)
		if clean != "" {
			terms = append(terms, clean+"*")
		}
	}
	return strings.Join(terms, " ")
}

// knowledgeWithStrength bundles a knowledge item with its computed strength for
// in-memory sorting.
type knowledgeWithStrength struct {
	item     db.KnowledgeItem
	strength float64
}

// Search performs FTS5 full-text search over title and content.
// Results are sorted app-side by Ebbinghaus strength so fresh high-importance
// items appear first. On each hit, recall_count is incremented atomically.
//
// FTS5 provides BM25-ranked inverted-index search with porter stemming and
// prefix matching (toFTS5Query appends * to each term). Falls back to nil
// when the query produces no usable FTS5 terms (all-special-char input).
//
//nolint:dupl // intentionally similar to SearchCoarse; SQL WHERE differs in root-level filter; extraction would complicate the query
func (s *KnowledgeStore) Search(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error) {
	ftsQuery := toFTS5Query(query)
	if ftsQuery == "" {
		return nil, nil
	}
	// Fetch more candidates than limit so the strength reordering has room.
	fetchLimit := limit * 3
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	// fts.rank is negative in FTS5 (closer to 0 = better match); ORDER BY rank ASC
	// returns best matches first. The app-side Ebbinghaus re-sort runs after fetch.
	const ftsQ = `SELECT ` + knowledgeSelectColsKI + `, fts.rank
		FROM knowledge_items_fts fts
		JOIN knowledge_items ki ON ki.rowid = fts.rowid
		WHERE knowledge_items_fts MATCH ?1
		  AND ki.archived_at IS NULL
		  AND (?2 IS NULL OR ki.workspace_id = ?2)
		ORDER BY fts.rank
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, ftsQ, ftsQuery, s.db.workspaceArg(), fetchLimit)
	if err != nil {
		return nil, errWrap("SearchKnowledge", err)
	}
	defer func() { _ = rows.Close() }()
	var items []db.KnowledgeItem
	for rows.Next() {
		var rank float64
		item, scanErr := scanKnowledgeItem(func(dest ...any) error {
			return rows.Scan(append(dest, &rank)...)
		})
		if scanErr != nil {
			return nil, errWrap("SearchKnowledge scan", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("SearchKnowledge iter", err)
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

// SearchCoarse searches only root-level rows (parent_id IS NULL,
// COALESCE(heading_level, 0) = 0) using FTS5. Used by search_knowledge mode="coarse".
//
//nolint:dupl // mirrors Search but adds root-level filter (parent_id IS NULL); extracting shared body would complicate callers
func (s *KnowledgeStore) SearchCoarse(ctx context.Context, query string, limit int) ([]db.KnowledgeItem, error) {
	ftsQuery := toFTS5Query(query)
	if ftsQuery == "" {
		return nil, nil
	}
	fetchLimit := limit * 3
	if fetchLimit < 50 {
		fetchLimit = 50
	}
	const ftsQ = `SELECT ` + knowledgeSelectColsKI + `, fts.rank
		FROM knowledge_items_fts fts
		JOIN knowledge_items ki ON ki.rowid = fts.rowid
		WHERE knowledge_items_fts MATCH ?1
		  AND ki.archived_at IS NULL
		  AND ki.parent_id IS NULL
		  AND COALESCE(ki.heading_level, 0) = 0
		  AND (?2 IS NULL OR ki.workspace_id = ?2)
		ORDER BY fts.rank
		LIMIT ?3`
	rows, err := s.db.conn.QueryContext(ctx, ftsQ, ftsQuery, s.db.workspaceArg(), fetchLimit)
	if err != nil {
		return nil, errWrap("SearchCoarse", err)
	}
	defer func() { _ = rows.Close() }()
	var items []db.KnowledgeItem
	for rows.Next() {
		var rank float64
		item, scanErr := scanKnowledgeItem(func(dest ...any) error {
			return rows.Scan(append(dest, &rank)...)
		})
		if scanErr != nil {
			return nil, errWrap("SearchCoarse scan", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errWrap("SearchCoarse iter", err)
	}

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
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

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

// UpdateLearningValue sets the star-rating (1–5) for the given knowledge item.
// Returns knowledge.ErrNotFound when no row matches within the workspace scope.
func (s *KnowledgeStore) UpdateLearningValue(ctx context.Context, id uuid.UUID, value int) error {
	const q = `UPDATE knowledge_items
		SET learning_value = ?1, updated_at = ?2
		WHERE id = ?3
		  AND (?4 IS NULL OR workspace_id = ?4)`
	res, err := s.db.conn.ExecContext(ctx, q, value, sqliteNowMillis(), id.String(), s.db.workspaceArg())
	if err != nil {
		return errWrap("UpdateLearningValue", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return errWrap("UpdateLearningValue rows", err)
	}
	if n == 0 {
		return knowledge.ErrNotFound
	}
	return nil
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
		// SECURITY/correctness: scan list MUST match knowledgeSelectCols (22 cols)
		// + trailing embedding BLOB = 23 destinations. Migration 000019 added
		// 5 decay columns; migration 000027 added 3 hierarchy columns
		// (parent_id, heading_path, heading_level); migration 000049 added 3
		// cross-domain reference columns (project_id, task_id, decision_id).
		// A shorter scan silently fails and returns zero results (security audit C-1).
		var item db.KnowledgeItem
		var idStr string
		var urlNS, tagsNS, createdNS, updatedNS, workspaceNS sql.NullString
		var learningValue sql.NullInt32
		var lastRecalledNS, archivedNS sql.NullString
		var parentIDNS, headingPathNS sql.NullString
		var headingLevelNS sql.NullInt32
		var projectIDNS, taskIDNS, decisionIDNS sql.NullString
		var rawEmbed []byte
		if err := rows.Scan(
			&idStr, &item.Type, &item.Title, &item.Content, &urlNS, &tagsNS,
			&createdNS, &updatedNS, &item.Source, &learningValue, &workspaceNS,
			&item.Importance, &item.RecallCount, &lastRecalledNS, &item.BaseLambda, &archivedNS,
			&parentIDNS, &headingPathNS, &headingLevelNS,
			&projectIDNS, &taskIDNS, &decisionIDNS,
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
		item.ParentID = pgtypeUUID(nsString(parentIDNS))
		if headingPathNS.Valid {
			item.HeadingPath = pgtype.Text{String: headingPathNS.String, Valid: true}
		}
		if headingLevelNS.Valid {
			item.HeadingLevel = pgtype.Int4{Int32: headingLevelNS.Int32, Valid: true}
		}
		item.ProjectID = pgtypeUUID(nsString(projectIDNS))
		item.TaskID = pgtypeUUID(nsString(taskIDNS))
		item.DecisionID = pgtypeUUID(nsString(decisionIDNS))

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

// ListChildren returns direct children of parentID ordered by heading_level, created_at.
// Used by navigate_knowledge and outline_knowledge MCP tools.
func (s *KnowledgeStore) ListChildren(ctx context.Context, parentID uuid.UUID) ([]*db.KnowledgeItem, error) {
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE parent_id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY COALESCE(heading_level, 0) ASC, created_at ASC`
	items, err := s.list(ctx, "ListChildren", q, parentID.String(), s.db.workspaceArg())
	if err != nil {
		return nil, err
	}
	result := make([]*db.KnowledgeItem, len(items))
	for i := range items {
		cp := items[i]
		result[i] = &cp
	}
	return result, nil
}

// ListByProjectID returns knowledge items associated with a project UUID.
// Added in migration 000049; no FK constraint (CLAUDE.md red-line §9).
// SECURITY: workspace-scoped.
func (s *KnowledgeStore) ListByProjectID(ctx context.Context, projectID uuid.UUID, limit int) ([]db.KnowledgeItem, error) {
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE project_id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC, id DESC
		LIMIT ?3`
	return s.list(ctx, "ListKnowledgeByProjectID", q, projectID.String(), s.db.workspaceArg(), limit)
}

// ListByTaskID returns knowledge items associated with a task UUID.
// Added in migration 000049; no FK constraint (CLAUDE.md red-line §9).
// SECURITY: workspace-scoped.
func (s *KnowledgeStore) ListByTaskID(ctx context.Context, taskID uuid.UUID, limit int) ([]db.KnowledgeItem, error) {
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE task_id = ?1
		  AND (?2 IS NULL OR workspace_id = ?2)
		ORDER BY created_at DESC, id DESC
		LIMIT ?3`
	return s.list(ctx, "ListKnowledgeByTaskID", q, taskID.String(), s.db.workspaceArg(), limit)
}

// ListRoots returns top-level items (parent_id IS NULL) ordered by creation
// date descending. Used by navigate_knowledge when no parent_id is supplied.
func (s *KnowledgeStore) ListRoots(ctx context.Context) ([]*db.KnowledgeItem, error) {
	const q = `SELECT ` + knowledgeSelectCols + ` FROM knowledge_items
		WHERE parent_id IS NULL
		  AND (?1 IS NULL OR workspace_id = ?1)
		ORDER BY created_at DESC`
	items, err := s.list(ctx, "ListRoots", q, s.db.workspaceArg())
	if err != nil {
		return nil, err
	}
	result := make([]*db.KnowledgeItem, len(items))
	for i := range items {
		cp := items[i]
		result[i] = &cp
	}
	return result, nil
}
