package config

import (
	"os"
	"strconv"
)

type Config struct {
	DBUser            string
	DBPass            string
	DBHost            string
	DBPort            string
	DBName            string
	DBMaxConns        int
	DBMinConns        int
	RedisAddr         string
	RedisPassword     string
	NatsURL           string
}

func Load() *Config {
	maxConns, _ := strconv.Atoi(getEnv("DB_MAX_CONNS", "50"))
	minConns, _ := strconv.Atoi(getEnv("DB_MIN_CONNS", "5"))

	return &Config{
		DBUser:        getEnv("DB_USER", "postgres"),
		DBPass:        getEnv("DB_PASSWORD", "postgres"),
		DBHost:        getEnv("DB_HOST", "localhost"),
		DBPort:        getEnv("DB_PORT", "5432"),
		DBName:        getEnv("DB_NAME", "adatrack_gps_master"),
		DBMaxConns:    maxConns,
		DBMinConns:    minConns,
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		NatsURL:       getEnv("NATS_URL", "nats://localhost:4222"),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}
