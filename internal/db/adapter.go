package db

import (
	"context"
	"time"
)

// DBAdapter defines the interface for keypooler database operations.
// Keypooler owns tiers, tier features, and API keys.
// Executions, integrations, and dead letters are owned by pulse.
type DBAdapter interface {
	Close() error

	// Tiers
	CreateTier(ctx context.Context, tier *Tier) error
	GetTier(ctx context.Context, id string) (*Tier, error)
	GetTierByName(ctx context.Context, name string) (*Tier, error)
	GetAllTiers(ctx context.Context) ([]*Tier, error)
	DeleteTier(ctx context.Context, id string) error

	// Tier Features
	SetTierFeatures(ctx context.Context, tierID string, features []*TierFeature) error
	GetTierFeatures(ctx context.Context, tierID string) ([]*TierFeature, error)

	// Keys
	CreateKey(ctx context.Context, key *Key) error
	GetKey(ctx context.Context, id string) (*Key, error)
	GetAllKeys(ctx context.Context) ([]*Key, error)
	GetKeysByTier(ctx context.Context, tierID string) ([]*Key, error)
	DeleteKey(ctx context.Context, id string) error
	SetKeyActive(ctx context.Context, id string, active bool) error
	IncrementUsage(ctx context.Context, keyID string) error

	// Key Secrets
	GetKeySecrets(ctx context.Context, keyID string) ([]*KeySecret, error)
	SetKeySecrets(ctx context.Context, keyID string, secrets []*KeySecret) error
}

// Tier represents a key tier with feature rate limits.
type Tier struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// TierFeature represents a feature rate limit within a tier.
type TierFeature struct {
	TierID        string
	Feature       string
	RateLimit     int
	WindowSeconds int
}

// Key represents an API key in the pool.
type Key struct {
	ID           string
	Name         string
	KeyEncrypted string
	TierID       string
	IsActive     bool
	ExpiresAt    *time.Time
	UsageLimit   *int
	UsageCount   int
	Metadata     map[string]any
	CreatedAt    time.Time
}

// KeySecret is a named encrypted secret bound to a key.
type KeySecret struct {
	KeyID          string
	Name           string
	ValueEncrypted string
}
