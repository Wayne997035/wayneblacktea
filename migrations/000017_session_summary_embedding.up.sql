-- Add summary_text and embedding columns to session_handoffs.
-- summary_text holds the Haiku-generated ≤500-char session summary written
-- by the Stop hook. embedding is reserved for future vector-similarity recall
-- (placeholder nullable BYTEA; full embedding via text-embedding-3-small is
-- a follow-up in the 5/4 backlog).

ALTER TABLE session_handoffs
    ADD COLUMN IF NOT EXISTS summary_text TEXT,
    ADD COLUMN IF NOT EXISTS embedding    BYTEA;
