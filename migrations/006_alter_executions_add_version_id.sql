ALTER TABLE executions ADD COLUMN version_id TEXT;

CREATE INDEX IF NOT EXISTS idx_executions_version_id ON executions(version_id);
