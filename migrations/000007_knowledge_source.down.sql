ALTER TABLE knowledge_items
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS learning_value;
