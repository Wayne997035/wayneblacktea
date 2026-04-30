-- All queries take workspace_id as the named nullable arg @workspace_id.
-- NULL → no filter (legacy mode); UUID → strict per-workspace scope.

-- name: ListActiveProjects :many
SELECT * FROM projects
WHERE status = 'active'
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY priority ASC, updated_at DESC;

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

-- name: ListActiveGoals :many
SELECT * FROM goals
WHERE status = 'active'
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY due_date ASC NULLS LAST;

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

-- name: GetAllPendingTasks :many
SELECT * FROM tasks
WHERE status IN ('pending', 'in_progress')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY priority ASC, created_at ASC;

-- name: CreateTask :one
INSERT INTO tasks (project_id, title, description, priority, assignee, due_date, importance, context, workspace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: CompleteTask :one
UPDATE tasks SET status = 'completed', artifact = sqlc.arg('artifact'), updated_at = NOW()
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

-- name: CountTotalActiveTasks :one
SELECT COUNT(*) FROM tasks
WHERE status IN ('pending', 'in_progress')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'));

-- name: DeleteTask :exec
DELETE FROM tasks
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'));
