package config

import (
	"os"
	"strconv"
	"time"
)

// getEnv reads an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt reads an environment variable as int or returns a default value
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsBool reads an environment variable as bool or returns a default value
func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}
	value, err := strconv.ParseBool(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsDuration reads an environment variable as duration or returns a default value
// The environment variable is expected to be an integer which will be multiplied by the unit
func getEnvAsDuration(key string, defaultValue int, unit time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return time.Duration(defaultValue) * unit
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return time.Duration(defaultValue) * unit
	}
	return time.Duration(value) * unit
}
