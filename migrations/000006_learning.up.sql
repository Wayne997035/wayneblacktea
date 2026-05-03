CREATE TABLE concepts (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title       TEXT NOT NULL,
    content     TEXT NOT NULL,
    tags        TEXT[] NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE review_schedule (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    concept_id      UUID NOT NULL, -- referential integrity in code (red line #9; see migration 000026).
                                   -- A future DeleteConcept handler MUST cleanup
                                   -- review_schedule rows referencing this concept_id
                                   -- (mirrors GTDStore.DeleteTask precedent for tasks).
    stability       FLOAT NOT NULL DEFAULT 1.0,
    difficulty      FLOAT NOT NULL DEFAULT 0.3,
    due_date        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_review_at  TIMESTAMPTZ,
    review_count    INTEGER NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_review_schedule_due_date ON review_schedule(due_date ASC);
CREATE INDEX idx_review_schedule_concept_id ON review_schedule(concept_id);
