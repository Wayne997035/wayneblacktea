-- name: CreateKnowledgeItem :one
INSERT INTO knowledge_items (type, title, content, url, tags, source, learning_value, workspace_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: UpdateKnowledgeEmbedding :exec
UPDATE knowledge_items SET embedding = $2 WHERE id = $1;

-- name: SearchKnowledgeFTS :many
-- Subquery wrapper required: Postgres does not allow ORDER BY to reference SELECT-list
-- alias (rank) in the same query level — wrapping materialises it first.
SELECT * FROM (
    SELECT *, ts_rank(to_tsvector('english', title || ' ' || content), plainto_tsquery('english', sqlc.arg('query'))) AS rank
    FROM knowledge_items
    WHERE to_tsvector('english', title || ' ' || content) @@ plainto_tsquery('english', sqlc.arg('query'))
      AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
) sub
ORDER BY rank DESC
LIMIT sqlc.arg('limit_n');

-- name: GetKnowledgeByID :one
SELECT id, type, title, content, url, tags, embedding, created_at, updated_at, source,
       learning_value, workspace_id, importance, recall_count, last_recalled_at,
       base_lambda, archived_at, parent_id, heading_path, heading_level
FROM knowledge_items
WHERE id = sqlc.arg('id')
  AND (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'));

-- name: ListKnowledge :many
SELECT id, type, title, content, url, tags, embedding, created_at, updated_at, source,
       learning_value, workspace_id, importance, recall_count, last_recalled_at,
       base_lambda, archived_at, parent_id, heading_path, heading_level
FROM knowledge_items
WHERE (sqlc.narg('workspace_id')::uuid IS NULL OR workspace_id = sqlc.narg('workspace_id'))
ORDER BY created_at DESC
LIMIT sqlc.arg('limit_n') OFFSET sqlc.arg('offset_n');
