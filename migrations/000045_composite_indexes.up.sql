-- tasks: workspace-scoped status+priority filter (GetAllPendingTasks, system_health stuck check)
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_status_priority
    ON tasks(workspace_id, status, priority DESC);

-- tasks: workspace+project ordered by recency (project task lists)
CREATE INDEX IF NOT EXISTS idx_tasks_workspace_project_updated
    ON tasks(workspace_id, project_id, updated_at DESC NULLS LAST);

-- decisions: workspace+repo+time (list_decisions by repo)
CREATE INDEX IF NOT EXISTS idx_decisions_workspace_repo_created
    ON decisions(workspace_id, repo_name, created_at DESC);

-- session_handoffs: workspace+repo+time (handoff lookup by repo)
CREATE INDEX IF NOT EXISTS idx_session_handoffs_workspace_repo_created
    ON session_handoffs(workspace_id, repo_name, created_at DESC);

-- activity_log: workspace+project+time (ListActivityLogsSince project filter)
CREATE INDEX IF NOT EXISTS idx_activity_log_workspace_project_created
    ON activity_log(workspace_id, project_id, created_at DESC);
