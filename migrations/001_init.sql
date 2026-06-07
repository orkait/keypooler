-- Consolidated, destructive (DROP+CREATE) schema. FK-safe drop order:
-- children before parents.
DROP TABLE IF EXISTS usage_events;
DROP TABLE IF EXISTS consumer_scopes;
DROP TABLE IF EXISTS consumers;
DROP TABLE IF EXISTS key_secrets;
DROP TABLE IF EXISTS keys;
DROP TABLE IF EXISTS tier_features;
DROP TABLE IF EXISTS tiers;

CREATE TABLE tiers (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    description TEXT NOT NULL DEFAULT '',
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
    usage_window_seconds INTEGER,
    usage_window_start TIMESTAMP,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE key_secrets (
    key_id TEXT NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    value_encrypted TEXT NOT NULL,
    PRIMARY KEY(key_id, name)
);
CREATE TABLE consumers (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    token_hash TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_active INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE consumer_scopes (
    consumer_id TEXT NOT NULL REFERENCES consumers(id) ON DELETE CASCADE,
    tier_id TEXT NOT NULL REFERENCES tiers(id) ON DELETE CASCADE,
    PRIMARY KEY(consumer_id, tier_id)
);
CREATE TABLE usage_events (
    id TEXT PRIMARY KEY,
    key_id TEXT NOT NULL,
    consumer_id TEXT,
    feature TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_keys_tier_id ON keys(tier_id);
CREATE INDEX IF NOT EXISTS idx_key_secrets_key_id ON key_secrets(key_id);
CREATE INDEX IF NOT EXISTS idx_consumers_token_hash ON consumers(token_hash);
CREATE INDEX IF NOT EXISTS idx_consumer_scopes_consumer_id ON consumer_scopes(consumer_id);
CREATE INDEX IF NOT EXISTS idx_usage_events_key_created ON usage_events(key_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_events_created ON usage_events(created_at);
