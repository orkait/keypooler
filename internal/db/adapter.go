package db

import (
	"context"
	"time"
)

// DBAdapter defines the interface for keypooler database operations.
// Keypooler owns tiers, tier features, API keys, consumers, and usage events.
// Executions, integrations, and dead letters are owned by pulse.
type DBAdapter interface {
	Close() error

	// Tiers
	CreateTier(ctx context.Context, tier *Tier) error
	GetTier(ctx context.Context, id string) (*Tier, error)
	GetTierByName(ctx context.Context, name string) (*Tier, error)
	GetAllTiers(ctx context.Context) ([]*Tier, error)
	DeleteTier(ctx context.Context, id string) error
	UpdateTierDescription(ctx context.Context, id, description string) error

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
	// ResetUsageWindow zeroes usage_count and stamps a fresh usage_window_start.
	// Used when a key's monthly (windowed) usage budget rolls over.
	ResetUsageWindow(ctx context.Context, keyID string, start time.Time) error

	// Key Secrets
	GetKeySecrets(ctx context.Context, keyID string) ([]*KeySecret, error)
	SetKeySecrets(ctx context.Context, keyID string, secrets []*KeySecret) error

	// Consumers
	CreateConsumer(ctx context.Context, consumer *Consumer) error
	GetConsumerByTokenHash(ctx context.Context, tokenHash string) (*Consumer, error)
	GetAllConsumers(ctx context.Context) ([]*Consumer, error)
	DeleteConsumer(ctx context.Context, id string) error
	AddConsumerScope(ctx context.Context, consumerID, tierID string) error
	GetConsumerScopes(ctx context.Context, consumerID string) ([]string, error)

	// Usage Events (audit)
	RecordUsageEvent(ctx context.Context, keyID, consumerID, feature string) error
	ListUsageEvents(ctx context.Context, limit int) ([]*UsageEvent, error)
}

// Tier represents a key tier with feature rate limits.
type Tier struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
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
	// UsageWindowSeconds, when set, makes the usage_limit a per-window budget
	// (e.g. 2592000 = 30 days). nil means the limit is a lifetime cap.
	UsageWindowSeconds *int
	// UsageWindowStart is the start of the current usage window. nil until the
	// first window opens.
	UsageWindowStart *time.Time
	Metadata         map[string]any
	CreatedAt        time.Time
}

// KeySecret is a named encrypted secret bound to a key.
type KeySecret struct {
	KeyID          string
	Name           string
	ValueEncrypted string
}

// Consumer is a scoped API client. It authenticates with a bearer token whose
// sha256 hash is stored; the plaintext token is shown exactly once at creation.
type Consumer struct {
	ID          string
	Name        string
	TokenHash   string
	Description string
	IsActive    bool
	CreatedAt   time.Time
}

// UsageEvent is an append-only audit record written on each successful key serve.
type UsageEvent struct {
	ID         string
	KeyID      string
	ConsumerID string
	Feature    string
	CreatedAt  time.Time
}
