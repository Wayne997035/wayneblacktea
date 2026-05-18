-- SQLite no-op down: DROP COLUMN requires SQLite 3.35+ and golang-migrate
-- does not guarantee that version. Column remains as unused nullable TEXT.
-- Fresh SQLite databases created from schema.sql will not have this column.
SELECT 1;
