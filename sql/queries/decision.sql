-- name: CreateDecision :one
-- actor_session_id / confirmed_by_human (migration 000076): caller-code-path
-- values only, never decoded from an MCP/HTTP payload — see
-- internal/decision.LogParams's ActorSessionID/ConfirmedByHuman doc comments.
INSERT INTO decisions (project_id, repo_name, title, context, decision, rationale, alternatives, workspace_id, task_id, source, actor_session_id, confirmed_by_human)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: ListDecisionsByRepo :many
-- [F1003-10] , id DESC tiebreaker + OFFSET added: without a tiebreaker, rows
-- sharing an identical created_at sort non-deterministically between calls,
-- so offset-based paging over them can skip or duplicate across page
-- boundaries. Matches ListDecisionsFiltered's existing convention below and
-- SQLite's ByRepo, which already orders this way.
SELECT * FROM decisions
WHERE repo_name = sqlc.arg('repo_name')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_n')
OFFSET sqlc.arg('offset_n');

-- name: ListDecisionsByProject :many
-- [F1003-10] Same , id DESC tiebreaker + OFFSET rationale as
-- ListDecisionsByRepo above.
SELECT * FROM decisions
WHERE project_id = sqlc.arg('project_id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_n')
OFFSET sqlc.arg('offset_n');

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
-- OFFSET added [F0930-13]: list_decisions previously had no pagination path
-- at all (has_more with no way to fetch the next page); offset_n defaults to
-- 0 at the Go layer (decision.ListParams zero value) so existing callers are
-- unaffected.
SELECT * FROM decisions
WHERE (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
  AND (sqlc.narg('project_id')::uuid IS NULL OR project_id = sqlc.narg('project_id'))
  AND (sqlc.narg('repo_name')::text IS NULL OR repo_name = sqlc.narg('repo_name'))
  AND (sqlc.arg('include_auto')::bool OR source = 'manual')
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('limit_n')
OFFSET sqlc.arg('offset_n');
