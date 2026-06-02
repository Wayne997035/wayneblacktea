// SessionStart hook logic for `wbt context session-start` (formerly
// the standalone wbt-context binary). Emits a JSON object with a
// "systemMessage" field that Claude Code injects into the first user
// message of a new session.
//
// Exit semantics: this function returns nil unconditionally so the wbt
// dispatcher (and the wbt-context shim) always exit 0 — Claude Code MUST
// never be blocked by a hook error. All failures are logged via slog to
// a 0600 file in os.TempDir.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

const (
	// contextCosineSimilarityThreshold is the minimum cosine similarity for a
	// recalled session to be injected into context. Requires GEMINI_API_KEY to
	// be set so real semantic embeddings are stored; falls back to showing the
	// most recent handoff when no embeddings exist.
	contextCosineSimilarityThreshold = 0.5

	// contextRecallTopK is the number of similar sessions to inject per recall pass.
	contextRecallTopK = 3

	// contextWindowChars is the approximate char budget for injected recall lines.
	contextWindowChars = 800
)

// RunContext dispatches `wbt context <subcmd>`. args is os.Args[2:] (subcmd
// onward). Currently supports only `session-start`. Returns nil so wbt always
// exits 0 — Claude Code MUST never be blocked by a hook error.
func RunContext(args []string) error {
	InitHookSlog("wbt-context")
	if len(args) < 1 || args[0] != "session-start" {
		fmt.Fprintf(os.Stderr, "usage: wbt context session-start\n")
		// Exit 0 so an unknown subcommand never blocks Claude Code hooks.
		return nil
	}
	runSessionStart()
	return nil
}

// SessionStartOutput matches the Claude Code hook spec: a JSON object with a
// "systemMessage" string that is prepended to the first user message.
type SessionStartOutput struct {
	SystemMessage string `json:"systemMessage"`
}

func runSessionStart() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = DSNFromFallback()
	}
	if dsn == "" {
		EmitContext("")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		slog.Warn("wbt-context: failed to parse DSN", "err", err)
		EmitContext("")
		return
	}
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Second
	tlsCfg, tlsErr := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if tlsErr != nil {
		slog.Warn("wbt-context: TLS config error", "err", tlsErr)
		EmitContext("")
		return
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Warn("wbt-context: DB connection failed", "err", err)
		EmitContext("")
		return
	}
	defer pool.Close()

	wsID := WorkspaceFromEnv()
	msg := buildContextMessage(ctx, pool, wsID)
	EmitContext(msg)
}

// buildContextMessage queries DB and assembles the context string.
func buildContextMessage(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID) string {
	var parts []string

	// 1. Latest unresolved handoff.
	if h := fetchLatestHandoff(ctx, pool, wsID); h != "" {
		parts = append(parts, "## Last session handoff\n"+h)
	}

	// 2. Recent decisions (last 5).
	if d := fetchRecentDecisions(ctx, pool, wsID, 5); d != "" {
		parts = append(parts, "## Recent decisions\n"+d)
	}

	// 3. Due reviews (concept titles only, limit 10).
	if r := fetchDueReviews(ctx, pool, wsID, 10); r != "" {
		parts = append(parts, "## Due reviews\n"+r)
	}

	// 4. Semantic recall: top-K similar handoffs, decisions, and knowledge items.
	// Uses real Gemini 768-dim semantic embeddings (requires GEMINI_API_KEY).
	// The query embedding is derived from the last session handoff summary text
	// so the recall is contextualised to the previous session.
	if recall := fetchSemanticRecall(ctx, pool, wsID); recall != "" {
		parts = append(parts, "## Relevant past context\n"+recall)
	}

	if len(parts) == 0 {
		return ""
	}
	return "context:\n\n" + strings.Join(parts, "\n\n")
}

