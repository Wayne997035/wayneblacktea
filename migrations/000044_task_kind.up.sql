ALTER TABLE tasks ADD COLUMN kind text NOT NULL DEFAULT 'general' CHECK (kind IN ('general','fix-pr','feature','refactor','research','chore'));
