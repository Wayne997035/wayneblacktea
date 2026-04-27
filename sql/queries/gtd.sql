-- name: ListActiveProjects :many
SELECT * FROM projects
WHERE status = 'active'
ORDER BY priority ASC, updated_at DESC;

-- name: GetProjectByName :one
SELECT * FROM projects WHERE name = $1 LIMIT 1;

-- name: CreateProject :one
INSERT INTO projects (goal_id, name, title, description, area, priority)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateProjectStatus :one
UPDATE projects SET status = $2, updated_at = NOW() WHERE id = $1 RETURNING *;

-- name: ListActiveGoals :many
SELECT * FROM goals WHERE status = 'active' ORDER BY due_date ASC NULLS LAST;

-- name: CreateGoal :one
INSERT INTO goals (title, description, area, due_date)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetTasksByProject :many
SELECT * FROM tasks
WHERE project_id = $1 AND status IN ('pending', 'in_progress')
ORDER BY priority ASC, created_at ASC;

-- name: GetAllPendingTasks :many
SELECT * FROM tasks
WHERE status IN ('pending', 'in_progress')
ORDER BY priority ASC, created_at ASC;

-- name: CreateTask :one
INSERT INTO tasks (project_id, title, description, priority, assignee, due_date, importance, context)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: CompleteTask :one
UPDATE tasks SET status = 'completed', artifact = $2, updated_at = NOW()
WHERE id = $1 RETURNING *;

-- name: CreateActivityLog :one
INSERT INTO activity_log (actor, project_id, action, notes)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CountCompletedTasksThisWeek :one
SELECT COUNT(*) FROM tasks
WHERE status = 'completed'
  AND updated_at >= date_trunc('week', NOW())
  AND updated_at < date_trunc('week', NOW()) + interval '1 week';

-- name: UpdateTaskStatus :one
UPDATE tasks SET status = $2, updated_at = NOW() WHERE id = $1 RETURNING *;

-- name: CountTotalActiveTasks :one
SELECT COUNT(*) FROM tasks WHERE status IN ('pending', 'in_progress');

-- name: DeleteTask :exec
DELETE FROM tasks WHERE id = $1;
