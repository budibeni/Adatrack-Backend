package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// parseFlags reads the harness configuration (env fallbacks match .env.local).
func parseFlags() options {
	opt := options{}
	flag.StringVar(&opt.tcpAddr, "tcp", envOr("E2E_TCP_ADDR", "127.0.0.1:19003"), "ingestion-tcp GT06 address")
	flag.StringVar(&opt.imei, "imei", envOr("E2E_IMEI", "864201040512345"), "registered device IMEI")
	flag.StringVar(&opt.company, "company", envOr("E2E_COMPANY", "DEV001"), "tenant company code")
	flag.StringVar(&opt.natsURL, "nats", envOr("NATS_URL", "nats://127.0.0.1:4222"), "NATS URL")
	flag.StringVar(&opt.redisAddr, "redis", envOr("REDIS_ADDR", "127.0.0.1:6379"), "Redis address")
	flag.IntVar(&opt.redisDB, "redis-db", envInt("REDIS_DB", 3), "Redis database index")
	flag.StringVar(&opt.keyPrefix, "redis-prefix", envOr("REDIS_KEY_PREFIX", "adatrack_gps:"), "live-state key prefix")
	flag.StringVar(&opt.pg.host, "pg-host", envOr("POSTGRES_HOST", "127.0.0.1"), "PostgreSQL host")
	flag.StringVar(&opt.pg.port, "pg-port", envOr("POSTGRES_PORT", "5432"), "PostgreSQL port")
	flag.StringVar(&opt.pg.user, "pg-user", envOr("POSTGRES_USER", "adatrack"), "PostgreSQL user")
	flag.StringVar(&opt.pg.password, "pg-password", envOr("POSTGRES_PASSWORD", ""), "PostgreSQL password")
	flag.StringVar(&opt.pg.db, "pg-db", envOr("POSTGRES_DB", "adatrack_gps_db"), "PostgreSQL database")
	flag.StringVar(&opt.masterSchema, "master-schema", envOr("MASTER_DB_NAME", "adatrack_gps_master"), "master schema")
	flag.DurationVar(&opt.timeout, "timeout", 30*time.Second, "per-step wait timeout")
	flag.BoolVar(&opt.load, "load", false, "run the throughput verification instead of the single-device flow")
	flag.IntVar(&opt.rate, "rate", 1000, "load mode: total messages per second")
	flag.DurationVar(&opt.duration, "duration", 10*time.Second, "load mode: test duration")
	flag.IntVar(&opt.devices, "devices", 10, "load mode: concurrent device connections")
	flag.Parse()
	return opt
}

// push performs a non-blocking send so a slow assertion never blocks the client.
func push(ch chan []byte, data []byte) {
	select {
	case ch <- data:
	default:
	}
}

// subject builds a prefixed telemetry subject using NATS_SUBJECT_PREFIX.
func subject(kind, imei string) string {
	prefix := envOr("NATS_SUBJECT_PREFIX", "telemetry")
	return prefix + "." + kind + "." + imei
}

// trim shortens a JSON payload for logs.
func trim(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// sentCount is the load-mode counter of frames written to the socket.
var sentCount atomic.Int64

// lower lowercases a company code for schema/Redis keys.
func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// decodeRaw parses a raw telemetry payload into a generic map (assertions).
func decodeRaw(data []byte) (map[string]any, error) {
	var m map[string]any
	err := json.Unmarshal(data, &m)
	return m, err
}

// envOr reads an env var with a default.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt reads an integer env var with a default.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}

// checkResult is one E2E assertion.
type checkResult struct {
	Name   string
	Detail string
	Err    error
}

// report prints every assertion and exits non-zero when one failed.
func report(results []checkResult) {
	failed := 0
	fmt.Println()
	for _, r := range results {
		status := "PASS"
		if r.Err != nil {
			status = "FAIL"
			failed++
		}
		fmt.Printf("[%s] %-22s %s", status, r.Name, r.Detail)
		if r.Err != nil {
			fmt.Printf("  error=%v", r.Err)
		}
		fmt.Println()
	}
	fmt.Printf("\nE2E summary: %d/%d checks passed\n", len(results)-failed, len(results))
	if failed > 0 {
		os.Exit(1)
	}
}
