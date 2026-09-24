-- 000080_deletion_tombstones.up.sql
-- [F191-03] Contract stage for the soft-delete / restore feature (PR #191,
-- decisions 3cc5350f / 17a1086b): a snapshot table delete_project and
-- delete_task will write into before removing a row, so a delete can be
-- reversed within a 30-day retention window.
--
-- This migration only creates the table. Nothing writes to it yet — the
-- snapshot INSERT (F191-04/05), the activity_log audit write (F191-06) and
-- the restore_project path (F191-07) land in later commits in this same PR
-- (#191); see internal/gtd/deleteproject_orchestration.go's
-- DeleteProjectAdapter interface for the (declared here, stubbed, not yet
-- wired into the delete transaction) methods this table exists to serve.
--
-- No FK constraints (red line #9): referential integrity lives in Go, at
-- the write boundary, exactly as project_id already does elsewhere.
-- deletion_tombstones.project_id is itself exempt from delete_project's own
-- project_id cleanup sweep — see gtd.ProjectIDCleanupExemptions for why
-- nulling it here would make restore_project unable to find what it just
-- wrote.
--
-- Column notes:
--   * id has NO DEFAULT. The Go orchestration layer generates it, matching
--     the convention every write path in this schema already follows for
--     ids that matter to transactional consistency.
--   * deletion_id groups every row one delete_project or delete_task call
--     writes (the project's own snapshot plus one row per task it took with
--     it). restore_project finds a group by its most recent project-kind
--     row, then writes back and consumes the WHOLE group by this id — never
--     by project_id, which would also reach an unrelated, still-in-window
--     group produced by an earlier standalone delete_task under the same
--     project (design 4; that earlier group belongs to a separate, not-yet-
--     designed single-task restore).
--   * entity_kind distinguishes a project-row snapshot from a task-row
--     snapshot within the same group.
--   * project_id is nullable: a delete_task snapshot's project_id is the
--     task's original project_id, itself nullable for tasks with no project
--     (migrations/000001_gtd.up.sql).
--   * payload is the entire source row, captured as JSONB built by
--     to_jsonb() in SQL (design: not assembled from a Go struct — the Go
--     Task type deliberately omits some columns, e.g. area added in 000079,
--     that a struct-based snapshot would silently drop on restore).
--   * deleted_at has NO DEFAULT and is not NOW(). The Go orchestration layer
--     reads the clock once per deletion group and writes that same value
--     into every row the group produces; a per-statement server-side default
--     cannot guarantee that, and the pruner's cutoff landing between two
--     slightly different per-row timestamps would delete a group's project
--     snapshot while leaving its task snapshots behind, or vice versa.
--
-- Index notes:
--   * (project_id, deleted_at DESC): restore_project's "most recent
--     project-kind tombstone for this project_id" lookup.
--   * (deleted_at): the pruner's range scan.
--   * (deletion_id): restore_project's "every row in this group" write-back
--     and consume, and the pruner's per-group accounting.

CREATE TABLE deletion_tombstones (
    id           UUID        PRIMARY KEY,
    workspace_id UUID,
    deletion_id  UUID        NOT NULL,
    entity_kind  TEXT        NOT NULL CHECK (entity_kind IN ('project', 'task')),
    entity_id    UUID        NOT NULL,
    project_id   UUID,
    payload      JSONB       NOT NULL,
    deleted_by   TEXT        NOT NULL,
    deleted_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_deletion_tombstones_project_deleted_at
    ON deletion_tombstones(project_id, deleted_at DESC);

CREATE INDEX idx_deletion_tombstones_deleted_at
    ON deletion_tombstones(deleted_at);

CREATE INDEX idx_deletion_tombstones_deletion_id
    ON deletion_tombstones(deletion_id);
