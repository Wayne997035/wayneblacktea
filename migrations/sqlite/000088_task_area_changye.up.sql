-- 000088_task_area_changye.up.sql (SQLite twin)
-- [F1011-01] SQLite twin of migrations/000088_task_area_changye.up.sql:
-- same slug, label, sort_order and statement; see that file for the why.
-- No dialect difference — archived and created_at take their column
-- defaults. Plain INSERT, no conflict clause, no FK constraint (red line
-- #9): a re-run must fail loudly, as in 000079.

INSERT INTO task_areas (area, label, sort_order) VALUES ('changye', 'Changye Nightmart', 60);
