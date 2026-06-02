// Package aicost records Anthropic API token usage and computed USD cost.
//
// Design principles:
//   - Fire-and-forget: Record returns immediately; the actual DB write is
//     dispatched to a background goroutine bounded by a semaphore (cap 50)
//     so AI calls are never blocked.  Failures are logged at slog.Warn and
//     swallowed.
//   - writeSync is the synchronous, unit-testable write path used by
//     integration tests that need deterministic row visibility.  Record wraps
//     it in a goroutine with its own context.Background()+timeout so the write
//     survives request-context cancellation.
//   - NopRecorder: zero-cost no-op used when Postgres pool is unavailable
//     (SQLite dev path) or during testing.
//   - cost_usd_micro: costs stored as integer micro-USD (USD * 1_000_000)
//     to avoid floating-point rounding.
package aicost

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// recordSemCap is the maximum number of concurrent background goroutines that
// pgRecorder.Record may spawn. Mirrors the autologSem cap in
// internal/mcp/middleware_autolog.go (cap 50).
const recordSemCap = 50

// recordWriteTimeout is the per-write deadline for the background goroutine.
// 5 s is sufficient for a single-row INSERT on a healthy Postgres connection
// while keeping the goroutine lifetime short.
const recordWriteTimeout = 5 * time.Second

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
	// Record persists one API call's usage asynchronously. It MUST NOT block
	// the caller or return errors — failures are logged internally and swallowed.
	Record(ctx context.Context, workspaceID *uuid.UUID, p RecordParams)
}

// NopRecorder is a no-op implementation used when PG pool is unavailable.
type NopRecorder struct{}

// Record is a no-op.
func (NopRecorder) Record(_ context.Context, _ *uuid.UUID, _ RecordParams) {}

// pgRecorder writes ai_cost_ledger rows via pgxpool. The public Record method
// is fire-and-forget: it dispatches the write to a bounded background goroutine
// and returns immediately so AI callers are never blocked.
type pgRecorder struct {
	pool *pgxpool.Pool
	// sem is a buffered channel used as a counting semaphore that caps the
	// number of concurrent background goroutines at recordSemCap.
	// nil → no cap (only possible if constructed directly in tests without
	// NewPgRecorder; existing tests are unaffected).
	sem chan struct{}
}

// NewPgRecorder creates a pgRecorder backed by pool. pool MUST NOT be nil.
func NewPgRecorder(pool *pgxpool.Pool) Recorder {
	return &pgRecorder{
		pool: pool,
		sem:  make(chan struct{}, recordSemCap),
	}
}

const insertLedgerSQL = `
INSERT INTO ai_cost_ledger
  (workspace_id, caller, model,
   input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
   cost_usd_micro)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

// writeSync performs the synchronous DB INSERT. It is unexported so callers
// must go through Record (which dispatches it to a goroutine), but it is
// directly callable by integration tests for deterministic row visibility.
//
// ctx must have its own independent timeout set by the caller; writeSync does
// not create a context internally.
func (r *pgRecorder) writeSync(ctx context.Context, workspaceID *uuid.UUID, p RecordParams) error {
	cost := costMicroUSD(p.Model, p.InputTokens, p.OutputTokens)

	_, err := r.pool.Exec(ctx, insertLedgerSQL,
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
		return fmt.Errorf("aicost.writeSync: %w", err)
	}
	return nil
}

// Record dispatches the DB write to a bounded background goroutine and returns
// immediately — the caller's AI response is never blocked.
//
// The goroutine:
//   - is guarded by a buffered semaphore (cap recordSemCap = 50); if the
//     semaphore is full the write is dropped with a slog.Warn.
//   - uses context.Background()+recordWriteTimeout so the write survives
//     request-context cancellation (backend-security-design.md goroutine rule).
//   - wraps the body in defer recover() so a panic inside writeSync can never
//     crash the server.
//
// contextcheck is suppressed: Record intentionally ignores the caller's ctx and
// spawns a goroutine with a fresh context.Background()-based timeout so the DB
// write survives request cancellation (fire-and-forget design per
// backend-security-design.md goroutine rule).
func (r *pgRecorder) Record(_ context.Context, workspaceID *uuid.UUID, p RecordParams) {
	// Caller ctx is intentionally ignored — we must not inherit a request
	// context that may already be cancelled when the goroutine runs.

	launch := func(fn func()) {
		if r.sem == nil {
			// Constructed directly without NewPgRecorder (only in tests that do
			// not call NewPgRecorder). Fall back to unbounded goroutine so
			// existing tests are not broken.
			go fn()
			return
		}
		select {
		case r.sem <- struct{}{}:
			go func() {
				defer func() { <-r.sem }()
				fn()
			}()
		default:
			slog.Warn("aicost: semaphore full, dropping cost-ledger write",
				"caller", p.Caller,
				"model", p.Model,
			)
		}
	}

	// The goroutine body does not use any context from Record's scope — it
	// creates its own context.Background()+timeout. contextcheck is suppressed
	// because the linter incorrectly flags the closure for not forwarding a
	// context that Record intentionally discards (fire-and-forget rule).
	//nolint:contextcheck // closure creates its own context.Background()-based timeout; caller ctx must NOT be forwarded
	launch(func() {
		// Recover from any panic so a write failure never crashes the process.
		defer func() {
			if rec := recover(); rec != nil {
				slog.Warn("aicost: panic in background write goroutine",
					"caller", p.Caller,
					"model", p.Model,
					"panic", fmt.Sprintf("%v", rec),
				)
			}
		}()

		bgCtx, cancel := context.WithTimeout(context.Background(), recordWriteTimeout)
		defer cancel()

		if err := r.writeSync(bgCtx, workspaceID, p); err != nil {
			slog.Warn("aicost: failed to record ledger entry",
				"caller", p.Caller,
				"model", p.Model,
				"error", err,
			)
		}
	})
}
