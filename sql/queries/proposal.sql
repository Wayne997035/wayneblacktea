-- name: CreatePendingProposal :one
INSERT INTO pending_proposals (workspace_id, type, payload, proposed_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetPendingProposal :one
SELECT * FROM pending_proposals
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
LIMIT 1;

-- [F170-06] row_limit/row_offset — same reasoning as gtd.sql's
-- ListActiveProjects. The `id DESC` tiebreaker also brings this query into
-- line with the SQLite twin (internal/storage/sqlite/proposal.go's
-- ListPending), which already ordered by created_at DESC, id DESC; the two
-- backends disagreed on tie order before this.
-- name: ListPendingProposals :many
SELECT * FROM pending_proposals
WHERE status = 'pending'
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('row_limit')::int OFFSET sqlc.arg('row_offset')::int;

-- name: ResolvePendingProposal :one
UPDATE pending_proposals
SET status = sqlc.arg('status'), resolved_at = NOW()
WHERE id = sqlc.arg('id')
  AND status = 'pending'
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
RETURNING *;
