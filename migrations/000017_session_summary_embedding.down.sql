-- Revert: drop summary_text and embedding columns from session_handoffs.
ALTER TABLE session_handoffs
    DROP COLUMN IF EXISTS summary_text,
    DROP COLUMN IF EXISTS embedding;
