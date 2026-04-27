-- name: CreateConcept :one
INSERT INTO concepts (title, content, tags, workspace_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetConceptByID :one
SELECT * FROM concepts
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'));

-- name: ListDueReviews :many
SELECT c.id, c.title, c.content, c.tags, c.created_at, c.updated_at,
       rs.id as schedule_id, rs.stability, rs.difficulty, rs.due_date, rs.review_count
FROM concepts c
JOIN review_schedule rs ON rs.concept_id = c.id
WHERE rs.due_date <= NOW()
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR c.workspace_id = sqlc.narg('workspace_id'))
ORDER BY rs.due_date ASC
LIMIT sqlc.arg('limit_n');

-- name: CreateReviewSchedule :one
INSERT INTO review_schedule (concept_id, workspace_id)
VALUES ($1, $2)
RETURNING *;

-- name: UpdateReviewSchedule :one
UPDATE review_schedule
SET stability = sqlc.arg('stability'), difficulty = sqlc.arg('difficulty'), due_date = sqlc.arg('due_date'),
    last_review_at = NOW(), review_count = review_count + 1, updated_at = NOW()
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
RETURNING *;
