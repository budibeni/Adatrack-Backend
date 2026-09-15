package controllers

import (
	"os"
	"strconv"
	"strings"
)

// envInt reads an integer env var with a default (used for the configurable
// Teltonika fuel IO IDs).
func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	return def
}
