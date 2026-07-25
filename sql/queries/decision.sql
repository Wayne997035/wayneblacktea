-- name: CreateDecision :one
INSERT INTO decisions (project_id, repo_name, title, context, decision, rationale, alternatives, workspace_id, task_id, source)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
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

-- name: ListDecisionsByTaskID :many
SELECT * FROM decisions
WHERE task_id = sqlc.arg('task_id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit_n');

-- name: ListDecisionsFiltered :many
-- P3.0a Stage B: source-filtered read path for MCP list_decisions.
-- project_id and repo_name are mutually exclusive at the application layer
-- (decision.ListParams.Validate) — this query accepts both narg'd so a nil
-- one is a no-op filter, but callers never pass both non-nil.
-- Source is filtered BEFORE ORDER/LIMIT so the limit isn't consumed by rows
-- that get excluded.
SELECT * FROM decisions
WHERE (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('repo_name')::text IS NULL OR repo_name = sqlc.narg('repo_name'))
  AND (sqlc.arg('include_auto')::bool OR source = 'manual')
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_n');
