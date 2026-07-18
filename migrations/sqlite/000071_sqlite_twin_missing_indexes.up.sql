-- SQLite-only corrective migration consolidating pre-existing index gaps
-- discovered while building the golang-migrate runner (E1): several merged
-- SQLite twin migrations create their table/columns but never created every
-- index internal/storage/sqlite/schema.sql (the runtime authority until E1)
-- has for that table. Per backend-security-design.md §6.4, merged migration
-- files are immutable; these files cannot be edited, so this migration
-- supplies the missing DDL. Add-only, no PG twin needed (SQLite-only index
-- parity; PG's own migrations are unaffected and already correct).
--
-- Gaps, by originating (unedited) merged migration:
--   000032_procedural_memories.up.sql: missing idx_procedural_memories_workspace,
--     idx_procedural_memories_repo.
--   000047_task_pr_branch.up.sql: missing idx_tasks_branch_name, idx_tasks_pr_url.
--   000053_memory_atoms_digest_status.up.sql: missing idx_memory_atoms_digest_status.

CREATE INDEX IF NOT EXISTS idx_procedural_memories_workspace ON procedural_memories(workspace_id);
CREATE INDEX IF NOT EXISTS idx_procedural_memories_repo      ON procedural_memories(repo_name);

CREATE INDEX IF NOT EXISTS idx_tasks_branch_name ON tasks(branch_name) WHERE branch_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tasks_pr_url      ON tasks(pr_url)      WHERE pr_url      IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_memory_atoms_digest_status ON memory_atoms(digest_status, workspace_id);
