-- Guard bypasses: operator-approved exceptions to would-deny classification.
-- Resolution order in wbt-guard: file > dir > repo > global (narrowest wins).
-- All bypass lookups use (scope, target) — keep those columns indexed.

CREATE TABLE IF NOT EXISTS guard_bypasses (
    id          UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NULL,
    scope       TEXT        NOT NULL CHECK (scope IN ('global','repo','dir','file')),
    target      TEXT        NOT NULL,
    tool_name   TEXT        NULL,  -- NULL means "all tools"
    reason      TEXT        NOT NULL,
    created_by  TEXT
);

-- Lookup pattern: find active bypass for a (scope, target) pair.
CREATE INDEX idx_guard_bypasses_scope_target
    ON guard_bypasses (scope, target);

-- Expiry scan: periodic cleanup / TTL checks.
CREATE INDEX idx_guard_bypasses_expires_at
    ON guard_bypasses (expires_at)
    WHERE expires_at IS NOT NULL;

-- NOTE: guard_events.bypass_id is intentionally NOT a FK to guard_bypasses.id.
-- Operator preference (5/3): no FK constraints in this schema. RevokeBypass uses
-- a hard DELETE; older guard_events rows may keep a stale bypass_id UUID after
-- revocation — that's accepted as audit drift, queried via JOIN nullability.
