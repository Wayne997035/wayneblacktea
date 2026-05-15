-- Migration 000040: SQLite no-op.
-- SQLite already uses an FTS5 virtual table (knowledge_items_fts) defined in
-- schema.sql for full-text search. GIN expression indexes are Postgres-specific.
-- No schema changes required for the SQLite backend.
SELECT 1;
