CREATE TABLE IF NOT EXISTS dead_letter (
    id TEXT PRIMARY KEY,
    execution_id TEXT NOT NULL REFERENCES executions(id),
    script TEXT NOT NULL,
    function_name TEXT NOT NULL,
    input TEXT,
    error TEXT,
    attempts INTEGER,
    failed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_dead_letter_failed_at ON dead_letter(failed_at);
