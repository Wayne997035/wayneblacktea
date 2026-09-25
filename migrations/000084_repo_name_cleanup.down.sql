-- 000084_repo_name_cleanup.down.sql — [F0925-33]
--
-- No-op: the up migration is a data cleanup, not a schema change, and the
-- cleared repo_name values are not recoverable (decision 3be90339 — no
-- backup was taken). A down migration that tried to "undo" 000084 would
-- have to invent values that were never actually captured, which is worse
-- than leaving the column cleared. This file exists only so golang-migrate
-- has a down target if 000084 is ever superseded by a later migration.
SELECT 1;
