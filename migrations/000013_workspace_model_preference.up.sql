-- Per-workspace AI model preference.
--
-- Each workspace_id has at most one row holding the Claude model identifier
-- (e.g. "claude-haiku-4-5", "claude-sonnet-4-6") to use when the system
-- makes Claude calls on its behalf. Workspaces without a row fall back to
-- the application default — currently "claude-sonnet-4-6", mirrored in
-- workspace.DefaultModelPreference (internal/workspace/workspace.go).
--
-- Why a dedicated table instead of an extra column on `repos`:
--   * `repos` is the tracked-Git-repo entity, not the tenant. Many repos
--     belong to one workspace (workspace_id is the tenant boundary).
--   * Model preference is a tenant-level setting; storing it on `repos`
--     would force every repo row to repeat the same value.
--
-- workspace_id is the PRIMARY KEY (one row per workspace). The application
-- treats the absence of a row as "use the default model"; the default
-- VARCHAR is only consulted when an INSERT omits model_preference.

CREATE TABLE workspace_preferences (
    workspace_id     UUID PRIMARY KEY,
    model_preference VARCHAR(100) NOT NULL DEFAULT 'claude-sonnet-4-6',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
