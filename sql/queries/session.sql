-- name: CreateSessionHandoff :one
INSERT INTO session_handoffs (project_id, repo_name, intent, context_summary)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetLatestUnresolvedHandoff :one
SELECT * FROM session_handoffs
WHERE resolved_at IS NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: ResolveHandoff :exec
UPDATE session_handoffs SET resolved_at = NOW() WHERE id = $1;
