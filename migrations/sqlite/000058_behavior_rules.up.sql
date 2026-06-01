CREATE TABLE IF NOT EXISTS behavior_rules (
    id            TEXT PRIMARY KEY,
    workspace_id  TEXT,
    condition     TEXT NOT NULL,
    action        TEXT NOT NULL,
    source_type   TEXT NOT NULL
                    CHECK (source_type IN ('reflection','outcome','manual')),
    source_id     TEXT,
    confidence    REAL NOT NULL DEFAULT 0.50
                    CHECK (confidence BETWEEN 0.00 AND 1.00),
    status        TEXT NOT NULL DEFAULT 'proposed'
                    CHECK (status IN ('proposed','active','rejected','deprecated')),
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_behavior_rules_workspace_id
    ON behavior_rules(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_behavior_rules_status
    ON behavior_rules(status);
CREATE INDEX IF NOT EXISTS idx_behavior_rules_status_created_at
    ON behavior_rules(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_behavior_rules_source
    ON behavior_rules(source_type, source_id) WHERE source_id IS NOT NULL;
-- 365-day retention for rejected/deprecated rows enforced by the scheduler's
-- daily-behavior-rule-prune job per backend-security-design.md §1.3.
-- Active and proposed rows are never auto-pruned.
-- SQLite: BehaviorRuleStore.PruneOlderThan deletes rows where
-- status IN ('rejected','deprecated') AND created_at < cutoff (RFC3339 TEXT comparison).
