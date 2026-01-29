package utils

import (
	"os"
	"strconv"
	"strings"
)

// GetEnvAsBool gets an environment variable as a boolean.
// It returns the fallback value if the key is missing or the value is not a valid boolean.
func GetEnvAsBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fallback
		}

		return b
	}

	return fallback
}

// GetEnv gets an environment variable by key or returns the fallback value.
func GetEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}

	return fallback
}

// GetEnvAsInt gets an environment variable as an int64.
// It returns the fallback value if the key is missing or the value is not a valid integer.
func GetEnvAsInt(key string, fallback int64) int64 {
	if value, ok := os.LookupEnv(key); ok {
		i, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fallback
		}

		return i
	}

	return fallback
}

// SplitAndTrim splits a string by a separator and trims whitespace from each part.
func SplitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}
	return parts
}

// parseCORSOrigins reads CORS allowed origins from environment variable
// Expected format: CORS_ALLOWED_ORIGINS=http://localhost:5173,https://example.com
func ParseCORSOrigins() []string {
	originsStr := GetEnv("CORS_ALLOWED_ORIGINS", "http://localhost:5173")
	return SplitAndTrim(originsStr, ",")
}
