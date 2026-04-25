-- name: CreateDecision :one
INSERT INTO decisions (project_id, repo_name, title, context, decision, rationale, alternatives)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListDecisionsByRepo :many
SELECT * FROM decisions
WHERE repo_name = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: ListDecisionsByProject :many
SELECT * FROM decisions
WHERE project_id = $1
ORDER BY created_at DESC
LIMIT $2;
