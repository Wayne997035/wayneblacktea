-- 000088_task_area_changye.down.sql (SQLite twin)
-- [F1011-01] Removes the 'changye' row only. Tasks already carrying
-- area = 'changye' are left untouched: with the row gone they vanish from
-- the gtd/areas counts until 000088 is applied again. See
-- migrations/000088_task_area_changye.down.sql.

DELETE FROM task_areas WHERE area = 'changye';
