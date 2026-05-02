// wbt-context is the Claude Code SessionStart hook binary.
//
// When Claude Code starts a new session it invokes the configured SessionStart
// hook. wbt-context queries the wayneblacktea database and emits a JSON object
// with a "systemMessage" field that Claude Code injects into the first user
// message, priming the model with:
//
//   - The most recent unresolved session handoff (intent + summary_text)
//   - The 5 most recent architectural decisions
//   - Due spaced-repetition reviews (concept titles only)
//
// Subcommand: `wbt-context session-start`
//
// Output on stdout: {"systemMessage": "context: ..."} (Claude Code hook spec).
// Exit 0 always; errors are logged to stderr and produce an empty systemMessage
// so the session is never blocked.
package main

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
)

const (
	// cosineSimilarityThreshold is the minimum cosine similarity for a recalled
	// item to be injected into the session context.
	// V1 placeholder: hashed embeddings are not semantic so this threshold is
	// intentionally low (effectively "take top-K regardless of similarity").
	// TODO(5/5): raise to 0.5 when using a real semantic embedding provider.
	cosineSimilarityThreshold = -1.0 // disabled for hashed v1 — take all top-K

	// recallTopK is the number of similar items to inject per store.
	recallTopK = 1

	// contextWindowChars is the approximate char budget for injected recall lines.
	contextWindowChars = 800
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "session-start" {
		fmt.Fprintf(os.Stderr, "usage: wbt-context session-start\n")
		// Exit 0 so an unknown subcommand never blocks Claude Code hooks.
		os.Exit(0)
	}
	runSessionStart()
}

// sessionStartOutput matches the Claude Code hook spec: a JSON object with a
// "systemMessage" string that is prepended to the first user message.
type sessionStartOutput struct {
	SystemMessage string `json:"systemMessage"`
}

func runSessionStart() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = dsnFromFallback()
	}
	if dsn == "" {
		emitContext("")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		slog.Warn("wbt-context: failed to parse DSN", "err", err)
		emitContext("")
		return
	}
	tlsCfg, tlsErr := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if tlsErr != nil {
		slog.Warn("wbt-context: TLS config error", "err", tlsErr)
		emitContext("")
		return
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		slog.Warn("wbt-context: DB connection failed", "err", err)
		emitContext("")
		return
	}
	defer pool.Close()

	wsID := workspaceFromEnv()
	msg := buildContextMessage(ctx, pool, wsID)
	emitContext(msg)
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
	// V1: uses hashed embedding (deterministic SHA-256, not semantic).
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
func fetchSemanticRecall(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID) string {
	// Build query embedding from the latest unresolved handoff's summary_text.
	queryText := fetchLatestHandoffSummaryText(ctx, pool, wsID)
	if strings.TrimSpace(queryText) == "" {
		return "" // nothing to embed → skip recall
	}

	embedder := localai.NewEmbeddingProvider()
	queryVec, err := embedder.Embed(queryText)
	if err != nil || len(queryVec) == 0 {
		slog.Warn("wbt-context: embedding failed for semantic recall", "err", err)
		return ""
	}

	var lines []string
	total := 0

	// Top-1 similar handoff (other than the latest one, which is already shown).
	for _, h := range fetchCosineSimilarHandoffs(ctx, pool, wsID, queryVec, recallTopK+1) {
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

	// Top-1 similar decision.
	for _, d := range fetchCosineSimilarDecisions(ctx, pool, wsID, queryVec, recallTopK) {
		line := "- [decision] " + d.title + ": " + truncate(d.decision, 100)
		if total+len(line) > contextWindowChars {
			break
		}
		lines = append(lines, line)
		total += len(line)
	}

	// Top-1 similar knowledge item.
	for _, k := range fetchCosineSimilarKnowledge(ctx, pool, wsID, queryVec, recallTopK) {
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
		if sim < cosineSimilarityThreshold {
			continue
		}
		candidates = append(candidates, scored{item: it, sim: sim})
	}

	sortBySimDesc(candidates, func(c scored) float64 { return c.sim })
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
//
// SECURITY: filtered by workspace_id via queryCosineCandidates.
func fetchCosineSimilarHandoffs(
	ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, queryVec []float32, limit int,
) []handoffRecallRow {
	const q = `SELECT intent, COALESCE(summary_text, ''), embedding
		FROM session_handoffs
		WHERE embedding IS NOT NULL
		  AND resolved_at IS NOT NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT 200`
	items := queryCosineCandidates(ctx, pool, q, queryVec, limit, "wbt-context: handoff cosine query failed", uuidArg(wsID))
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
//
// SECURITY: filtered by workspace_id via queryCosineCandidates.
func fetchCosineSimilarDecisions(
	ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, queryVec []float32, limit int,
) []decisionRecallRow {
	const q = `SELECT title, decision, embedding
		FROM decisions
		WHERE embedding IS NOT NULL
		  AND ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT 200`
	items := queryCosineCandidates(ctx, pool, q, queryVec, limit, "wbt-context: decision cosine query failed", uuidArg(wsID))
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

// fetchCosineSimilarKnowledge fetches the most recent knowledge items.
//
// V1 NOTE: knowledge_items.embedding uses Gemini 768-dim vectors while the new
// hashed EmbeddingProvider produces 32-dim vectors.  Cosine similarity across
// different-dim vectors is always 0 (CosineSimilarity returns 0 on dim mismatch).
// For v1 we fall back to recency-order for knowledge recall.
// TODO(5/5): switch to a unified embedding provider (same dims across all stores)
// and enable true cosine recall for knowledge items too.
//
// SECURITY: filtered by workspace_id.
func fetchCosineSimilarKnowledge(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, _ []float32, limit int) []knowledgeRecallRow {
	// V1: recency-based fallback — dims differ between knowledge (768) and hashed (32).
	const q = `SELECT title, content
		FROM knowledge_items
		WHERE ($1::uuid IS NULL OR workspace_id = $1)
		ORDER BY created_at DESC
		LIMIT $2`
	rows, err := pool.Query(ctx, q, uuidArg(wsID), limit)
	if err != nil {
		slog.Warn("wbt-context: knowledge recall query failed", "err", err)
		return nil
	}
	defer rows.Close()

	var result []knowledgeRecallRow
	for rows.Next() {
		var title, content string
		if err := rows.Scan(&title, &content); err != nil {
			continue
		}
		result = append(result, knowledgeRecallRow{title: title, content: content})
	}
	return result
}

// sortBySimDesc sorts a slice of any type by descending similarity score.
// Uses a simple insertion-sort-style swap (table sizes ≤ 200, acceptable).
func sortBySimDesc[T any](s []T, score func(T) float64) {
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

func emitContext(msg string) {
	out, _ := json.Marshal(sessionStartOutput{SystemMessage: msg})
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

func workspaceFromEnv() *uuid.UUID {
	raw := strings.TrimSpace(os.Getenv("WORKSPACE_ID"))
	if raw == "" {
		return nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil
	}
	return &id
}

func dsnFromFallback() string {
	candidates := []string{
		"/Users/waynechen/_project/wayneblacktea/.env.local",
		"/Users/waynechen/_project/wayneblacktea/.env",
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p) //nolint:gosec // candidates is a hard-coded allowlist, not user input
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "DATABASE_URL=") {
				return strings.TrimPrefix(line, "DATABASE_URL=")
			}
		}
	}
	return ""
}
