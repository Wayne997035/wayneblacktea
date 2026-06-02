// Package aicost records Anthropic API token usage and computed USD cost.
//
// Design principles:
//   - Fire-and-forget: Record never returns an error to the caller; failures
//     are logged at slog.Warn and swallowed so AI calls are never blocked.
//   - NopRecorder: zero-cost no-op used when Postgres pool is unavailable
//     (SQLite dev path) or during testing.
//   - cost_usd_micro: costs stored as integer micro-USD (USD * 1_000_000)
//     to avoid floating-point rounding.
package aicost

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ModelRate holds per-million-token input and output prices for one model.
type ModelRate struct {
	InPerMillion  float64 // USD per 1M input tokens
	OutPerMillion float64 // USD per 1M output tokens
}

// RateUSDPerMillion is the rate table used by costMicroUSD.
// Sources: https://www.anthropic.com/pricing (2026-06 snapshot).
// Unknown models produce 0 cost + slog.Warn (see costMicroUSD).
var RateUSDPerMillion = map[string]ModelRate{
	"claude-haiku-4-5":  {InPerMillion: 0.25, OutPerMillion: 1.25},
	"claude-sonnet-4-6": {InPerMillion: 3, OutPerMillion: 15},
	"claude-opus-4-8":   {InPerMillion: 15, OutPerMillion: 75},
}

// costMicroUSD computes the cost in micro-USD (USD * 1_000_000) for the given
// model and token counts. Unknown models return 0 and emit a slog.Warn so
// operators can add missing entries to RateUSDPerMillion.
func costMicroUSD(model string, inputTokens, outputTokens int64) int64 {
	rate, ok := RateUSDPerMillion[model]
	if !ok {
		slog.Warn("aicost: unknown model, cost not computed",
			"model", model,
			"input_tokens", inputTokens,
			"output_tokens", outputTokens,
		)
		return 0
	}
	// Multiply before dividing to minimise rounding loss.
	// 1_000_000 micro-USD per USD, 1_000_000 tokens per million → factors cancel.
	inCostMicro := float64(inputTokens) * rate.InPerMillion
	outCostMicro := float64(outputTokens) * rate.OutPerMillion
	return int64(inCostMicro + outCostMicro)
}

// RecordParams carries the parameters for a single API call recording.
type RecordParams struct {
	Caller           string // e.g. "summarizer.Summarize"
	Model            string // e.g. "claude-haiku-4-5"
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
}

// Recorder is the interface for recording AI cost ledger entries.
type Recorder interface {
	// Record persists one API call's usage. It MUST NOT block the caller or
	// return errors — failures are logged internally and swallowed.
	Record(ctx context.Context, workspaceID *uuid.UUID, p RecordParams)
}

// NopRecorder is a no-op implementation used when PG pool is unavailable.
type NopRecorder struct{}

// Record is a no-op.
func (NopRecorder) Record(_ context.Context, _ *uuid.UUID, _ RecordParams) {}

// pgRecorder writes ai_cost_ledger rows via pgxpool. Fire-and-forget: errors
// are logged at slog.Warn and swallowed so AI calls are never blocked.
type pgRecorder struct {
	pool *pgxpool.Pool
}

// NewPgRecorder creates a pgRecorder. pool MUST NOT be nil.
func NewPgRecorder(pool *pgxpool.Pool) Recorder {
	return &pgRecorder{pool: pool}
}

const insertLedgerSQL = `
INSERT INTO ai_cost_ledger
  (workspace_id, caller, model,
   input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
   cost_usd_micro)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// Record writes one cost-ledger row. If the write fails it logs a warning and
// returns — the caller's AI response is unaffected.
func (r *pgRecorder) Record(ctx context.Context, workspaceID *uuid.UUID, p RecordParams) {
	cost := costMicroUSD(p.Model, p.InputTokens, p.OutputTokens)

	// Independent timeout: the write MUST NOT inherit a request context that
	// may already be cancelled (goroutine context rule / fire-and-forget design
	// per backend-security-design.md goroutine rule).
	_ = ctx // original ctx deliberately unused — fire-and-forget with fresh context
	writeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	//nolint:contextcheck // fire-and-forget: independent timeout, never inherit caller ctx; protects AI response from DB write failure
	_, err := r.pool.Exec(writeCtx, insertLedgerSQL,
		workspaceID,
		p.Caller,
		p.Model,
		p.InputTokens,
		p.OutputTokens,
		p.CacheReadTokens,
		p.CacheWriteTokens,
		cost,
	)
	if err != nil {
		slog.Warn("aicost: failed to record ledger entry",
			"caller", p.Caller,
			"model", p.Model,
			"error", err,
		)
	}
}
