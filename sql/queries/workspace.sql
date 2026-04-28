-- name: ListActiveRepos :many
SELECT * FROM repos
WHERE status = 'active'
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY last_activity DESC NULLS LAST, name ASC;

-- name: GetRepoByName :one
SELECT * FROM repos
WHERE name = sqlc.arg('name')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
LIMIT 1;

-- name: UpsertRepo :one
INSERT INTO repos (name, path, description, language, current_branch, known_issues, next_planned_step, last_activity, workspace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (name) DO UPDATE SET
    path = EXCLUDED.path,
    description = EXCLUDED.description,
    language = EXCLUDED.language,
    current_branch = EXCLUDED.current_branch,
    known_issues = COALESCE(EXCLUDED.known_issues, repos.known_issues),
    next_planned_step = EXCLUDED.next_planned_step,
    last_activity = EXCLUDED.last_activity,
    updated_at = NOW()
RETURNING *;
