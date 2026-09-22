package main

import (
	"flag"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// options are the harness settings (env fallbacks match .env.local).
// pgConfig holds the PostgreSQL connection parameters (audit assertions).
type pgConfig struct {
	host, port, user, password, db string
}

type options struct {
	mediaURL   string // service-media REST base
	wsURL      string // service-websocket REST base (token + WS)
	apiURL     string // api-vehicle REST base
	company    string
	imei       string
	hmacSecret string // fallback secret; the DB row wins when present
	adminEmail string
	adminPass  string
	pg         pgConfig
	timeout    time.Duration
	sweepWait  time.Duration
}

func parseFlags() options {
	opt := options{}
	flag.StringVar(&opt.mediaURL, "media-base", envOr("E2E_MEDIA_BASE", "http://127.0.0.1:8095"), "service-media base URL")
	flag.StringVar(&opt.wsURL, "ws-base", envOr("E2E_HTTP_BASE", "http://127.0.0.1:8082"), "service-websocket base URL")
	flag.StringVar(&opt.apiURL, "api-base", envOr("E2E_API_BASE", "http://127.0.0.1:8081"), "api-vehicle base URL")
	flag.StringVar(&opt.company, "company", envOr("E2E_COMPANY", "DEV001"), "tenant company code")
	flag.StringVar(&opt.imei, "imei", envOr("E2E_IMEI", "864201040512345"), "registered device IMEI")
	flag.StringVar(&opt.hmacSecret, "hmac-secret", envOr("MEDIA_HMAC_SECRET", ""), "fallback per-company HMAC secret")
	flag.StringVar(&opt.adminEmail, "admin-email", envOr("E2E_ADMIN_EMAIL", "admin@dev001.io"), "tenant admin email")
	flag.StringVar(&opt.adminPass, "admin-password", envOr("E2E_ADMIN_PASSWORD", "Admin@123"), "tenant admin password")
	flag.StringVar(&opt.pg.host, "pg-host", envOr("POSTGRES_HOST", "127.0.0.1"), "PostgreSQL host")
	flag.StringVar(&opt.pg.port, "pg-port", envOr("POSTGRES_PORT", "5533"), "PostgreSQL port")
	flag.StringVar(&opt.pg.user, "pg-user", envOr("POSTGRES_USER", "adatrack_gps_user"), "PostgreSQL user")
	flag.StringVar(&opt.pg.password, "pg-password", envOr("POSTGRES_PASSWORD", ""), "PostgreSQL password")
	flag.StringVar(&opt.pg.db, "pg-db", envOr("POSTGRES_DB", "adatrack_gps_db"), "PostgreSQL database")
	flag.DurationVar(&opt.timeout, "timeout", 15*time.Second, "per-step timeout")
	flag.DurationVar(&opt.sweepWait, "sweep-wait", 45*time.Second, "how long to wait for the retention sweep")
	flag.Parse()
	return opt
}

// wsEndpoint builds the documented WebSocket endpoint (PRD §8.3).
func (o options) wsEndpoint(token string) string {
	parsed, err := url.Parse(o.wsURL)
	if err != nil {
		return o.wsURL
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	default:
		parsed.Scheme = "ws"
	}
	parsed.Path = "/ws/v1/adatrack"
	q := parsed.Query()
	q.Set("token", token)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// envOr reads an env var with a default.
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envInt reads an integer env var with a default.
func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return def
}
