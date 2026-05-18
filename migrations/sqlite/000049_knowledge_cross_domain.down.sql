-- SQLite no-op down: DROP COLUMN requires SQLite 3.35+ and golang-migrate
-- does not guarantee that version. Columns remain as unused nullable TEXT.
-- Fresh SQLite databases created from schema.sql will not have these columns.
SELECT 1;
