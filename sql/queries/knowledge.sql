-- name: CreateKnowledgeItem :one
INSERT INTO knowledge_items (type, title, content, url, tags, source, learning_value, workspace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateKnowledgeEmbedding :exec
UPDATE knowledge_items SET embedding = $2 WHERE id = $1;

-- name: SearchKnowledgeFTS :many
SELECT *, ts_rank(to_tsvector('english', title || ' ' || content), plainto_tsquery('english', sqlc.arg('query'))) AS rank
FROM knowledge_items
WHERE to_tsvector('english', title || ' ' || content) @@ plainto_tsquery('english', sqlc.arg('query'))
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY rank DESC
LIMIT sqlc.arg('limit_n');

-- name: GetKnowledgeByID :one
SELECT * FROM knowledge_items
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'));

-- name: ListKnowledge :many
SELECT * FROM knowledge_items
WHERE (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit_n') OFFSET sqlc.arg('offset_n');