// fetchSemanticRecall builds a query embedding from the latest handoff summary
// and returns the top-K similar handoffs, decisions, and knowledge items.
// Returns "" when no embeddings exist or on any error (best-effort).
// Uses the real Gemini 768-dim provider (default); falls back to hashed 32-dim
// when GEMINI_API_KEY is absent (fail-soft from NewEmbeddingProvider).
// Only rows whose embedding_provider matches the current provider are searched,
// preventing cross-provider dimension mismatches (CosineSimilarity returns 0).
func fetchSemanticRecall(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID) string {
	// Build query embedding from the latest unresolved handoff's summary_text.
	queryText := fetchLatestHandoffSummaryText(ctx, pool, wsID)
	if strings.TrimSpace(queryText) == "" {
		return "" // nothing to embed → skip recall
	}

	// NewEmbeddingProvider defaults to Gemini 768-dim; falls back to hashed with Warn.
	embedder, err := localai.NewEmbeddingProvider()
	if err != nil {
		slog.Warn("wbt-context: unknown embedding provider", "err", err)
		return ""
	}
	queryVec, err := embedder.Embed(queryText)
	if err != nil || len(queryVec) == 0 {
		slog.Warn("wbt-context: embedding failed for semantic recall", "err", err)
		return ""
	}

	// Determine the active provider tag so recall queries can filter to same-provider rows.
	activeProvider := localai.ProviderTagFromDim(len(queryVec))

	var lines []string
	total := 0

	// Top-K similar handoffs (filtered to same provider to prevent dim-mismatch zero scores).
	for _, h := range fetchCosineSimilarHandoffs(ctx, pool, wsID, queryVec, activeProvider, contextRecallTopK+1) {
		text := h.intent
		if h.summaryText != "" {
			text += ": " + truncate(h.summaryText, 100)
		}
		line := "- [handoff] " + text
		if total+len(line) > contextWindowChars {
			break
		}
		lines = append(lines, line)
		total += len(line)
	}

	// Top-K similar decisions.
	// NOTE: decisions.embedding has no active writer in this sprint (the writer is a follow-up
	// task). The SearchByCosine path falls back to recency order when no embeddings exist.
	// Provider filter is still applied so this is ready when the writer ships.
	for _, d := range fetchCosineSimilarDecisions(ctx, pool, wsID, queryVec, activeProvider, contextRecallTopK) {
		line := "- [decision] " + d.title + ": " + truncate(d.decision, 100)
		if total+len(line) > contextWindowChars {
			break
		}
		lines = append(lines, line)
		total += len(line)
	}

	// Top-K similar knowledge items via pgvector native cosine. knowledge_items.embedding is
	// vector(768) written by the real Gemini provider (factory.go:189). No provider filter:
	// every knowledge row is real Gemini 768-dim, so there is no mixed-space hazard.
	for _, k := range fetchCosineSimilarKnowledge(ctx, pool, wsID, queryVec, contextRecallTopK) {
		line := "- [knowledge] " + k.title + ": " + truncate(k.content, 100)
		if total+len(line) > contextWindowChars {
			break
		}
		lines = append(lines, line)
		total += len(line)
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// fetchLatestHandoffSummaryText returns the summary_text of the most recent
// unresolved handoff.  Returns "" when none exists.
func fetchLatestHandoffSummaryText(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID) string {
	const q = `SELECT COALESCE(summary_text, '') FROM session_handoffs
		WHERE resolved_at IS NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT 1`
	var text string
	if err := pool.QueryRow(ctx, q, uuidArg(wsID)).Scan(&text); err != nil {
		return ""
	}
	return text
}

type handoffRecallRow struct {
	intent      string
	summaryText string
}

// recallItem is an intermediate type for the cosine recall pipeline.
type recallItem struct {
	col1, col2 string // two text columns (e.g. intent+summary, title+decision)
	embedding  []byte
}

// queryCosineCandidates executes a raw SQL query that returns (col1, col2, embedding BYTEA),
// deserializes embeddings, computes cosine similarity, and returns the top-limit results
// sorted by descending similarity.  The scan expects exactly these 3 columns.
//
// SECURITY: query MUST include workspace_id scoping (enforced by each call site).
func queryCosineCandidates(
	ctx context.Context, pool *pgxpool.Pool, query string,
	queryVec []float32, limit int, logWarnMsg string, args ...any,
) []recallItem {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		slog.Warn(logWarnMsg, "err", err)
		return nil
	}
	defer rows.Close()

	type scored struct {
		item recallItem
		sim  float64
	}
	var candidates []scored
	for rows.Next() {
		var it recallItem
		if err := rows.Scan(&it.col1, &it.col2, &it.embedding); err != nil {
			continue
		}
		vec := localai.DeserializeEmbedding(it.embedding)
		if vec == nil {
			continue
		}
		sim := localai.CosineSimilarity(queryVec, vec)
		if sim < contextCosineSimilarityThreshold {
			continue
		}
		candidates = append(candidates, scored{item: it, sim: sim})
	}

	SortBySimDesc(candidates, func(c scored) float64 { return c.sim })
	if limit < len(candidates) {
		candidates = candidates[:limit]
	}
	result := make([]recallItem, 0, len(candidates))
	for _, c := range candidates {
		result = append(result, c.item)
	}
	return result
}

// fetchCosineSimilarHandoffs fetches up to limit resolved handoffs with non-null
// embeddings sorted by cosine similarity to queryVec.
// The most recent unresolved handoff is excluded (already shown above).
// Only rows with embedding_provider matching activeProvider are searched to prevent
// cross-provider dimension mismatches that return zero cosine scores.
//
// SECURITY: filtered by workspace_id via queryCosineCandidates.
func fetchCosineSimilarHandoffs(
	ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, queryVec []float32, activeProvider string, limit int,
) []handoffRecallRow {
	// Filter by embedding_provider to exclude legacy 'hashed' rows when real provider is active.
	const q = `SELECT intent, COALESCE(summary_text, ''), embedding
		FROM session_handoffs
		WHERE embedding IS NOT NULL
		  AND resolved_at IS NOT NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND (embedding_provider = $2 OR embedding_provider IS NULL AND $2 = 'hashed')
		ORDER BY created_at DESC
		LIMIT 200`
	items := queryCosineCandidates(ctx, pool, q, queryVec, limit, "wbt-context: handoff cosine query failed", uuidArg(wsID), activeProvider)
	result := make([]handoffRecallRow, 0, len(items))
	for _, it := range items {
		result = append(result, handoffRecallRow{intent: it.col1, summaryText: it.col2})
	}
	return result
}

type decisionRecallRow struct {
	title    string
	decision string
}

// fetchCosineSimilarDecisions fetches decisions sorted by cosine similarity.
// Provider-filtered to exclude legacy 'hashed' rows when real provider is active.
//
// NOTE: decisions.embedding has no active writer yet (follow-up task). When no
// embedding rows exist this function returns an empty slice and the caller silently
// skips decisions from semantic recall (recency-only behaviour is preserved).
//
// SECURITY: filtered by workspace_id via queryCosineCandidates.
func fetchCosineSimilarDecisions(
	ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, queryVec []float32, activeProvider string, limit int,
) []decisionRecallRow {
	const q = `SELECT title, decision, embedding
		FROM decisions
		WHERE embedding IS NOT NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		  AND (embedding_provider = $2 OR embedding_provider IS NULL AND $2 = 'hashed')
		ORDER BY created_at DESC
		LIMIT 200`
	items := queryCosineCandidates(ctx, pool, q, queryVec, limit, "wbt-context: decision cosine query failed", uuidArg(wsID), activeProvider)
	result := make([]decisionRecallRow, 0, len(items))
	for _, it := range items {
		result = append(result, decisionRecallRow{title: it.col1, decision: it.col2})
	}
	return result
}

type knowledgeRecallRow struct {
	title   string
	content string
}

// fetchCosineSimilarKnowledge fetches knowledge items most similar to queryVec using
// pgvector's native <=> operator (DB-side cosine + ORDER BY). knowledge_items.embedding is
// vector(768) written by the real Gemini provider (factory.go:189), so recall works when
// GEMINI_API_KEY is set. Unlike handoffs/decisions (BYTEA brute-force via
// queryCosineCandidates), this table is native pgvector and has NO embedding_provider column
// — every row is real Gemini 768-dim, so no provider filter and no mixed-space hazard.
//
// SECURITY: filtered by workspace_id in the query.
func fetchCosineSimilarKnowledge(
	ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID,
	queryVec []float32, limit int,
) []knowledgeRecallRow {
	const q = `SELECT title, content
		FROM knowledge_items
		WHERE embedding IS NOT NULL
		  AND ($2::uuid IS NULL OR workspace_id = $2)
		  AND (1 - (embedding <=> $1::vector)) >= $4
		ORDER BY embedding <=> $1::vector
		LIMIT $3`
	rows, err := pool.Query(ctx, q, pgvector.NewVector(queryVec), uuidArg(wsID), limit, contextCosineSimilarityThreshold)
	if err != nil {
		slog.Warn("wbt-context: knowledge cosine query failed", "err", err)
		return nil
	}
	defer rows.Close()
	result := make([]knowledgeRecallRow, 0, limit)
	for rows.Next() {
		var r knowledgeRecallRow
		if err := rows.Scan(&r.title, &r.content); err != nil {
			continue
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("wbt-context: knowledge cosine rows iteration failed", "err", err)
	}
	return result
}

// SortBySimDesc sorts a slice of any type by descending similarity score.
// Uses a simple insertion-sort-style swap (table sizes <= 200, acceptable).
// Exported for test access.
func SortBySimDesc[T any](s []T, score func(T) float64) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if score(s[j]) > score(s[i]) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

type handoffRow struct {
	Intent      string
	SummaryText *string
	CreatedAt   time.Time
}

func fetchLatestHandoff(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID) string {
	const q = `SELECT intent, summary_text, created_at FROM session_handoffs
		WHERE resolved_at IS NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT 1`
	row := pool.QueryRow(ctx, q, uuidArg(wsID))
	var h handoffRow
	if err := row.Scan(&h.Intent, &h.SummaryText, &h.CreatedAt); err != nil {
		return ""
	}
	out := fmt.Sprintf("- Intent: %s (created %s)", h.Intent, h.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"))
	if h.SummaryText != nil && *h.SummaryText != "" {
		out += "\n- Summary: " + *h.SummaryText
	}
	return out
}

type decisionRow struct {
	Title    string
	RepoName *string
	Decision string
}

func fetchRecentDecisions(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, limit int) string {
	const q = `SELECT title, repo_name, decision FROM decisions
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT $2`
	rows, err := pool.Query(ctx, q, uuidArg(wsID), limit)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var d decisionRow
		if err := rows.Scan(&d.Title, &d.RepoName, &d.Decision); err != nil {
			continue
		}
		repo := ""
		if d.RepoName != nil && *d.RepoName != "" {
			repo = " [" + *d.RepoName + "]"
		}
		lines = append(lines, fmt.Sprintf("- %s%s: %s", d.Title, repo, truncate(d.Decision, 120)))
	}
	return strings.Join(lines, "\n")
}

func fetchDueReviews(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, limit int) string {
	const q = `SELECT c.title FROM concepts c
		JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE rs.due_date <= NOW()
		  AND c.status = 'active'
		  AND ($1::uuid IS NULL OR c.workspace_id = $1)
		ORDER BY rs.due_date ASC
		LIMIT $2`
	rows, err := pool.Query(ctx, q, uuidArg(wsID), limit)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var titles []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			continue
		}
		titles = append(titles, "- "+title)
	}
	return strings.Join(titles, "\n")
}

// EmitContext writes the SessionStart hook JSON envelope to stdout.
// Exported for test access.
func EmitContext(msg string) {
	out, _ := json.Marshal(SessionStartOutput{SystemMessage: msg})
	fmt.Println(string(out))
}

// uuidArg converts *uuid.UUID to an interface{} suitable for pgx placeholder.
// nil maps to nil so that `($1::uuid IS NULL OR ...)` matches all rows.
func uuidArg(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id
}

func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}
