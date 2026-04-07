CREATE TABLE IF NOT EXISTS integration_versions (
    id TEXT PRIMARY KEY,
    integration_name TEXT NOT NULL,
    function_name TEXT NOT NULL,
    version INTEGER NOT NULL,
    runtime TEXT NOT NULL,
    feature TEXT NOT NULL,
    contract_json TEXT NOT NULL,
    code TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft',
    checksum TEXT NOT NULL,
    created_by TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    activated_at TIMESTAMP,
    UNIQUE(integration_name, function_name, version)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_integration_versions_active
ON integration_versions(integration_name, function_name)
WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_integration_versions_lookup
ON integration_versions(integration_name, function_name, version DESC);
