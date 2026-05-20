// Stop hook + health snapshot logic for `wbt doctor` (formerly the standalone
// wbt-doctor binary). Connects to the wayneblacktea Postgres database and
// emits a JSON snapshot of personal-OS health (stuck in-progress tasks,
// pending proposal queue depth, due reviews) plus a list of human-readable
// "forgotten signals" — short strings flagging likely Claude omissions.
//
// Designed to run as a Claude Code Stop hook so the next SessionStart can
// surface the previous session's open loops without depending on the live
// MCP process (which is gone by Stop time).
//
// Stop hook upgrade (session lifecycle): if stdin contains a Claude Code
// transcript JSON (non-empty), the doctor:
//  1. Calls Haiku to produce a ≤500-char plain-text summary.
//  2. Writes the summary to session_handoffs.summary_text (best-effort).
//  3. Saves the summary as a zettelkasten knowledge_item (type=zettelkasten,
//     source=auto-summary) for long-term searchable recall.
//
// Output:
//   - JSON to stdout (parseable by SessionStart hook / claude-hud)
//   - Forgotten signals also written to stderr in human-readable form
//   - Always returns nil so wbt (and the wbt-doctor shim) exits 0
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	localai "github.com/Wayne997035/wayneblacktea/internal/ai"
	"github.com/Wayne997035/wayneblacktea/internal/storage"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// doctorEmbeddingSummaryCap is the character limit used when embedding a
// session summary. Matches sessionSummaryMaxChars in summarizer.go.
const doctorEmbeddingSummaryCap = 500

// doctorMaxStdinBytes caps the transcript read from stdin to avoid OOM on
// unexpectedly large payloads. Claude Code transcripts are typically <10 MB.
const doctorMaxStdinBytes = 16 * 1024 * 1024 // 16 MB

// DoctorSnapshot is the JSON envelope written to stdout by `wbt doctor`.
// Exported so tests can construct fixtures without leaving the package.
type DoctorSnapshot struct {
	GeneratedAt      time.Time `json:"generated_at"`
	Workspace        string    `json:"workspace,omitempty"`
	StuckTasks       []string  `json:"stuck_task_ids,omitempty"`
	StuckCount       int       `json:"stuck_count"`
	InProgressCount  int       `json:"in_progress_count"`
	PendingProposals int       `json:"pending_proposals"`
	DueReviews       int       `json:"due_reviews"`
	ForgottenSignals []string  `json:"forgotten_signals,omitempty"`
	SessionSummary   string    `json:"session_summary,omitempty"`
}

// RunDoctor dispatches `wbt doctor`. args is unused but kept for the standard
// Run<X>(args []string) error signature. Always returns nil so wbt (and the
// wbt-doctor shim) exits 0 — the Stop hook MUST never block Claude Code.
func RunDoctor(_ []string) error {
	InitHookSlog("wbt-doctor")
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = DSNFromFallback()
	}
	if dsn == "" {
		emitDoctor(DoctorSnapshot{GeneratedAt: time.Now().UTC()})
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		emitDoctor(DoctorSnapshot{GeneratedAt: time.Now().UTC()})
		return nil
	}
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 30 * time.Second
	tlsCfg, tlsErr := storage.BuildTLSConfig(os.Getenv("APP_ENV"), os.Getenv("PGSSLROOTCERT"))
	if tlsErr != nil {
		slog.Error("doctor DB TLS config failed; emitting empty snapshot", "err", tlsErr)
		emitDoctor(DoctorSnapshot{GeneratedAt: time.Now().UTC()})
		return nil
	}
	if tlsCfg != nil {
		cfg.ConnConfig.TLSConfig = tlsCfg
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		emitDoctor(DoctorSnapshot{GeneratedAt: time.Now().UTC()})
		return nil
	}
	defer pool.Close()

	wsID := WorkspaceFromEnv()
	snap := DoctorSnapshot{GeneratedAt: time.Now().UTC()}
	if wsID != nil {
		snap.Workspace = wsID.String()
	}

	stuckThreshold := 4 * time.Hour
	collectTaskHealth(ctx, pool, wsID, stuckThreshold, &snap)
	collectProposalCount(ctx, pool, wsID, &snap)
	collectDueReviewCount(ctx, pool, wsID, &snap)

	// Session summary: read stdin transcript, call Haiku, persist.
	// A separate 30 s budget so AI latency does not consume the 15 s DB timeout.
	// Using context.Background() here is intentional: the outer ctx will expire
	// before the AI call finishes; we want the AI goroutine to have its own budget.
	summaryCtx, summaryCancel := context.WithTimeout(
		context.Background(), 30*time.Second,
	)
	defer summaryCancel()
	summary := processDoctorSummary(ctx, summaryCtx, pool, wsID)
	snap.SessionSummary = summary

	snap.ForgottenSignals = DetectDoctorSignals(snap)

	emitDoctor(snap)
	return nil
}

