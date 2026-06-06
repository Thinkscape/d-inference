// Package env provides shared environment variable helpers and constants
// used across coordinator subpackages. It exists to avoid import cycles
// between packages that independently reference the same env var prefix
// and helper functions.
package env

import (
	"os"
	"strconv"
	"strings"
)

// EnvPrefix is the namespace prefix for all coordinator environment variables.
const EnvPrefix = "EIGENINFERENCE"

// EnvOr reads key from the environment and returns fallback when the key is
// missing or empty.
func EnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// FirstNonEmpty returns the first non-empty string from vals.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// EnvFloat reads key from the environment as a float64, returning fallback
// when the key is missing, empty, or unparseable.
func EnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// EnvInt reads key from the environment as an int, returning fallback when
// the key is missing, empty, or unparseable.
func EnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// EnvBool reads key from the environment as a boolean, returning fallback when
// the key is missing, empty, or unparseable. Accepts 1/true/yes/on and
// 0/false/no/off (case-insensitive).
func EnvBool(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
