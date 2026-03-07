CREATE TABLE IF NOT EXISTS tiers (
    id TEXT PRIMARY KEY,
    name TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tier_features (
    tier_id TEXT NOT NULL REFERENCES tiers(id) ON DELETE CASCADE,
    feature TEXT NOT NULL,
    rate_per_minute INTEGER NOT NULL,
    PRIMARY KEY(tier_id, feature)
);
