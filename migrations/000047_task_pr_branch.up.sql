ALTER TABLE tasks ADD COLUMN IF NOT EXISTS branch_name TEXT;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS pr_url TEXT;
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS commit_shas TEXT[] DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_tasks_branch_name ON tasks(branch_name) WHERE branch_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_pr_url ON tasks(pr_url) WHERE pr_url IS NOT NULL;
