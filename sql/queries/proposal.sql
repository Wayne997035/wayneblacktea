-- name: CreatePendingProposal :one
INSERT INTO pending_proposals (workspace_id, type, payload, proposed_by)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetPendingProposal :one
SELECT * FROM pending_proposals WHERE id = $1 LIMIT 1;

-- name: ListPendingProposals :many
SELECT * FROM pending_proposals
WHERE status = 'pending'
ORDER BY created_at DESC;

-- name: ResolvePendingProposal :one
UPDATE pending_proposals
SET status = $2, resolved_at = NOW()
WHERE id = $1 AND status = 'pending'
RETURNING *;
