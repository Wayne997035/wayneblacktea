DROP INDEX IF EXISTS idx_tasks_pr_url;
DROP INDEX IF EXISTS idx_tasks_branch_name;
ALTER TABLE tasks DROP COLUMN IF EXISTS commit_shas;
ALTER TABLE tasks DROP COLUMN IF EXISTS pr_url;
ALTER TABLE tasks DROP COLUMN IF EXISTS branch_name;
