CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE knowledge_items (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    type        TEXT NOT NULL CHECK (type IN ('article','til','bookmark','zettelkasten')),
    title       TEXT NOT NULL,
    content     TEXT NOT NULL DEFAULT '',
    url         TEXT,
    tags        TEXT[] NOT NULL DEFAULT '{}',
    embedding   vector(768),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_knowledge_type ON knowledge_items(type);
CREATE INDEX idx_knowledge_created_at ON knowledge_items(created_at DESC);
CREATE INDEX idx_knowledge_embedding ON knowledge_items
    USING ivfflat (embedding vector_cosine_ops) WITH (lists = 50);