// processDoctorSummary reads stdin, summarises the transcript with Haiku,
// writes the summary to session_handoffs.summary_text, and saves a
// zettelkasten knowledge item. Returns the summary text (empty string on any
// error or empty stdin). dbCtx is used for DB operations; aiCtx is the
// separate AI-call budget.
func processDoctorSummary(dbCtx, aiCtx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID) string {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, doctorMaxStdinBytes))
	if err != nil || len(raw) == 0 {
		return ""
	}

	// The Claude Code Stop hook sends a transcript JSON. Extract the messages
	// array if present; fall back to treating the whole stdin as plain text.
	transcript := parseDoctorTranscript(raw)
	if len(transcript) == 0 {
		return ""
	}

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		slog.Warn("doctor: ANTHROPIC_API_KEY not set; skipping session summary")
		return ""
	}

	summarizer := localai.New(apiKey)
	text, sumErr := summarizer.SummarizeSession(aiCtx, transcript)
	if sumErr != nil {
		slog.Warn("doctor: session summary failed", "err", sumErr)
		return ""
	}
	if text == "" {
		return ""
	}

	// Persist summary_text to the latest unresolved session handoff (best-effort).
	updateDoctorHandoffSummary(dbCtx, pool, wsID, text)

	// Embed the summary and write to session_handoffs.embedding (best-effort).
	// A fresh context is intentional: aiCtx may be near expiry and the
	// hashed embedding write (<1 ms) must not be coupled to the AI call timeout.
	embedCtx, embedCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer embedCancel()
	// Intentional fresh ctx: aiCtx is the AI-call budget and may be near
	// expiry; this DB embedding write needs its own short timeout.
	//nolint:contextcheck // intentional fresh embedCtx, see comment above
	writeDoctorHandoffEmbedding(embedCtx, pool, wsID, text)

	// Save as searchable zettelkasten knowledge item (best-effort).
	saveDoctorKnowledgeItem(dbCtx, pool, wsID, text)

	return text
}

// parseDoctorTranscript tries to decode a Claude Code hook transcript JSON
// envelope. If the input is not valid JSON or has no messages, it falls back
// to treating the entire stdin as a single user message so the summarizer
// still gets input.
func parseDoctorTranscript(raw []byte) []localai.Message {
	// Claude Code hook sends: {"transcript": [{"role":"user","content":"..."},...]}
	var envelope struct {
		Transcript []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"transcript"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Transcript) > 0 {
		msgs := make([]localai.Message, 0, len(envelope.Transcript))
		for _, m := range envelope.Transcript {
			msgs = append(msgs, localai.Message{Role: m.Role, Content: m.Content})
		}
		return msgs
	}

	// Fallback: treat entire stdin as a single user message.
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return nil
	}
	return []localai.Message{{Role: "user", Content: text}}
}

// writeDoctorHandoffEmbedding embeds the session summary and stores the bytes
// in the most recent unresolved session_handoffs.embedding column
// (best-effort).
//
// V1 PLACEHOLDER: uses HashedEmbeddingProvider (deterministic SHA-256 hash,
// not semantically meaningful).  Swap to a real provider in 5/5 sprint.
// Input is capped at doctorEmbeddingSummaryCap chars to match the summary length.
func writeDoctorHandoffEmbedding(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, summary string) {
	if summary == "" {
		return
	}
	// Cap to doctorEmbeddingSummaryCap characters (already capped by
	// SummarizeSession but be defensive here too).
	runes := []rune(summary)
	if len(runes) > doctorEmbeddingSummaryCap {
		summary = string(runes[:doctorEmbeddingSummaryCap])
	}

	embedder, err := localai.NewEmbeddingProvider()
	if err != nil {
		slog.Warn("doctor: unknown embedding provider", "err", err)
		return
	}
	vec, err := embedder.Embed(summary)
	if err != nil || len(vec) == 0 {
		slog.Warn("doctor: embedding generation failed", "err", err)
		return
	}

	embBytes := localai.SerializeEmbedding(vec)
	if embBytes == nil {
		return
	}

	var wsArg any
	if wsID != nil {
		wsArg = wsID
	}
	const q = `UPDATE session_handoffs
		SET embedding = $1
		WHERE id = (
			SELECT id FROM session_handoffs
			WHERE resolved_at IS NULL
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			ORDER BY created_at DESC
			LIMIT 1
		)`
	if _, err := pool.Exec(ctx, q, embBytes, wsArg); err != nil {
		slog.Warn("doctor: failed to write handoff embedding", "err", err)
	}
}

