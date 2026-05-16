ALTER TABLE tasks ADD COLUMN kind TEXT NOT NULL DEFAULT 'general' CHECK (kind IN ('general','fix-pr','feature','refactor','research','chore'));
