-- 000088_task_area_changye.down.sql
-- [F1011-01] Removes the 'changye' row only. Tasks already carrying
-- area = 'changye' are left untouched (no rewrite to misc): with the row
-- gone they vanish from the gtd/areas counts and list_tasks(area="changye")
-- answers "unknown area" until 000088 is applied again, which restores the
-- counts with no data change.

DELETE FROM task_areas WHERE area = 'changye';
