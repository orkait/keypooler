CREATE TABLE IF NOT EXISTS keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    key_encrypted TEXT NOT NULL,
    tier_id TEXT NOT NULL REFERENCES tiers(id),
    is_active INTEGER DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_keys_tier_id ON keys(tier_id);
