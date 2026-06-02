-- Migration: 000061_ai_cost_ledger
-- Purpose: AI cost + token observability ledger.
-- Captures input/output/cache token counts and computed USD cost for every
-- Anthropic API call. Workspace-scoped. No FK constraints (CLAUDE.md #9).
--
-- Retention: 30-day TTL enforced by the daily-ai-cost-ledger-prune scheduler
-- job (DELETE WHERE created_at < NOW() - INTERVAL '30 days') per
-- backend-security-design.md §1.3 (observability tables MUST have retention).

CREATE TABLE ai_cost_ledger (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID,
    caller              TEXT        NOT NULL,
    model               TEXT        NOT NULL,
    input_tokens        BIGINT      NOT NULL DEFAULT 0,
    output_tokens       BIGINT      NOT NULL DEFAULT 0,
    cache_read_tokens   BIGINT      NOT NULL DEFAULT 0,
    cache_write_tokens  BIGINT      NOT NULL DEFAULT 0,
    cost_usd_micro      BIGINT      NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Composite index for workspace-scoped time-series queries and TTL prune.
CREATE INDEX idx_ai_cost_ledger_workspace_time
    ON ai_cost_ledger (workspace_id, created_at DESC);

COMMENT ON TABLE ai_cost_ledger
    IS 'AI token usage and computed USD cost per API call. 30-day retention via daily-ai-cost-ledger-prune scheduler job per backend-security-design.md §1.3.';
COMMENT ON COLUMN ai_cost_ledger.cost_usd_micro
    IS 'Computed cost in micro-USD (USD * 1_000_000). Stored as integer to avoid float rounding.';
