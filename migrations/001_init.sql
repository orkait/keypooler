-- The whole schema, for Postgres. Idempotent: every statement can run on a database
-- that already has it.
CREATE TABLE IF NOT EXISTS tiers (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS tier_features (
    tier_id TEXT NOT NULL REFERENCES tiers(id) ON DELETE CASCADE,
    feature TEXT NOT NULL,
    rate_limit INTEGER NOT NULL,
    window_seconds INTEGER NOT NULL DEFAULT 60,
    PRIMARY KEY (tier_id, feature)
);
CREATE TABLE IF NOT EXISTS keys (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    key_value TEXT NOT NULL,
    tier_id TEXT NOT NULL REFERENCES tiers(id),
    is_active BOOLEAN NOT NULL DEFAULT true,
    expires_at TIMESTAMPTZ,
    usage_limit INTEGER,
    usage_count INTEGER NOT NULL DEFAULT 0,
    usage_window_seconds INTEGER,
    usage_window_start TIMESTAMPTZ,
    -- A consumer reports a key the provider refused for the rest of its billing period;
    -- the key is skipped until this time and serves again on its own once it passes.
    exhausted_until TIMESTAMPTZ,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS key_secrets (
    key_id TEXT NOT NULL REFERENCES keys(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (key_id, name)
);
CREATE TABLE IF NOT EXISTS consumers (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    token_hash TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS consumer_scopes (
    consumer_id TEXT NOT NULL REFERENCES consumers(id) ON DELETE CASCADE,
    tier_id TEXT NOT NULL REFERENCES tiers(id) ON DELETE CASCADE,
    PRIMARY KEY (consumer_id, tier_id)
);
-- Audit rows outlive their key and consumer, so no foreign keys here.
CREATE TABLE IF NOT EXISTS usage_events (
    id TEXT PRIMARY KEY,
    key_id TEXT NOT NULL,
    consumer_id TEXT,
    feature TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_keys_tier_id ON keys(tier_id);
CREATE INDEX IF NOT EXISTS idx_consumers_token_hash ON consumers(token_hash);
CREATE INDEX IF NOT EXISTS idx_usage_events_key_created ON usage_events(key_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_events_created ON usage_events(created_at);
