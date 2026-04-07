package config

import (
	"fmt"
	"time"
)

// Config holds all application configuration loaded from environment variables.
type Config struct {
	// Server
	ServerPort            int
	ServerReadTimeout     time.Duration
	ServerWriteTimeout    time.Duration
	ServerIdleTimeout     time.Duration
	ServerShutdownTimeout time.Duration

	// Database
	DBPath          string
	DBMaxOpenConns  int
	DBBusyTimeoutMS int

	// Security
	EncryptionKey string
	AdminToken    string

	// Worker pool
	WorkerCount        int
	WorkerWarmupPeriod time.Duration

	// Queue
	QueueMaxSize int

	// Scripts
	ScriptsPath string

	// Runner
	RustboxURL        string
	RustboxAPIKey     string
	RustboxTimeoutSec int

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

		DBPath:          getEnv("DB_PATH", "./data/pool.db"),
		DBMaxOpenConns:  getEnvAsInt("DB_MAX_OPEN_CONNS", 1),
		DBBusyTimeoutMS: getEnvAsInt("DB_BUSY_TIMEOUT_MS", 5000),

		EncryptionKey: getEnv("ENCRYPTION_KEY", ""),
		AdminToken:    getEnv("ADMIN_TOKEN", ""),

		WorkerCount:        getEnvAsInt("WORKER_COUNT", 10),
		WorkerWarmupPeriod: getEnvAsDuration("WORKER_WARMUP_SECONDS", 30, time.Second),

		QueueMaxSize: getEnvAsInt("QUEUE_MAX_SIZE", 1000),

		ScriptsPath: getEnv("SCRIPTS_PATH", "./scripts"),

		RustboxURL:        getEnv("RUSTBOX_URL", "http://127.0.0.1:4096"),
		RustboxAPIKey:     getEnv("RUSTBOX_API_KEY", ""),
		RustboxTimeoutSec: getEnvAsInt("RUSTBOX_TIMEOUT_SECONDS", 70),

		LogLevel:    getEnv("LOG_LEVEL", "info"),
		LogFormat:   getEnv("LOG_FORMAT", "json"),
		LogRequests: getEnvAsBool("LOG_REQUESTS", true),
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
	if err := validateDBMaxOpenConns(c.DBMaxOpenConns); err != nil {
		return err
	}
	if err := validateLogLevel(c.LogLevel); err != nil {
		return err
	}
	if err := validateLogFormat(c.LogFormat); err != nil {
		return err
	}
	if err := validatePositiveInt("WORKER_COUNT", c.WorkerCount); err != nil {
		return err
	}
	if err := validatePositiveInt("QUEUE_MAX_SIZE", c.QueueMaxSize); err != nil {
		return err
	}
	if c.RustboxURL == "" {
		return fmt.Errorf("RUSTBOX_URL is required")
	}
	if c.RustboxAPIKey == "" {
		return fmt.Errorf("RUSTBOX_API_KEY is required")
	}
	if err := validatePositiveInt("RUSTBOX_TIMEOUT_SECONDS", c.RustboxTimeoutSec); err != nil {
		return err
	}
	return nil
}
