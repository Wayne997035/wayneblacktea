-- Phase A: workspace isolation preparation.
-- Adds workspace_id to all domain tables. NULLABLE in this phase so existing
-- rows (no workspace assignment yet) remain valid. Phase B will introduce
-- WORKSPACE_ID env filtering at the MCP/API layer.
--
-- All ALTER TABLE ADD COLUMN are nullable with no default, so PostgreSQL
-- performs only a metadata change (no full table rewrite) since 11+.

ALTER TABLE goals             ADD COLUMN workspace_id UUID;
ALTER TABLE projects          ADD COLUMN workspace_id UUID;
ALTER TABLE tasks             ADD COLUMN workspace_id UUID;
ALTER TABLE activity_log      ADD COLUMN workspace_id UUID;
ALTER TABLE repos             ADD COLUMN workspace_id UUID;
ALTER TABLE decisions         ADD COLUMN workspace_id UUID;
ALTER TABLE session_handoffs  ADD COLUMN workspace_id UUID;
ALTER TABLE knowledge_items   ADD COLUMN workspace_id UUID;
ALTER TABLE concepts          ADD COLUMN workspace_id UUID;
ALTER TABLE review_schedule   ADD COLUMN workspace_id UUID;

-- Partial indexes: only index rows that have a workspace assigned.
-- Saves space and keeps lookups fast once Phase B starts populating workspace_id.
CREATE INDEX idx_goals_workspace_id            ON goals(workspace_id)            WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_projects_workspace_id         ON projects(workspace_id)         WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_tasks_workspace_id            ON tasks(workspace_id)            WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_activity_log_workspace_id     ON activity_log(workspace_id)     WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_repos_workspace_id            ON repos(workspace_id)            WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_decisions_workspace_id        ON decisions(workspace_id)        WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_session_handoffs_workspace_id ON session_handoffs(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_knowledge_items_workspace_id  ON knowledge_items(workspace_id)  WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_concepts_workspace_id         ON concepts(workspace_id)         WHERE workspace_id IS NOT NULL;
CREATE INDEX idx_review_schedule_workspace_id  ON review_schedule(workspace_id)  WHERE workspace_id IS NOT NULL;
