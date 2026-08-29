-- [F170-21] Normalise goals.due_date and tasks.due_date to the fixed-width
-- layout the Go side writes (sqliteTimestampLayout, '%Y-%m-%dT%H:%M:%f' + 'Z').
--
-- Why this is needed: these two TEXT columns were written by two mutually
-- incomparable layouts. Create*/Update* used Go's time.RFC3339Nano, which
-- STRIPS trailing fractional zeros ("2026-09-01T09:00:00Z"); Import* used the
-- fixed 3-digit layout ("2026-09-01T09:00:00.000Z"). SQLite compares TEXT
-- byte-wise and '.' (0x2E) sorts before 'Z' (0x5A), so the same instant in the
-- two shapes does not compare equal and a later instant can sort BEFORE an
-- earlier one. Once ActiveGoalsPage grew a LIMIT, that ordering stopped
-- deciding display order and started deciding which rows are on page 1.
--
-- The Go write paths are fixed in the same commit; this migration repairs the
-- rows already on disk.
--
-- PRECISION: strftime('%f') truncates to milliseconds, so a value that
-- carried sub-millisecond digits ("...00.123456Z") normalises to "...00.123Z".
-- That is a real, if tiny, loss, which is why every pre-image is saved below
-- rather than the update being done in place — the .down.sql restores them
-- exactly.

-- Pre-image store, so this migration is reversible. Deliberately no FK on
-- row_id: referential integrity is the code's job in this repo, and a FK here
-- would also make the goals/tasks rows undeletable while the backup exists.
CREATE TABLE IF NOT EXISTS f170_21_due_date_backup (
    table_name   TEXT NOT NULL,
    row_id       TEXT NOT NULL,
    old_due_date TEXT NOT NULL,
    PRIMARY KEY (table_name, row_id)
);

-- The WHERE clause is deliberately narrow, and each conjunct earns its place:
--   * due_date IS NOT NULL      — nothing to normalise.
--   * strftime(...) IS NOT NULL — strftime returns NULL for any value it
--     cannot parse. Without this, an unparseable due_date would be
--     overwritten WITH NULL, destroying it. Such rows are left untouched.
--   * due_date <> strftime(...) — the normalisation is idempotent, so this is
--     exactly "the stored text is not already in the target layout". Rows
--     already correct are not rewritten and do not enter the backup table.
INSERT OR REPLACE INTO f170_21_due_date_backup (table_name, row_id, old_due_date)
SELECT 'goals', id, due_date
  FROM goals
 WHERE due_date IS NOT NULL
   AND strftime('%Y-%m-%dT%H:%M:%fZ', due_date) IS NOT NULL
   AND due_date <> strftime('%Y-%m-%dT%H:%M:%fZ', due_date);

UPDATE goals
   SET due_date = strftime('%Y-%m-%dT%H:%M:%fZ', due_date)
 WHERE due_date IS NOT NULL
   AND strftime('%Y-%m-%dT%H:%M:%fZ', due_date) IS NOT NULL
   AND due_date <> strftime('%Y-%m-%dT%H:%M:%fZ', due_date);

INSERT OR REPLACE INTO f170_21_due_date_backup (table_name, row_id, old_due_date)
SELECT 'tasks', id, due_date
  FROM tasks
 WHERE due_date IS NOT NULL
   AND strftime('%Y-%m-%dT%H:%M:%fZ', due_date) IS NOT NULL
   AND due_date <> strftime('%Y-%m-%dT%H:%M:%fZ', due_date);

UPDATE tasks
   SET due_date = strftime('%Y-%m-%dT%H:%M:%fZ', due_date)
 WHERE due_date IS NOT NULL
   AND strftime('%Y-%m-%dT%H:%M:%fZ', due_date) IS NOT NULL
   AND due_date <> strftime('%Y-%m-%dT%H:%M:%fZ', due_date);
