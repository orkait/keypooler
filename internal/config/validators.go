package config

import "fmt"

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

// validateEncryptionKey checks if encryption key is valid
func validateEncryptionKey(key string) error {
	if key == "" {
		return fmt.Errorf("ENCRYPTION_KEY is required - generate with: openssl rand -hex 32")
	}
	if len(key) != 64 { // 32 bytes hex = 64 characters
		return fmt.Errorf("ENCRYPTION_KEY must be exactly 32 bytes (64 hex characters), got %d", len(key))
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

// validateDBMaxOpenConns: local SQLite must use exactly 1 connection (single-writer
// file lock); a remote libSQL/Turso DB has no such constraint and may use a pool.
func validateDBMaxOpenConns(conns int, isRemote bool) error {
	if conns < 1 {
		return fmt.Errorf("DB_MAX_OPEN_CONNS must be at least 1, got %d", conns)
	}
	if !isRemote && conns != 1 {
		return fmt.Errorf("DB_MAX_OPEN_CONNS must be 1 for local SQLite, got %d", conns)
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
