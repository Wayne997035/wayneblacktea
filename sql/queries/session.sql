-- name: CreateSessionHandoff :one
INSERT INTO session_handoffs (project_id, repo_name, intent, context_summary, workspace_id)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetLatestUnresolvedHandoff :one
SELECT * FROM session_handoffs
WHERE resolved_at IS NULL
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC
LIMIT 1;

-- name: ResolveHandoff :execrows
UPDATE session_handoffs SET resolved_at = NOW()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'));
