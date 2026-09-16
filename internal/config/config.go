package config

import "os"

type Config struct {
	DBUser     string
	DBPass     string
	DBHost     string
	DBPort     string
	DBName     string
	RedisAddr  string
	NatsURL    string
}

func Load() *Config {
	return &Config{
		DBUser:    getEnv("DB_USER", "postgres"),
		DBPass:    getEnv("DB_PASSWORD", "postgres"),
		DBHost:    getEnv("DB_HOST", "localhost"),
		DBPort:    getEnv("DB_PORT", "5432"),
		DBName:    getEnv("DB_NAME", "adatrack_gps_master"),
		RedisAddr: getEnv("REDIS_ADDR", "localhost:6379"),
		NatsURL:   getEnv("NATS_URL", "nats://localhost:4222"),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}
