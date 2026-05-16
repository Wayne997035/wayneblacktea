ALTER TABLE tasks ADD COLUMN checklist jsonb NOT NULL DEFAULT '[]'::jsonb;
