package internal

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvOr returns the env value or a default when unset/empty.
func EnvOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// envInt parses an integer env var, falling back to def.
func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return def
}

// envBool parses a boolean env var ("1/true/yes/on"), falling back to def.
func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	case "0", "false", "f", "no", "n", "off":
		return false
	default:
		return def
	}
}

// envDurationList parses a comma separated list of milliseconds ("1000,5000").
func envDurationList(key string, def []time.Duration) []time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return def
	}
	var out []time.Duration
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ms, err := strconv.Atoi(part); err == nil {
			out = append(out, time.Duration(ms)*time.Millisecond)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// EnvBoolDefault exposes envBool for services (e.g. GT06_DATE_BCD toggles).
func EnvBoolDefault(key string, def bool) bool { return envBool(key, def) }

// EnvIntDefault exposes envInt for services.
func EnvIntDefault(key string, def int) int { return envInt(key, def) }
