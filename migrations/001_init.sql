DROP TABLE IF EXISTS key_secrets;
DROP TABLE IF EXISTS keys;
DROP TABLE IF EXISTS tier_features;
DROP TABLE IF EXISTS tiers;

CREATE TABLE tiers (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE tier_features (
    tier_id TEXT NOT NULL REFERENCES tiers(id) ON DELETE CASCADE,
    feature TEXT NOT NULL,
    rate_limit INTEGER NOT NULL,
    window_seconds INTEGER NOT NULL DEFAULT 60,
    PRIMARY KEY(tier_id, feature)
);
CREATE TABLE keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    key_encrypted TEXT NOT NULL,
    tier_id TEXT NOT NULL REFERENCES tiers(id),
    is_active INTEGER NOT NULL DEFAULT 1,
    expires_at TIMESTAMP,
    usage_limit INTEGER,
    usage_count INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE key_secrets (
    key_id TEXT NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    value_encrypted TEXT NOT NULL,
    PRIMARY KEY(key_id, name)
);
CREATE INDEX IF NOT EXISTS idx_keys_tier_id ON keys(tier_id);
CREATE INDEX IF NOT EXISTS idx_key_secrets_key_id ON key_secrets(key_id);
