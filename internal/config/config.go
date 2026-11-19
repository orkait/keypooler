package config

import (
	"errors"
	"os"
	"strings"
	"time"
)

const (
	envAdminToken            = "ADMIN_TOKEN"
	envDatabaseURL           = "DATABASE_URL"
	envEncryptionKey         = "ENCRYPTION_KEY"
	envPreviousEncryptionKey = "ENCRYPTION_KEY_PREVIOUS"

	Addr             = ":8080"
	ReadTimeout      = 30 * time.Second
	WriteTimeout     = 30 * time.Second
	IdleTimeout      = 120 * time.Second
	ShutdownTimeout  = 30 * time.Second
	MaxDBConns       = 4
	EventRetention   = 90 * 24 * time.Hour
	PrunePeriod      = 24 * time.Hour
	WritebackPeriod  = time.Second
	BalancePeriod    = 15 * time.Minute
	BalanceTimeout   = 15 * time.Second
	AuthCacheTTL     = time.Minute
	MigrationTimeout = 5 * time.Second
	MigrationsDir    = "./migrations"
)

var postgresSchemes = []string{"postgres://", "postgresql://"}

var (
	ErrNoAdminToken = errors.New(envAdminToken + " is required - generate with: openssl rand -hex 32")
	ErrDatabaseURL  = errors.New(envDatabaseURL + " must be a postgres:// URL")
)

type Config struct {
	DatabaseURL           string
	EncryptionKey         string
	PreviousEncryptionKey string
	AdminToken            string
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:           os.Getenv(envDatabaseURL),
		EncryptionKey:         os.Getenv(envEncryptionKey),
		PreviousEncryptionKey: os.Getenv(envPreviousEncryptionKey),
		AdminToken:            os.Getenv(envAdminToken),
	}
	if cfg.AdminToken == "" {
		return nil, ErrNoAdminToken
	}
	if !hasAnyPrefix(cfg.DatabaseURL, postgresSchemes) {
		return nil, ErrDatabaseURL
	}
	return cfg, nil
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
