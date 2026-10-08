package api

import "time"

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
	Metadata       map[string]any       `json:"metadata"`
	SecretNames    []string             `json:"secret_names"`
	Usage          map[string]rateUsage `json:"usage"`
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
