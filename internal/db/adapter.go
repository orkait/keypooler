package db

import (
	"context"
	"time"
)

// DBAdapter defines the interface for all database operations.
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

	// Executions
	CreateExecution(ctx context.Context, exec *Execution) error
	GetExecution(ctx context.Context, id string) (*Execution, error)
	UpdateExecutionStatus(ctx context.Context, id, status, keyID string, attempts int) error
	UpdateExecutionResult(ctx context.Context, id, status, output, errMsg string, completedAt time.Time) error
	GetExecutionsByStatus(ctx context.Context, status string, limit int) ([]*Execution, error)

	// Dead Letter
	CreateDeadLetter(ctx context.Context, dl *DeadLetter) error
	GetDeadLetters(ctx context.Context, limit int) ([]*DeadLetter, error)
	GetDeadLetter(ctx context.Context, id string) (*DeadLetter, error)
	DeleteDeadLetter(ctx context.Context, id string) error
}

// Tier represents a key tier with feature rate limits.
type Tier struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// TierFeature represents a feature rate limit within a tier.
type TierFeature struct {
	TierID       string
	Feature      string
	RatePerMinute int
}

// Key represents an API key in the pool.
type Key struct {
	ID           string
	Name         string
	KeyEncrypted string
	TierID       string
	IsActive     bool
	CreatedAt    time.Time
}

// Execution represents a script execution.
type Execution struct {
	ID           string
	Script       string
	FunctionName string
	KeyID        *string
	Status       string
	TriggerType  string
	CallbackURL  *string
	Input        *string
	Output       *string
	Error        *string
	Attempts     int
	CreatedAt    time.Time
	CompletedAt  *time.Time
}

// DeadLetter represents a failed execution in the dead letter queue.
type DeadLetter struct {
	ID           string
	ExecutionID  string
	Script       string
	FunctionName string
	Input        *string
	Error        *string
	Attempts     int
	FailedAt     time.Time
}

// Execution status constants.
const (
	StatusPending  = "pending"
	StatusRunning  = "running"
	StatusSuccess  = "success"
	StatusFailed   = "failed"
	StatusRetrying = "retrying"
)

// Trigger type constants.
const (
	TriggerAPI      = "api"
	TriggerSchedule = "schedule"
)
