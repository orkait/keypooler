package config

import (
	"encoding/hex"
	"fmt"
	"strings"
)

var (
	ValidLogLevels = map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}

	ValidLogFormats = map[string]bool{
		"json":   true,
		"pretty": true,
	}
)

// validateEncryptionKey: ENCRYPTION_KEY is optional. Empty selects plaintext
// storage (the default). When set it must be 32 bytes of hex (64 chars) for
// AES-256; new key/secret writes are then stored encrypted (enc:gcm: tag).
func validateEncryptionKey(key string) error {
	if key == "" {
		return nil
	}
	if len(key) != 64 { // 32 bytes hex = 64 characters
		return fmt.Errorf("ENCRYPTION_KEY must be empty (plaintext) or exactly 32 bytes (64 hex characters), got %d", len(key))
	}
	if _, err := hex.DecodeString(key); err != nil {
		return fmt.Errorf("ENCRYPTION_KEY must be valid hex: %w", err)
	}
	return nil
}

// validateDatabaseURL requires a Postgres URL. The URL itself is never echoed: it
// carries the password.
func validateDatabaseURL(url string) error {
	if !strings.HasPrefix(url, "postgres://") && !strings.HasPrefix(url, "postgresql://") {
		return fmt.Errorf("DATABASE_URL must be a postgres:// URL")
	}
	return nil
}

// validateAdminToken checks if admin token is present
func validateAdminToken(token string) error {
	if token == "" {
		return fmt.Errorf("ADMIN_TOKEN is required - generate with: openssl rand -hex 32")
	}
	return nil
}

// validateLogLevel checks if log level is valid
func validateLogLevel(level string) error {
	if !ValidLogLevels[level] {
		return fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error, got: %s", level)
	}
	return nil
}

// validateLogFormat checks if log format is valid
func validateLogFormat(format string) error {
	if !ValidLogFormats[format] {
		return fmt.Errorf("LOG_FORMAT must be one of: json, pretty, got: %s", format)
	}
	return nil
}

// validatePositiveInt checks if an integer value is at least 1
func validatePositiveInt(name string, value int) error {
	if value < 1 {
		return fmt.Errorf("%s must be at least 1, got %d", name, value)
	}
	return nil
}
