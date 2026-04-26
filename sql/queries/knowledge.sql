-- name: CreateKnowledgeItem :one
INSERT INTO knowledge_items (type, title, content, url, tags, source, learning_value)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: UpdateKnowledgeEmbedding :exec
UPDATE knowledge_items SET embedding = $2 WHERE id = $1;

-- name: SearchKnowledgeFTS :many
SELECT *, ts_rank(to_tsvector('english', title || ' ' || content), plainto_tsquery('english', $1)) AS rank
FROM knowledge_items
WHERE to_tsvector('english', title || ' ' || content) @@ plainto_tsquery('english', $1)
ORDER BY rank DESC
LIMIT $2;

-- name: GetKnowledgeByID :one
SELECT * FROM knowledge_items WHERE id = $1;

-- name: ListKnowledge :many
SELECT * FROM knowledge_items ORDER BY created_at DESC LIMIT $1 OFFSET $2;
