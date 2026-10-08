package db

import (
	"context"
	"time"
)

type DBAdapter interface {
	Close() error

	CreateTier(ctx context.Context, tier *Tier, features []*TierFeature) error
	GetTierByName(ctx context.Context, name string) (*Tier, error)
	GetAllTiers(ctx context.Context) ([]*Tier, error)
	UpdateTierDescription(ctx context.Context, id, description string) error
	SetTierFeatures(ctx context.Context, tierID string, features []*TierFeature) error
	DeleteTier(ctx context.Context, id string) error
	TierFeaturesByTier(ctx context.Context) (map[string][]*TierFeature, error)

	CreateKey(ctx context.Context, key *Key, secrets []*KeySecret) error
	GetAllKeys(ctx context.Context) ([]*Key, error)
	DeleteKey(ctx context.Context, id string) error
	AddUsage(ctx context.Context, keyID string, n int) error
	ResetUsageWindow(ctx context.Context, keyID string, start time.Time, count int) error
	SetKeyExhausted(ctx context.Context, keyID string, until time.Time) error
	UpdateKeyBudget(ctx context.Context, keyID string, budget *Budget) error
	RecordSpend(ctx context.Context, event *SpendEvent, periodStart *time.Time) (spent float64, duplicate bool, err error)
	KeySecretsByKey(ctx context.Context) (map[string][]*KeySecret, error)

	CreateConsumer(ctx context.Context, consumer *Consumer) error
	GetConsumerByTokenHash(ctx context.Context, tokenHash string) (*Consumer, error)
	GetAllConsumers(ctx context.Context) ([]*Consumer, error)
	DeleteConsumer(ctx context.Context, id string) error
	AddConsumerScope(ctx context.Context, consumerID, tierID string) error
	GetConsumerScopes(ctx context.Context, consumerID string) ([]string, error)
	ConsumerScopesByConsumer(ctx context.Context) (map[string][]string, error)

	RecordUsageEvents(ctx context.Context, events []*UsageEvent) error
	ListUsageEvents(ctx context.Context, limit int) ([]*UsageEvent, error)
}

type Tier struct {
	ID          string
	Name        string
	Description string
	CreatedAt   time.Time
}

type TierFeature struct {
	TierID        string
	Feature       string
	RateLimit     int
	WindowSeconds int
}

type Key struct {
	ID                 string
	Name               string
	KeyValue           string
	TierID             string
	IsActive           bool
	ExpiresAt          *time.Time
	UsageLimit         *int
	UsageCount         int
	UsageWindowSeconds *int
	UsageWindowStart   *time.Time
	ExhaustedUntil     *time.Time
	Budget             *Budget
	Spent              float64
	SpentPeriodStart   *time.Time
	Metadata           map[string]any
	CreatedAt          time.Time
}

type Budget struct {
	Amount   float64
	Unit     string
	ResetDay *int
}

type SpendEvent struct {
	ID         string
	KeyID      string
	ConsumerID string
	Amount     float64
	Unit       string
	RequestID  string
	CreatedAt  time.Time
}

type KeySecret struct {
	KeyID string
	Name  string
	Value string
}

type Consumer struct {
	ID          string
	Name        string
	TokenHash   string
	Description string
	IsActive    bool
	CreatedAt   time.Time
}

type UsageEvent struct {
	ID         string
	KeyID      string
	ConsumerID string
	Feature    string
	CreatedAt  time.Time
}
