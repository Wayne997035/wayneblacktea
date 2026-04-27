-- Phase A: GTD richness.
-- Adds importance (1=high / 2=med / 3=low, distinct from priority's 1-5 scale)
-- and context (free-form discussion background) to tasks. Both NULLABLE so
-- existing tasks remain valid; the MCP add_task tool keeps these optional.

ALTER TABLE tasks
    ADD COLUMN importance SMALLINT CHECK (importance BETWEEN 1 AND 3),
    ADD COLUMN context    TEXT;
