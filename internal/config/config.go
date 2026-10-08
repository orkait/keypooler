package config

import (
	"errors"
	"os"
	"strings"
	"time"
)

const (
	envAdminToken    = "ADMIN_TOKEN"
	envDatabaseURL   = "DATABASE_URL"
	envEncryptionKey = "ENCRYPTION_KEY"

	Addr            = ":8080"
	ReadTimeout     = 30 * time.Second
	WriteTimeout    = 30 * time.Second
	IdleTimeout     = 120 * time.Second
	ShutdownTimeout = 30 * time.Second
	MaxDBConns      = 4
)

var postgresSchemes = []string{"postgres://", "postgresql://"}

var (
	ErrNoAdminToken = errors.New(envAdminToken + " is required - generate with: openssl rand -hex 32")
	ErrDatabaseURL  = errors.New(envDatabaseURL + " must be a postgres:// URL")
)

type Config struct {
	DatabaseURL   string
	EncryptionKey string
	AdminToken    string
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:   os.Getenv(envDatabaseURL),
		EncryptionKey: os.Getenv(envEncryptionKey),
		AdminToken:    os.Getenv(envAdminToken),
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
