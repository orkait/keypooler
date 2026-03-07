CREATE TABLE IF NOT EXISTS executions (
    id TEXT PRIMARY KEY,
    script TEXT NOT NULL,
    function_name TEXT NOT NULL,
    key_id TEXT,
    status TEXT DEFAULT 'pending',
    trigger_type TEXT DEFAULT 'api',
    callback_url TEXT,
    input TEXT,
    output TEXT,
    error TEXT,
    attempts INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_executions_status ON executions(status);
CREATE INDEX IF NOT EXISTS idx_executions_script ON executions(script, function_name);