// updateDoctorHandoffSummary writes summary to the latest unresolved
// session_handoff.
func updateDoctorHandoffSummary(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, summary string) {
	var wsArg any
	if wsID != nil {
		wsArg = wsID
	}
	const q = `UPDATE session_handoffs
		SET summary_text = $1
		WHERE id = (
			SELECT id FROM session_handoffs
			WHERE resolved_at IS NULL
			  AND ($2::uuid IS NULL OR workspace_id = $2)
			ORDER BY created_at DESC
			LIMIT 1
		)`
	if _, err := pool.Exec(ctx, q, summary, wsArg); err != nil {
		slog.Warn("doctor: failed to update handoff summary_text", "err", err)
	}
}

// saveDoctorKnowledgeItem persists the session summary as a zettelkasten
// knowledge item.
//
// ARCHITECTURAL EXCEPTION (decision b1a87143 generally requires LLM-generated
// content to go through pending_proposals → user confirm). Stop-hook session
// summaries are exempted because:
//  1. They are factual digests of a session the operator just lived through
//     (low hallucination surface vs. the reflection cron's speculative
//     "lessons learned" output).
//  2. They are user-initiated by ending the session — no unattended cron.
//  3. They are length-capped (sessionSummaryMaxChars=500) and the prompt
//     explicitly forbids credentials in the output.
//  4. They are clearly labelled `source='auto-summary'` so the operator can
//     delete the row from the knowledge dashboard if a hallucination slips
//     through. The reflection cron has no such "after the fact" cleanup
//     because its proposals are gated up-front.
//
// If model output quality degrades or this gate is abused, route through
// pending_proposals with a TypeSessionSummary type instead.
func saveDoctorKnowledgeItem(ctx context.Context, pool *pgxpool.Pool, wsID *uuid.UUID, summary string) {
	var wsArg any
	if wsID != nil {
		wsArg = wsID
	}
	title := "Session summary " + time.Now().UTC().Format("2006-01-02 15:04")
	const q = `INSERT INTO knowledge_items
		(type, title, content, source, tags, workspace_id)
		VALUES ('zettelkasten', $1, $2, 'auto-summary', '[]', $3)`
	if _, err := pool.Exec(ctx, q, title, summary, wsArg); err != nil {
		slog.Warn("doctor: failed to save knowledge item", "err", err)
	}
}

func collectTaskHealth(ctx context.Context, pool *pgxpool.Pool, ws *uuid.UUID, threshold time.Duration, snap *DoctorSnapshot) {
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

func collectProposalCount(ctx context.Context, pool *pgxpool.Pool, ws *uuid.UUID, snap *DoctorSnapshot) {
	const q = `SELECT COUNT(*) FROM pending_proposals
		WHERE status = 'pending'
		  AND ($1::uuid IS NULL OR workspace_id = $1)`
	_ = pool.QueryRow(ctx, q, ws).Scan(&snap.PendingProposals)
}

func collectDueReviewCount(ctx context.Context, pool *pgxpool.Pool, ws *uuid.UUID, snap *DoctorSnapshot) {
	const q = `SELECT COUNT(*) FROM concepts c
		JOIN review_schedule rs ON rs.concept_id = c.id
		WHERE rs.due_date <= NOW()
		  AND ($1::uuid IS NULL OR c.workspace_id = $1)`
	_ = pool.QueryRow(ctx, q, ws).Scan(&snap.DueReviews)
}

// DetectDoctorSignals returns the forgotten-signal strings for snapshot s.
// Exported for test access.
func DetectDoctorSignals(s DoctorSnapshot) []string {
	var sig []string
	if s.StuckCount > 0 {
		sig = append(sig, fmt.Sprintf("%d stuck in-progress task(s) — likely missing complete_task call", s.StuckCount))
	}
	if active := s.InProgressCount - s.StuckCount; active > 0 {
		sig = append(sig, fmt.Sprintf("%d active in_progress task(s) — close with complete_task or set_session_handoff", active))
	}
	if s.PendingProposals >= 5 {
		sig = append(sig, fmt.Sprintf("%d pending proposals queued — triage backlog", s.PendingProposals))
	}
	if s.DueReviews > 0 {
		sig = append(sig, fmt.Sprintf("%d concept(s) due for review today", s.DueReviews))
	}
	return sig
}

func emitDoctor(s DoctorSnapshot) {
	out, _ := json.MarshalIndent(s, "", "  ")
	fmt.Println(string(out))

	// Mirror signals to stderr so the Stop-hook log surfaces them.
	for _, msg := range s.ForgottenSignals {
		fmt.Fprintln(os.Stderr, "WARNING: "+msg)
	}
}
