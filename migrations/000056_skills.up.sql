-- Migration 000056: Skill Library — reusable Claude Code skill definitions.
-- Each row captures a named skill with its triggers, steps, failure modes,
-- and verification checklist so Claude sessions can extract, search, and apply
-- proven approaches.
--
-- No FOREIGN KEY constraints (CLAUDE.md red-line §9): source_atom_ids is a
-- code-layer reference to memory_atoms.id; referential integrity is enforced
-- by service-layer query-on-write and nullable JOIN on read.
CREATE TABLE IF NOT EXISTS skills (
    id                     UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id           UUID,
    name                   TEXT         NOT NULL,
    description            TEXT         NOT NULL DEFAULT '',
    triggers               JSONB        NOT NULL DEFAULT '[]',
    steps                  JSONB        NOT NULL DEFAULT '[]',
    failure_modes          JSONB        NOT NULL DEFAULT '[]',
    verification_checklist JSONB        NOT NULL DEFAULT '[]',
    examples               JSONB        NOT NULL DEFAULT '[]',
    source_atom_ids        JSONB        NOT NULL DEFAULT '[]',
    success_count          INT          NOT NULL DEFAULT 0,
    failure_count          INT          NOT NULL DEFAULT 0,
    last_used_at           TIMESTAMPTZ,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_skills_workspace  ON skills(workspace_id) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_skills_name       ON skills(name);
CREATE INDEX IF NOT EXISTS idx_skills_success    ON skills(workspace_id, success_count DESC) WHERE workspace_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_skills_last_used  ON skills(last_used_at DESC NULLS LAST);
COMMENT ON TABLE skills IS 'Curated skill library — intentionally no TTL; rows accumulate by design (explicit extract_skill only).';
COMMENT ON COLUMN skills.source_atom_ids IS 'Code-layer refs to memory_atoms.id; no FK per project red-line #9';
