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

	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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

	if len(parts) == 0 {
		return ""
	}
	return "context:\n\n" + strings.Join(parts, "\n\n")
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
