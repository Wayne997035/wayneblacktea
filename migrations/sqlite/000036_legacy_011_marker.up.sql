-- SQLite parity no-op for 000036. SQLite never had the psql metacommand
-- problem (its 000011 was already a manual-only paired file) and the
-- backfill is unnecessary on a single-tenant local DB. Marker exists for
-- consistency with the Postgres migration numbering.
SELECT 1;
