package config

import (
	"fmt"
	"time"
)

// Config holds all keypooler configuration loaded from environment variables.
type Config struct {
	// Server
	ServerPort            int
	ServerReadTimeout     time.Duration
	ServerWriteTimeout    time.Duration
	ServerIdleTimeout     time.Duration
	ServerShutdownTimeout time.Duration

	// Database
	DatabaseURL    string // postgres:// URL
	DBMaxOpenConns int

	// Security
	EncryptionKey string
	AdminToken    string

	// Logging
	LogLevel    string
	LogFormat   string
	LogRequests bool
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	cfg := &Config{
		ServerPort:            getEnvAsInt("SERVER_PORT", 8080),
		ServerReadTimeout:     getEnvAsDuration("SERVER_READ_TIMEOUT_SECONDS", 30, time.Second),
		ServerWriteTimeout:    getEnvAsDuration("SERVER_WRITE_TIMEOUT_SECONDS", 30, time.Second),
		ServerIdleTimeout:     getEnvAsDuration("SERVER_IDLE_TIMEOUT_SECONDS", 120, time.Second),
		ServerShutdownTimeout: getEnvAsDuration("SERVER_SHUTDOWN_TIMEOUT_SECONDS", 30, time.Second),

		DatabaseURL:    getEnv("DATABASE_URL", ""),
		DBMaxOpenConns: getEnvAsInt("DB_MAX_OPEN_CONNS", 4),

		EncryptionKey: getEnv("ENCRYPTION_KEY", ""),
		AdminToken:    getEnv("ADMIN_TOKEN", ""),

		LogLevel:  getEnv("LOG_LEVEL", "info"),
		LogFormat: getEnv("LOG_FORMAT", "json"),
		// Off by default: every serve is already a usage_events row, and a line per
		// draw is the bulk of the service's log volume.
		LogRequests: getEnvAsBool("LOG_REQUESTS", false),
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if err := validateEncryptionKey(c.EncryptionKey); err != nil {
		return err
	}
	if err := validateAdminToken(c.AdminToken); err != nil {
		return err
	}
	if err := validateDatabaseURL(c.DatabaseURL); err != nil {
		return err
	}
	if err := validateLogLevel(c.LogLevel); err != nil {
		return err
	}
	if err := validateLogFormat(c.LogFormat); err != nil {
		return err
	}
	return nil
}
