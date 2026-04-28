-- name: CreateDecision :one
INSERT INTO decisions (project_id, repo_name, title, context, decision, rationale, alternatives, workspace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListDecisionsByRepo :many
SELECT * FROM decisions
WHERE repo_name = sqlc.arg('repo_name')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit_n');

-- name: ListDecisionsByProject :many
SELECT * FROM decisions
WHERE project_id = sqlc.arg('project_id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit_n');

-- name: ListAllDecisions :many
SELECT * FROM decisions
WHERE (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit_n');
