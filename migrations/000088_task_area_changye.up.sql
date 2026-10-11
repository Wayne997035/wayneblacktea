-- 000088_task_area_changye.up.sql
-- [F1011-01] Adds the 'changye' area (Changye Nightmart) to the task_areas
-- lookup so Changye tasks stop being parked in 'misc'. A migration rather
-- than a hand INSERT on prod = decision 3bf29ed0: the row is versioned,
-- replayable, and identical on both backends. Data only — no schema object
-- is added, and no FK constraint (red line #9); tasks.area is still checked
-- at the Go write boundary (TaskAreaExists).
--
-- Plain INSERT, no conflict clause: same stance as 000079 — a re-run, or a
-- changye row someone already added by hand, must fail loudly instead of
-- being skipped silently.

INSERT INTO task_areas (area, label, sort_order) VALUES ('changye', 'Changye Nightmart', 60);
