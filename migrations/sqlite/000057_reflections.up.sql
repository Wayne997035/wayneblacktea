CREATE TABLE IF NOT EXISTS reflections (
    id                   TEXT PRIMARY KEY,
    workspace_id         TEXT,
    type                 TEXT NOT NULL
                            CHECK (type IN ('daily','weekly','task','decision',
                                            'proposal','knowledge','system')),
    related_entity_type  TEXT,
    related_entity_id    TEXT,
    summary              TEXT NOT NULL DEFAULT '',
    insights             TEXT NOT NULL DEFAULT 'null',
    patterns_detected    TEXT NOT NULL DEFAULT 'null',
    suggested_actions    TEXT NOT NULL DEFAULT 'null',
    confidence           REAL NOT NULL DEFAULT 0.0
                            CHECK (confidence BETWEEN 0.0 AND 1.0),
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_reflections_workspace_id    ON reflections(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reflections_type_created_at ON reflections(type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reflections_related_entity  ON reflections(related_entity_type, related_entity_id) WHERE related_entity_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reflections_created_at      ON reflections(created_at DESC);
-- 180-day retention enforced by the scheduler's daily-reflection-prune job per
-- backend-security-design.md §1.3. SQLite: ReflectionStore.PruneOlderThan deletes
-- rows where created_at < cutoff (RFC3339 TEXT comparison).
