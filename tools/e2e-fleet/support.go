package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// pgConfig holds the PostgreSQL connection parameters (fleet asserts).
type pgConfig struct {
	host, port, user, password, db string
}

// options are the harness settings (env fallbacks match .env.local).
type options struct {
	wsBase    string // service-websocket base (login + REST playback/geocode)
	tcpAddr   string // ingestion-tcp GT06 listener
	company   string
	imei      string
	adminMail string
	adminPass string
	pg        pgConfig
	timeout   time.Duration
	wait      time.Duration
	cleanup   bool
}

func parseFlags() options {
	opt := options{}
	flag.StringVar(&opt.wsBase, "ws-base", envOr("E2E_HTTP_BASE", "http://127.0.0.1:8082"), "service-websocket base URL")
	flag.StringVar(&opt.tcpAddr, "tcp", envOr("E2E_TCP_ADDR", "127.0.0.1:9003"), "ingestion-tcp GT06 address")
	flag.StringVar(&opt.company, "company", envOr("E2E_COMPANY", "DEV001"), "tenant company code")
	flag.StringVar(&opt.imei, "imei", envOr("E2E_IMEI", "864201040512345"), "registered device IMEI")
	flag.StringVar(&opt.adminMail, "admin-email", envOr("E2E_ADMIN_EMAIL", "admin@dev001.io"), "tenant admin email")
	flag.StringVar(&opt.adminPass, "admin-password", envOr("E2E_ADMIN_PASSWORD", "Admin@123"), "tenant admin password")
	flag.StringVar(&opt.pg.host, "pg-host", envOr("POSTGRES_HOST", "127.0.0.1"), "PostgreSQL host")
	flag.StringVar(&opt.pg.port, "pg-port", envOr("POSTGRES_PORT", "5533"), "PostgreSQL port")
	flag.StringVar(&opt.pg.user, "pg-user", envOr("POSTGRES_USER", "adatrack_gps_user"), "PostgreSQL user")
	flag.StringVar(&opt.pg.password, "pg-password", envOr("POSTGRES_PASSWORD", ""), "PostgreSQL password")
	flag.StringVar(&opt.pg.db, "pg-db", envOr("POSTGRES_DB", "adatrack_gps_db"), "PostgreSQL database")
	flag.DurationVar(&opt.timeout, "timeout", 15*time.Second, "per-step timeout")
	flag.DurationVar(&opt.wait, "wait", 90*time.Second, "how long to wait for the accumulator flush")
	flag.BoolVar(&opt.cleanup, "cleanup", true, "restore the vehicle counters and remove the created trips")
	flag.Parse()
	return opt
}

// client is a thin HTTP helper (service-websocket REST).
type client struct{ hc *http.Client }

func newClient(timeout time.Duration) *client { return &client{hc: &http.Client{Timeout: timeout}} }

// request performs one HTTP call and returns status + body.
func (c *client) request(method, url string, body []byte, headers map[string]string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return 0, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, raw, err
}

// login authenticates the tenant admin and returns the access token.
func (c *client) login(base, email, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	status, raw, err := c.request(http.MethodPost, base+"/api/v1/auth/login", body,
		map[string]string{"Content-Type": "application/json"})
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("login status %d: %s", status, trim(raw, 200))
	}
	var env struct {
		Data struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("decode login: %w", err)
	}
	if env.Data.AccessToken == "" {
		return "", fmt.Errorf("login returned no token")
	}
	return env.Data.AccessToken, nil
}

// bearer builds the Authorization header.
func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// trim shortens a payload for logs.
func trim(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// envOr reads an env var with a default.
func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envFloat reads a float env var with a default.
func envFloat(key string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// upper/lower normalise tenant codes.
func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
