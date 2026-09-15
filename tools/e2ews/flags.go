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
type options struct {
	baseURL                         string // http://127.0.0.1:8082
	tcpAddr                         string // ingestion-tcp GT06 listener
	imei                            string // registered device IMEI of the tested vehicle
	plate                           string // expected plate number of that vehicle
	company                         string
	adminEmail, adminPassword       string
	platformEmail, platformPassword string
	driverEmail, driverPassword     string
	operatorEmail, operatorPassword string
	timeout                         time.Duration
	pg                              pgConfig
	masterSchema                    string
}

// pgConfig holds the PostgreSQL connection parameters (audit assertions).
type pgConfig struct {
	host, port, user, password, db string
}

// parseFlags reads the harness configuration.
func parseFlags() options {
	opt := options{}
	flag.StringVar(&opt.baseURL, "base", envOr("E2E_HTTP_BASE", "http://127.0.0.1:8082"),
		"service-websocket base URL")
	flag.StringVar(&opt.tcpAddr, "tcp", envOr("E2E_TCP_ADDR", "127.0.0.1:9003"),
		"ingestion-tcp GT06 address (device frame source)")
	flag.StringVar(&opt.imei, "imei", envOr("E2E_IMEI", "864201040512345"), "registered device IMEI")
	flag.StringVar(&opt.plate, "plate", envOr("E2E_PLATE", "B 1234 XYZ"), "expected plate number")
	flag.StringVar(&opt.company, "company", envOr("E2E_COMPANY", "DEV001"), "tenant company code")
	flag.StringVar(&opt.adminEmail, "admin-email", envOr("E2E_ADMIN_EMAIL", "admin@dev001.io"), "tenant admin email")
	flag.StringVar(&opt.adminPassword, "admin-password", envOr("E2E_ADMIN_PASSWORD", "Admin@123"), "tenant admin password")
	flag.StringVar(&opt.platformEmail, "platform-email", envOr("E2E_PLATFORM_EMAIL", "platform@adatrackgps.local"),
		"platform SuperAdmin email")
	flag.StringVar(&opt.platformPassword, "platform-password", envOr("E2E_PLATFORM_PASSWORD", "Platform@123"),
		"platform SuperAdmin password")
	flag.StringVar(&opt.driverEmail, "driver-email", envOr("E2E_DRIVER_EMAIL", "driver@dev001.io"),
		"driver email (row-level RBAC checks)")
	flag.StringVar(&opt.driverPassword, "driver-password", envOr("E2E_DRIVER_PASSWORD", "Admin@123"), "driver password")
	flag.StringVar(&opt.operatorEmail, "operator-email", envOr("E2E_OPERATOR_EMAIL", "operator@dev001.io"),
		"operator email")
	flag.StringVar(&opt.operatorPassword, "operator-password", envOr("E2E_OPERATOR_PASSWORD", "Admin@123"), "operator password")
	flag.DurationVar(&opt.timeout, "timeout", 10*time.Second, "per-step timeout")
	flag.StringVar(&opt.pg.host, "pg-host", envOr("POSTGRES_HOST", "127.0.0.1"), "PostgreSQL host")
	flag.StringVar(&opt.pg.port, "pg-port", envOr("POSTGRES_PORT", "5533"), "PostgreSQL port")
	flag.StringVar(&opt.pg.user, "pg-user", envOr("POSTGRES_USER", "adatrack"), "PostgreSQL user")
	flag.StringVar(&opt.pg.password, "pg-password", envOr("POSTGRES_PASSWORD", ""), "PostgreSQL password")
	flag.StringVar(&opt.pg.db, "pg-db", envOr("POSTGRES_DB", "adatrack_gps_db"), "PostgreSQL database")
	flag.StringVar(&opt.masterSchema, "master-schema", envOr("MASTER_DB_NAME", "adatrack_gps_master"), "master schema")
	flag.Parse()
	return opt
}

// wsURL builds the documented WebSocket endpoint (PRD §8.3).
func (o options) wsURL(token string) string {
	parsed, err := url.Parse(o.baseURL)
	if err != nil {
		return o.baseURL
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	default:
		parsed.Scheme = "ws"
	}
	parsed.Path = "/ws/v1/adatrack"
	query := parsed.Query()
	query.Set("token", token)
	parsed.RawQuery = query.Encode()
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
