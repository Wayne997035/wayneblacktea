-- Migration 000029: create vision_items table for "future ideas that can't be acted on now".
--
-- Vision items capture work that is conceptually important but currently blocked
-- by dependencies, resources, or timing. They graduate to real GTD tasks via
-- promote_vision_to_task when conditions become favourable.
--
-- Retention policy: vision_items are intentional long-lived records (unlike event/
-- observation tables). No TTL is required. Items are archived via status='dismissed'.
--
-- No FK constraints (CLAUDE.md #9 / backend-security-design.md §1.1).
-- Referential integrity for workspace_id / project_id / promoted_task_id is
-- enforced in the application layer (service / handler validate-on-write +
-- nullable JOIN on read).

CREATE TABLE IF NOT EXISTS vision_items (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        UUID,
    repo_name           TEXT,
    project_id          UUID,
    title               TEXT        NOT NULL,
    why_blocked         TEXT        NOT NULL,
    depends_on          JSONB       NOT NULL DEFAULT '[]',
    parent_initiative   TEXT,
    status              TEXT        NOT NULL DEFAULT 'open'
                            CHECK (status IN ('open','discussing','maturing','promoted','dismissed')),
    context_md          TEXT,
    promoted_task_id    UUID,
    last_discussed_at   TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Partial index: only non-dismissed items are query-hot (active backlog view).
CREATE INDEX IF NOT EXISTS idx_vision_items_status
    ON vision_items(status) WHERE status != 'dismissed';

-- Partial index: initiative grouping for roadmap views (NULL initiative rows are sparse).
CREATE INDEX IF NOT EXISTS idx_vision_items_initiative
    ON vision_items(parent_initiative) WHERE parent_initiative IS NOT NULL;
