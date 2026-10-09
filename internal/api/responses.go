package api

import (
	"time"

	"github.com/orkait/keypooler/internal/keypool"
)

type errorBody struct {
	Error string `json:"error"`
}

type statusBody struct {
	Status string `json:"status"`
}

type poolHealth struct {
	PoolSize   int  `json:"pool_size"`
	Encryption bool `json:"encryption"`
}

type drawnKey struct {
	KeyID    string            `json:"key_id"`
	Value    string            `json:"value"`
	Metadata map[string]any    `json:"metadata"`
	Secrets  map[string]string `json:"secrets"`
}

type addedKey struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Tier               string         `json:"tier"`
	ExpiresAt          *string        `json:"expires_at"`
	UsageLimit         *int           `json:"usage_limit"`
	UsageWindowSeconds *int           `json:"usage_window_seconds"`
	Metadata           map[string]any `json:"metadata"`
	SecretNames        []string       `json:"secret_names"`
}

type rateUsage struct {
	Used          int `json:"used"`
	Limit         int `json:"limit"`
	WindowSeconds int `json:"window_seconds"`
}

type listedKey struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	TierID         string               `json:"tier_id"`
	IsActive       bool                 `json:"is_active"`
	ExpiresAt      *string              `json:"expires_at"`
	ExhaustedUntil *string              `json:"exhausted_until"`
	UsageLimit     *int                 `json:"usage_limit"`
	UsageCount     int                  `json:"usage_count"`
	Budget         *budgetView          `json:"budget"`
	Spent          float64              `json:"spent"`
	Remaining      *float64             `json:"remaining"`
	ResetsAt       *string              `json:"resets_at"`
	Balance        *balanceView         `json:"balance"`
	Metadata       map[string]any       `json:"metadata"`
	SecretNames    []string             `json:"secret_names"`
	Usage          map[string]rateUsage `json:"usage"`
}

type balanceView struct {
	Used      float64 `json:"used"`
	Limit     float64 `json:"limit"`
	Unit      string  `json:"unit"`
	ResetsAt  *string `json:"resets_at"`
	CheckedAt string  `json:"checked_at"`
}

func balanceViewOf(b *keypool.Balance) *balanceView {
	if b == nil {
		return nil
	}
	return &balanceView{Used: b.Used, Limit: b.Limit, Unit: b.Unit, ResetsAt: rfc3339OrNil(b.ResetsAt), CheckedAt: rfc3339(b.CheckedAt)}
}

type budgetView struct {
	Amount   float64 `json:"amount"`
	Unit     string  `json:"unit"`
	ResetDay *int    `json:"reset_day"`
}

type budgetSet struct {
	KeyID  string      `json:"key_id"`
	Budget *budgetView `json:"budget"`
}

type spendRecorded struct {
	KeyID     string      `json:"key_id"`
	Spent     float64     `json:"spent"`
	Budget    *budgetView `json:"budget"`
	Remaining *float64    `json:"remaining"`
	ResetsAt  *string     `json:"resets_at"`
	Duplicate bool        `json:"duplicate"`
}

type exhaustedKey struct {
	Status string `json:"status"`
	Until  string `json:"until"`
}

type createdConsumer struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

type listedConsumer struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	IsActive    bool     `json:"is_active"`
	Scopes      []string `json:"scopes"`
}

type grantedScope struct {
	ConsumerID string `json:"consumer_id"`
	Tier       string `json:"tier"`
}

type usageEvent struct {
	KeyID      string `json:"key_id"`
	ConsumerID string `json:"consumer_id"`
	Feature    string `json:"feature"`
	CreatedAt  string `json:"created_at"`
}

type tierView struct {
	ID          string                      `json:"id"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Features    map[string]featureLimitBody `json:"features"`
}

func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func rfc3339OrNil(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := rfc3339(*t)
	return &s
}
