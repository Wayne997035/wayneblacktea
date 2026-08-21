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
-- path/description/language/current_branch/next_planned_step are
-- presence-aware (Ω6, 2026-08-20-mcp-surface-spec.md): the CASE checks the
-- bound PARAMETER ($2/$3/$4/$5/$7), not EXCLUDED.<col> (which post-INSERT is
-- never NULL — it's whatever the VALUES clause carried). NULL means the
-- caller omitted the field (preserve stored value); a non-NULL value
-- (including "") means an explicit set. Without this, every sync_repo call
-- that didn't re-specify a field silently wiped it. known_issues already had
-- this protection.
INSERT INTO repos (name, path, description, language, current_branch, known_issues, next_planned_step, last_activity, workspace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (workspace_id, name) DO UPDATE SET
    path = CASE WHEN $2 IS NULL THEN repos.path ELSE EXCLUDED.path END,
    description = CASE WHEN $3 IS NULL THEN repos.description ELSE EXCLUDED.description END,
    language = CASE WHEN $4 IS NULL THEN repos.language ELSE EXCLUDED.language END,
    current_branch = CASE WHEN $5 IS NULL THEN repos.current_branch ELSE EXCLUDED.current_branch END,
    known_issues = COALESCE(EXCLUDED.known_issues, repos.known_issues),
    next_planned_step = CASE WHEN $7 IS NULL THEN repos.next_planned_step ELSE EXCLUDED.next_planned_step END,
    last_activity = EXCLUDED.last_activity,
    updated_at = NOW()
RETURNING *;
