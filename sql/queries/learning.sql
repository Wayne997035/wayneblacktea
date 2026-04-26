-- name: CreateConcept :one
INSERT INTO concepts (title, content, tags) VALUES ($1, $2, $3) RETURNING *;

-- name: GetConceptByID :one
SELECT * FROM concepts WHERE id = $1;

-- name: ListDueReviews :many
SELECT c.id, c.title, c.content, c.tags, c.created_at, c.updated_at,
       rs.id as schedule_id, rs.stability, rs.difficulty, rs.due_date, rs.review_count
FROM concepts c
JOIN review_schedule rs ON rs.concept_id = c.id
WHERE rs.due_date <= NOW()
ORDER BY rs.due_date ASC
LIMIT $1;

-- name: CreateReviewSchedule :one
INSERT INTO review_schedule (concept_id) VALUES ($1) RETURNING *;

-- name: UpdateReviewSchedule :one
UPDATE review_schedule
SET stability = $2, difficulty = $3, due_date = $4,
    last_review_at = NOW(), review_count = review_count + 1, updated_at = NOW()
WHERE id = $1 RETURNING *;
