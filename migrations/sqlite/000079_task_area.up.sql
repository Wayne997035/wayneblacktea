-- 000079_task_area.up.sql (SQLite twin)
-- [F186-01]
--
-- See migrations/000079_task_area.up.sql for the full design rationale.
--
-- Dialect differences from the Postgres original:
--   * TIMESTAMPTZ/NOW() → TEXT with the strftime default already used by
--     every other table in migrations/sqlite/000012_sqlite_baseline.up.sql.
--   * BOOLEAN → INTEGER 0/1, mirroring the is_mutating convention
--     (000035) — the store boundary converts to/from Go bool.
--   * ILIKE → LIKE. SQLite's LIKE is already case-insensitive for ASCII,
--     so the Postgres side uses ILIKE specifically to keep the two
--     backends' backfill results identical.
--   * CREATE TABLE (not IF NOT EXISTS) and a bare ADD COLUMN: a re-run MUST
--     fail loudly rather than silently no-op.

CREATE TABLE task_areas (
    area       TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 100,
    archived   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

INSERT INTO task_areas (area, label, sort_order) VALUES
    ('wbt',        'wayneblacktea 2.0',  10),
    ('ai-arch',    'AI 架構 / 制度',      20),
    ('wbt3',       'WBT 3.0',            30),
    ('coverones',  'CoverOnes',          40),
    ('intel-flow', 'intelligence-flow',  50),
    ('misc',       '其他',                90),
    ('unsorted',   '未分類',              99);

ALTER TABLE tasks ADD COLUMN area TEXT NOT NULL DEFAULT 'unsorted';

CREATE INDEX idx_tasks_area_status ON tasks(area, status);

-- Backfill — same rules, same order, same first-match-wins semantics as the
-- Postgres twin. wbt3 MUST precede wbt.

UPDATE tasks SET area = 'wbt3'
 WHERE area = 'unsorted' AND title LIKE '[WBT3.0%';

UPDATE tasks SET area = 'ai-arch'
 WHERE area = 'unsorted'
   AND (title LIKE '[制度%'
     OR title LIKE '[harness%'
     OR title LIKE '[bsl%'
     OR title LIKE '[機械引擎%'
     OR title LIKE '[索引%'
     OR title LIKE '[sprint %'
     OR title LIKE '[7th-army%'
     OR title LIKE '[PR175%'
     OR title LIKE '[PR160%'
     OR title LIKE '[token 稽核%'
     OR title LIKE '[eval%'
     OR title LIKE 'review-army-ledger%'
     OR title LIKE 'pitfalls%');

UPDATE tasks SET area = 'coverones'
 WHERE area = 'unsorted'
   AND (title LIKE '%CoverOnes%'
     OR title LIKE '[marketplace%'
     OR title LIKE '[admin-web%');

UPDATE tasks SET area = 'intel-flow'
 WHERE area = 'unsorted' AND title LIKE '[intel-flow%';

UPDATE tasks SET area = 'wbt'
 WHERE area = 'unsorted'
   AND (title LIKE '[wbt%' OR title LIKE '[wayneblacktea%');
