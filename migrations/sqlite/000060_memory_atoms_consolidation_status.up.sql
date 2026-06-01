-- SQLite parity for M9: no COMMENT ON COLUMN syntax in SQLite.
-- digest_status already supports 'consolidated' via unconstrained TEXT column.
-- This file is a documented no-op to keep the migration sequence in parity.
SELECT 1; -- 'consolidated' is a valid digest_status value: pending | done | failed | consolidated
