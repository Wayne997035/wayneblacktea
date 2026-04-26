ALTER TABLE knowledge_items
    ADD COLUMN IF NOT EXISTS source       TEXT NOT NULL DEFAULT 'manual',
    ADD COLUMN IF NOT EXISTS learning_value INT CHECK (learning_value BETWEEN 1 AND 5);
