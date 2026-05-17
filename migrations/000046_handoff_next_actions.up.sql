ALTER TABLE session_handoffs ADD COLUMN IF NOT EXISTS next_actions JSONB NOT NULL DEFAULT '[]'::jsonb;
