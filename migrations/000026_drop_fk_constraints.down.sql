-- Migration 000026 rollback: re-add foreign-key constraints.
--
-- Mirror of the original FKs introduced in migrations 000001, 000003, 000004,
-- 000006, 000021, 000022. Cascade semantics are preserved exactly. NOT VALID
-- is intentionally NOT used: a rollback should re-establish full integrity,
-- not skip validation of existing rows.
--
-- Plain SQL only; no psql metacommands (golang-migrate cannot parse them).

ALTER TABLE projects
    ADD CONSTRAINT projects_goal_id_fkey
    FOREIGN KEY (goal_id) REFERENCES goals(id);

ALTER TABLE tasks
    ADD CONSTRAINT tasks_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id);

ALTER TABLE activity_log
    ADD CONSTRAINT activity_log_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id);

ALTER TABLE decisions
    ADD CONSTRAINT decisions_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id);

ALTER TABLE session_handoffs
    ADD CONSTRAINT session_handoffs_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id);

ALTER TABLE review_schedule
    ADD CONSTRAINT review_schedule_concept_id_fkey
    FOREIGN KEY (concept_id) REFERENCES concepts(id) ON DELETE CASCADE;

ALTER TABLE work_sessions
    ADD CONSTRAINT work_sessions_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL;

ALTER TABLE work_sessions
    ADD CONSTRAINT work_sessions_current_task_id_fkey
    FOREIGN KEY (current_task_id) REFERENCES tasks(id) ON DELETE SET NULL;

ALTER TABLE work_session_tasks
    ADD CONSTRAINT work_session_tasks_session_id_fkey
    FOREIGN KEY (session_id) REFERENCES work_sessions(id) ON DELETE CASCADE;

ALTER TABLE work_session_tasks
    ADD CONSTRAINT work_session_tasks_task_id_fkey
    FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE;
