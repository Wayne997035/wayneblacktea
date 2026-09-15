-- Ghost-task detection (read-only) — F184-09
--
-- Detects rows created by the pre-F184-08 bug where an MCP `complete_task`
-- tool call was misclassified by the auto-log classifier as a brand new
-- task. Root cause: internal/ai/activity_classifier.go's `classifierSystemPrompt`
-- `is_task` branch had no guard excluding "this activity is itself
-- reporting a task's completion" — the model would echo the same
-- completion event back as `is_task=true, task_title="Complete task <id>"`.
-- Fixed at the code level (not the prompt) by an unconditional guard in
-- internal/mcp/middleware_classify.go's autoCaptureMCPTask, keyed on the
-- structured `toolName == "complete_task"` field.
--
-- This query is documentation only. It is NOT executed as part of this
-- change, and this change does NOT delete, cancel, or otherwise modify any
-- row it matches — the 28 known ghost rows stay exactly as they are.
-- Running it (and acting on the result) is explicitly out of scope here;
-- ticket 92991ba2 reserves that for a separate cleanup ticket.
--
-- Portable across both backends (no regex, no Postgres-only operators) —
-- see wayneblacktea/CLAUDE.md "Dual backend, runtime-resolved".
--
-- Matches the exact shape `synthesiseClassifierDescription`
-- (internal/mcp/middleware_classify.go:366-379) produces on the auto-accept
-- path: a JSON-marshalled {"artifact":...,"task_id":...} object, followed
-- by " | " and the classifier's rationale text. `kind = 'general'`
-- corroborates the source further, since `validator.KindGeneral` is the
-- only kind this auto-accept path ever sets
-- (internal/mcp/middleware_classify.go:257).
--
-- CAUTION: a pre-existing, human-authored task that happens to be titled
-- "Complete task ..." would also match this query. The `kind`/`description`
-- clauses reduce false positives but do not eliminate them — the result
-- set MUST be manually inspected (e.g. checking `description` really is a
-- classifier-synthesised JSON blob) before any row is treated as a ghost.
SELECT id, title, description, status, created_at
FROM tasks
WHERE title LIKE 'Complete task %'
  AND kind = 'general'
  AND description LIKE '%"task_id":%'
ORDER BY created_at;
