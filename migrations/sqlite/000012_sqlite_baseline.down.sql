-- Reverses 000012_sqlite_baseline.up.sql. Drops triggers/virtual table first
-- (they reference knowledge_items), then all base tables. Indexes are dropped
-- automatically by SQLite when their owning table is dropped.

DROP TRIGGER IF EXISTS knowledge_items_au;
DROP TRIGGER IF EXISTS knowledge_items_ad;
DROP TRIGGER IF EXISTS knowledge_items_ai;
DROP TABLE IF EXISTS knowledge_items_fts;

DROP TABLE IF EXISTS pending_proposals;
DROP TABLE IF EXISTS review_schedule;
DROP TABLE IF EXISTS concepts;
DROP TABLE IF EXISTS knowledge_items;
DROP TABLE IF EXISTS decisions;
DROP TABLE IF EXISTS repos;
DROP TABLE IF EXISTS session_handoffs;
DROP TABLE IF EXISTS activity_log;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS projects;
DROP TABLE IF EXISTS goals;
