-- SQLite parity for migrations/000061_ai_cost_ledger.up.sql.
-- ai_cost_ledger is PG-only (the pgRecorder only writes when pgxpool != nil);
-- SQLite dev path skips recording gracefully via NopRecorder.
-- This file exists for dual-backend parity compliance per
-- backend-security-design.md §6.3.
SELECT 1; -- no-op: SQLite dev path uses NopRecorder; no table required.
