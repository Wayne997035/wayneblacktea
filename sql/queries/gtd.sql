-- All queries take workspace_id as the named nullable arg @workspace_id.
-- NULL → no filter (legacy mode); UUID → strict per-workspace scope.

-- [F170-04] row_limit/row_offset: this query had no LIMIT at all, so its
-- result — and therefore the list_projects MCP response — grew with the
-- table, with nothing between a large projects table and the caller's
-- context window. Callers that genuinely want every row (the HTTP dashboard,
-- context_handler) pass db.UnboundedRowLimit; that keeps their contract
-- while making the cap a decision each caller states rather than one nobody
-- can make.
--
-- The id tiebreaker is required, not cosmetic: (priority, updated_at) is not
-- unique, and OFFSET paging over a non-total order silently drops and repeats
-- rows across pages.
-- name: ListActiveProjects :many
SELECT * FROM projects
WHERE status = 'active'
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY priority ASC, updated_at DESC, id ASC
LIMIT sqlc.arg('row_limit')::int OFFSET sqlc.arg('row_offset')::int;

-- name: GetProjectByName :one
SELECT * FROM projects
WHERE name = sqlc.arg('name')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
LIMIT 1;

-- name: GetProjectByID :one
SELECT * FROM projects
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
LIMIT 1;

-- name: CreateProject :one
INSERT INTO projects (goal_id, name, title, description, area, priority, workspace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateProjectStatus :one
UPDATE projects SET status = sqlc.arg('status'), updated_at = NOW()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
RETURNING *;

-- [F170-05] row_limit/row_offset — same reasoning as ListActiveProjects
-- above. The id tiebreaker matters more here than anywhere: due_date is
-- nullable and goals without one all sort equal, so unpaginated order was
-- already arbitrary among them and OFFSET paging would have been unstable.
-- name: ListActiveGoals :many
SELECT * FROM goals
WHERE status = 'active'
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY due_date ASC NULLS LAST, id ASC
LIMIT sqlc.arg('row_limit')::int OFFSET sqlc.arg('row_offset')::int;

-- name: CreateGoal :one
INSERT INTO goals (title, description, area, due_date, workspace_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetTasksByProject :many
SELECT * FROM tasks
WHERE project_id = sqlc.arg('project_id')
  AND status IN ('pending', 'in_progress')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY priority ASC, created_at ASC;

-- name: ListProjectTasksAllStatuses :many
-- All-statuses variant of GetTasksByProject. Used by the ProjectDetailPage to
-- render both the "open" and the "completed/cancelled" sections; the default
-- GetTasksByProject query stays active-only so existing GTD list pages don't
-- regress. Ordering: newest activity first via COALESCE(updated_at, created_at)
-- DESC so completed rows surface in roughly the order they were finished.
SELECT * FROM tasks
WHERE project_id = sqlc.arg('project_id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY COALESCE(updated_at, created_at) DESC;

-- name: GetAllPendingTasks :many
SELECT * FROM tasks
WHERE status IN ('pending', 'in_progress')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY priority ASC, created_at ASC;

-- name: CreateTask :one
INSERT INTO tasks (project_id, title, description, priority, assignee, due_date, importance, context, kind, workspace_id, vision_item_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: SetTaskVisionItemID :one
UPDATE tasks
SET vision_item_id = sqlc.arg('vision_item_id'),
    updated_at     = NOW()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
RETURNING *;

-- name: CompleteTask :one
-- artifact is presence-aware (Ω4, 2026-08-20-mcp-surface-spec.md): omitting
-- it (sqlc.narg → SQL NULL) preserves whatever is already stored, matching
-- upsert_project_arch.summary/file_map's established convention. Without
-- COALESCE, re-completing a reopened task without re-supplying artifact
-- silently wiped an already-recorded PR/commit link.
UPDATE tasks SET status = 'completed', artifact = COALESCE(sqlc.narg('artifact'), artifact), updated_at = NOW()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
RETURNING *;

-- name: CreateActivityLog :one
INSERT INTO activity_log (actor, project_id, action, notes, workspace_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: CountCompletedTasksThisWeek :one
SELECT COUNT(*) FROM tasks
WHERE status = 'completed'
  AND updated_at >= date_trunc('week', NOW())
  AND updated_at < date_trunc('week', NOW()) + interval '1 week'
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'));

-- name: UpdateTaskStatus :one
UPDATE tasks SET status = sqlc.arg('status'), updated_at = NOW()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
RETURNING *;

-- name: BeginTaskStatus :one
-- Atomically sets status to in_progress only when the current status is not
-- already in_progress, preventing duplicate activity_log rows on concurrent calls.
-- Returns pgx.ErrNoRows when the task is already in_progress or not found.
UPDATE tasks SET status = 'in_progress', updated_at = NOW()
WHERE id = sqlc.arg('id')::uuid
  AND workspace_id = sqlc.arg('workspace_id')::uuid
  AND status != 'in_progress'
RETURNING *;

-- name: CountWeeklyRelevantTasks :one
-- Returns count of tasks that are "relevant to this week":
-- (1) completed this week, OR
-- (2) pending/in_progress AND (due_date this week OR created this week)
SELECT COUNT(*) FROM tasks
WHERE (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
  AND (
    (status = 'completed'
       AND updated_at >= date_trunc('week', NOW())
       AND updated_at < date_trunc('week', NOW()) + interval '1 week')
    OR (status IN ('pending','in_progress')
       AND ((due_date >= date_trunc('week', NOW())
             AND due_date < date_trunc('week', NOW()) + interval '1 week')
         OR created_at >= date_trunc('week', NOW())))
  );

-- name: DeleteTask :exec
-- The Go-side store wraps this in a transaction together with cleanup of
-- work_session_tasks + work_sessions.current_task_id (the cascade behaviour
-- previously enforced by FKs; see migration 000026 and gtd.Store.DeleteTask).
DELETE FROM tasks
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'));

-- name: UpdateGoal :one
UPDATE goals
SET title       = sqlc.arg('title'),
    description = sqlc.arg('description'),
    area        = sqlc.arg('area'),
    status      = sqlc.arg('status'),
    due_date    = sqlc.arg('due_date'),
    updated_at  = NOW()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
RETURNING *;

-- name: UpdateProject :one
UPDATE projects
SET title       = sqlc.arg('title'),
    description = sqlc.arg('description'),
    area        = sqlc.arg('area'),
    priority    = sqlc.arg('priority'),
    status      = sqlc.arg('status'),
    goal_id     = sqlc.arg('goal_id'),
    updated_at  = NOW()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
RETURNING *;
