// wbt-doctor connects to the wayneblacktea Postgres database and emits a
// JSON snapshot of personal-OS health (stuck in-progress tasks, pending
// proposal queue depth, due reviews) plus a list of human-readable
// "forgotten signals" — short strings flagging likely Claude omissions.
//
// Designed to run as a Claude Code Stop hook so the next SessionStart can
// surface the previous session's open loops without depending on the live
// MCP process (which is gone by Stop time).
//
// Output:
//   - JSON to stdout (parseable by SessionStart hook / claude-hud)
//   - Forgotten signals also written to stderr in human-readable form
//   - Exit 0 always (do not block Stop)
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type snapshot struct {
	GeneratedAt      time.Time `json:"generated_at"`
	Workspace        string    `json:"workspace,omitempty"`
	StuckTasks       []string  `json:"stuck_task_ids,omitempty"`
	StuckCount       int       `json:"stuck_count"`
	InProgressCount  int       `json:"in_progress_count"`
	PendingProposals int       `json:"pending_proposals"`
	DueReviews       int       `json:"due_reviews"`
	ForgottenSignals []string  `json:"forgotten_signals,omitempty"`
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		// Fall back to .env.local in the wayneblacktea repo (Stop hook runs
		// without the project env). Best-effort; silent if missing.
		dsn = dsnFromFallback()
	}
	if dsn == "" {
		// No DSN reachable → emit empty snapshot so the consumer can still
		// read JSON without erroring.
		emit(snapshot{GeneratedAt: time.Now().UTC()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		emit(snapshot{GeneratedAt: time.Now().UTC()})
		return
	}
	cfg.ConnConfig.TLSConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // G402: doctor targets self-signed certs
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		emit(snapshot{GeneratedAt: time.Now().UTC()})
		return
	}
	defer pool.Close()

	wsID := workspaceFromEnv()
	snap := snapshot{GeneratedAt: time.Now().UTC()}
	if wsID != nil {
		snap.Workspace = wsID.String()
	}

	stuckThreshold := 4 * time.Hour
	collectTaskHealth(ctx, pool, wsID, stuckThreshold, &snap)
	collectProposalCount(ctx, pool, wsID, &snap)
	collectDueReviewCount(ctx, pool, wsID, &snap)

	snap.ForgottenSignals = detectSignals(snap)

	emit(snap)
}

func collectTaskHealth(ctx context.Context, pool *pgxpool.Pool, ws *uuid.UUID, threshold time.Duration, snap *snapshot) {
	const q = `SELECT id, updated_at FROM tasks
		WHERE status = 'in_progress'
		  AND ($1::uuid IS NULL OR workspace_id = $1)`
	rows, err := pool.Query(ctx, q, ws)
	if err != nil {
		return
	}
	defer rows.Close()
	cutoff := time.Now().Add(-threshold)
	for rows.Next() {
		var id uuid.UUID
		var updatedAt time.Time
		if err := rows.Scan(&id, &updatedAt); err != nil {
			continue
		}
		snap.InProgressCount++
		if updatedAt.Before(cutoff) {
			snap.StuckCount++
			snap.StuckTasks = append(snap.StuckTasks, id.String())
		}
	}
}

func collectProposalCount(ctx context.Context, pool *pgxpool.Pool, ws *uuid.UUID, snap *snapshot) {
	const q = `SELECT COUNT(*) FROM pending_proposals
		WHERE status = 'pending'
		  AND ($1::uuid IS NULL OR workspace_id = $1)`
	_ = pool.QueryRow(ctx, q, ws).Scan(&snap.PendingProposals)
}

func collectDueReviewCount(ctx context.Context, pool *pgxpool.Pool, ws *uuid.UUID, snap *snapshot) {
	const q = `SELECT COUNT(*) FROM concepts c
		JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE rs.due_date <= NOW()
		  AND ($1::uuid IS NULL OR c.workspace_id = $1)`
	_ = pool.QueryRow(ctx, q, ws).Scan(&snap.DueReviews)
}

func detectSignals(s snapshot) []string {
	var sig []string
	if s.StuckCount > 0 {
		sig = append(sig, fmt.Sprintf("%d stuck in-progress task(s) — likely missing complete_task call", s.StuckCount))
	}
	if s.PendingProposals >= 5 {
		sig = append(sig, fmt.Sprintf("%d pending proposals queued — triage backlog", s.PendingProposals))
	}
	if s.DueReviews > 0 {
		sig = append(sig, fmt.Sprintf("%d concept(s) due for review today", s.DueReviews))
	}
	return sig
}

func emit(s snapshot) {
	out, _ := json.MarshalIndent(s, "", "  ")
	fmt.Println(string(out))

	// Mirror signals to stderr so the Stop-hook log surfaces them.
	for _, msg := range s.ForgottenSignals {
		fmt.Fprintln(os.Stderr, "⚠ "+msg)
	}
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
	_ = errors.New("no fallback DSN")
	return ""
}
