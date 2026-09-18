-- 000079_task_area.up.sql
-- [F186-01] Adds a single-valued `area` classification to tasks, backed by a
-- task_areas lookup table, so "how many tickets are left in <project>" has
-- exactly one answer.
--
-- The problem this fixes: classification only ever existed as a bracket
-- prefix in the title, indexed by nothing. The same concept had three
-- non-overlapping encodings at the time of writing ([wbt] 155 rows,
-- [wbt-2.0] 7 rows, project_id=2be35a8f 92 rows); WBT3.0 was spread over
-- ~25 distinct prefixes; the string "AI 架構" did not exist in the data at
-- all; and 20% of open rows had no bracket whatsoever. Every answer to
-- "how many are left" was therefore a boundary drawn by eye at query time,
-- which is why no two answers agreed.
--
-- Design notes:
--   * Lookup table, not a CHECK constraint and not a native enum. The
--     vocabulary actively evolves (a "戰役" starts and ends), and changing a
--     CHECK means a migration plus a production deploy — and a deploy here
--     is a measured ~73s MCP outage. With a lookup table, adding an area is
--     one INSERT. Native enum is worse still: PostgreSQL has no DROP VALUE,
--     so retiring one rewrites the column under ACCESS EXCLUSIVE.
--   * No foreign key from tasks.area to task_areas.area (red line #9 /
--     backend-security-design.md §1.1). Referential integrity lives in Go,
--     at the write boundary, exactly as project_id already does (000001).
--   * Single-valued TEXT, not TEXT[] + GIN. "How many are left" only has a
--     unique answer when a row belongs to exactly one bucket — with
--     multi-value the per-area counts sum to more than the total, which is
--     the very symptom being fixed. (A GIN/array design is also ~7x faster
--     on containment queries, but that result is at billion-row scale; this
--     table holds ~1.8k rows, where every approach is sub-millisecond.)
--   * NOT NULL DEFAULT 'unsorted' rather than a nullable column. A NULL
--     would be silent; 'unsorted' is a visible bucket that the counts
--     resource always prints. This repo already demonstrated what silence
--     costs: due_date is documented "Required." on the add_task tool but
--     carries no mcp.Required(), and 14% of open rows have it empty with
--     nobody noticing.
--   * Index on (area, status), area first: every query is "the open rows of
--     one area", so the equality column has to lead for the index to be
--     usable. A (status, area) index would not serve it.
--   * Seeding the initial rows here (rather than leaving the table empty)
--     is deliberate: an empty lookup table would make the very first
--     add_task call fail validation with no way to recover through the MCP
--     surface.

CREATE TABLE task_areas (
    area       TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 100,
    archived   BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
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

-- Backfill. Rules are applied in order and each one only claims rows still
-- sitting in 'unsorted', so the first match wins — wbt3 MUST precede wbt
-- because '[WBT3.0 ...' also matches the '[wbt%' pattern.
--
-- ILIKE (not LIKE) so the PostgreSQL behaviour matches SQLite, whose LIKE is
-- case-insensitive for ASCII by default. '[' is a literal in both dialects'
-- LIKE — neither treats it as a character class.
--
-- This backfill is best-effort by construction: no rule can decide which
-- bucket an unbracketed title belongs to. Those rows stay 'unsorted' on
-- purpose. An LLM classifier was considered and rejected — the whole point
-- of this migration is a single stable answer, and a non-deterministic
-- classifier would just move the "different number every time" problem one
-- layer down.

UPDATE tasks SET area = 'wbt3'
 WHERE area = 'unsorted' AND title ILIKE '[WBT3.0%';

UPDATE tasks SET area = 'ai-arch'
 WHERE area = 'unsorted'
   AND (title ILIKE '[制度%'
     OR title ILIKE '[harness%'
     OR title ILIKE '[bsl%'
     OR title ILIKE '[機械引擎%'
     OR title ILIKE '[索引%'
     OR title ILIKE '[sprint %'
     OR title ILIKE '[7th-army%'
     OR title ILIKE '[PR175%'
     OR title ILIKE '[PR160%'
     OR title ILIKE '[token 稽核%'
     OR title ILIKE '[eval%'
     OR title ILIKE 'review-army-ledger%'
     OR title ILIKE 'pitfalls%');

UPDATE tasks SET area = 'coverones'
 WHERE area = 'unsorted'
   AND (title ILIKE '%CoverOnes%'
     OR title ILIKE '[marketplace%'
     OR title ILIKE '[admin-web%');

UPDATE tasks SET area = 'intel-flow'
 WHERE area = 'unsorted' AND title ILIKE '[intel-flow%';

UPDATE tasks SET area = 'wbt'
 WHERE area = 'unsorted'
   AND (title ILIKE '[wbt%' OR title ILIKE '[wayneblacktea%');
