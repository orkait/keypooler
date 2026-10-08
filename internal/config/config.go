package config

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	LogFormatJSON   = "json"
	LogFormatPretty = "pretty"
)

var logLevels = []string{"debug", "info", "warn", "error"}

type Config struct {
	ServerPort            int
	ServerReadTimeout     time.Duration
	ServerWriteTimeout    time.Duration
	ServerIdleTimeout     time.Duration
	ServerShutdownTimeout time.Duration

	DatabaseURL    string
	DBMaxOpenConns int

	EncryptionKey string
	AdminToken    string

	LogLevel    string
	LogFormat   string
	LogRequests bool
}

func Load() (*Config, error) {
	cfg := &Config{
		ServerPort:            envInt("SERVER_PORT", 8080),
		ServerReadTimeout:     envSeconds("SERVER_READ_TIMEOUT_SECONDS", 30),
		ServerWriteTimeout:    envSeconds("SERVER_WRITE_TIMEOUT_SECONDS", 30),
		ServerIdleTimeout:     envSeconds("SERVER_IDLE_TIMEOUT_SECONDS", 120),
		ServerShutdownTimeout: envSeconds("SERVER_SHUTDOWN_TIMEOUT_SECONDS", 30),
		DatabaseURL:           env("DATABASE_URL", ""),
		DBMaxOpenConns:        envInt("DB_MAX_OPEN_CONNS", 4),
		EncryptionKey:         env("ENCRYPTION_KEY", ""),
		AdminToken:            env("ADMIN_TOKEN", ""),
		LogLevel:              env("LOG_LEVEL", "info"),
		LogFormat:             env("LOG_FORMAT", LogFormatJSON),
		LogRequests:           envBool("LOG_REQUESTS", false),
	}
	switch {
	case cfg.AdminToken == "":
		return nil, fmt.Errorf("ADMIN_TOKEN is required - generate with: openssl rand -hex 32")
	case !strings.HasPrefix(cfg.DatabaseURL, "postgres://") && !strings.HasPrefix(cfg.DatabaseURL, "postgresql://"):
		return nil, fmt.Errorf("DATABASE_URL must be a postgres:// URL")
	case !slices.Contains(logLevels, cfg.LogLevel):
		return nil, fmt.Errorf("LOG_LEVEL must be one of %v, got: %s", logLevels, cfg.LogLevel)
	case cfg.LogFormat != LogFormatJSON && cfg.LogFormat != LogFormatPretty:
		return nil, fmt.Errorf("LOG_FORMAT must be json or pretty, got: %s", cfg.LogFormat)
	}
	return cfg, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envSeconds(key string, def int) time.Duration {
	return time.Duration(envInt(key, def)) * time.Second
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
