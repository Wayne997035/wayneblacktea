-- [F170-21] Restore the exact pre-normalisation due_date text.
--
-- This is a true inverse, not a best-effort one: the up migration recorded
-- every value it was about to change in f170_21_due_date_backup, so rows that
-- carried sub-millisecond precision come back with it. Recomputing the old
-- shape instead would be impossible — normalisation is many-to-one
-- ("...00Z" and "...00.000Z" both map to "...00.000Z"), so nothing in the
-- normalised value says which shape it came from.
--
-- Rows the up migration skipped (already correct, NULL, or unparseable) have
-- no backup row and are correctly left alone by the IN (...) predicate.

UPDATE goals
   SET due_date = (
        SELECT b.old_due_date
          FROM f170_21_due_date_backup b
         WHERE b.table_name = 'goals'
           AND b.row_id = goals.id
   )
 WHERE id IN (SELECT row_id FROM f170_21_due_date_backup WHERE table_name = 'goals');

UPDATE tasks
   SET due_date = (
        SELECT b.old_due_date
          FROM f170_21_due_date_backup b
         WHERE b.table_name = 'tasks'
           AND b.row_id = tasks.id
   )
 WHERE id IN (SELECT row_id FROM f170_21_due_date_backup WHERE table_name = 'tasks');

DROP TABLE IF EXISTS f170_21_due_date_backup;
