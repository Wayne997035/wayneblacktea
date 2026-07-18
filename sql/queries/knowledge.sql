-- name: UpdateKnowledgeEmbedding :exec
UPDATE knowledge_items SET embedding = $2 WHERE id = $1;
