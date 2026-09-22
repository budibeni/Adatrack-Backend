package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// pgConfig holds the PostgreSQL connection parameters (td_fuel_logs asserts).
type pgConfig struct {
	host, port, user, password, db string
}

// options are the harness settings (env fallbacks match .env.local).
type options struct {
	wsBase    string // service-websocket base (login + WS)
	apiBase   string // api-vehicle base (fuel config + history)
	tcpAddr   string // ingestion-tcp GT06 listener
	company   string
	imei      string
	high      float64 // first sensor height (cm) — the baseline
	low       float64 // second sensor height (cm) — the drop
	adminMail string
	adminPass string
	pg        pgConfig
	timeout   time.Duration
	wait      time.Duration
	dryRunCfg bool // set the drop threshold when no fuel config exists
}

func parseFlags() options {
	opt := options{}
	flag.StringVar(&opt.wsBase, "ws-base", envOr("E2E_HTTP_BASE", "http://127.0.0.1:8082"), "service-websocket base URL")
	flag.StringVar(&opt.apiBase, "api-base", envOr("E2E_API_BASE", "http://127.0.0.1:8081"), "api-vehicle base URL")
	flag.StringVar(&opt.tcpAddr, "tcp", envOr("E2E_TCP_ADDR", "127.0.0.1:9003"), "ingestion-tcp GT06 address")
	flag.StringVar(&opt.company, "company", envOr("E2E_COMPANY", "DEV001"), "tenant company code")
	flag.StringVar(&opt.imei, "imei", envOr("E2E_IMEI", "864201040512345"), "registered device IMEI")
	flag.Float64Var(&opt.high, "high-cm", envFloat("E2E_FUEL_HIGH_CM", 90), "baseline sensor height (cm)")
	flag.Float64Var(&opt.low, "low-cm", envFloat("E2E_FUEL_LOW_CM", 40), "dropped sensor height (cm)")
	flag.StringVar(&opt.adminMail, "admin-email", envOr("E2E_ADMIN_EMAIL", "admin@dev001.io"), "tenant admin email")
	flag.StringVar(&opt.adminPass, "admin-password", envOr("E2E_ADMIN_PASSWORD", "Admin@123"), "tenant admin password")
	flag.StringVar(&opt.pg.host, "pg-host", envOr("POSTGRES_HOST", "127.0.0.1"), "PostgreSQL host")
	flag.StringVar(&opt.pg.port, "pg-port", envOr("POSTGRES_PORT", "5533"), "PostgreSQL port")
	flag.StringVar(&opt.pg.user, "pg-user", envOr("POSTGRES_USER", "adatrack_gps_user"), "PostgreSQL user")
	flag.StringVar(&opt.pg.password, "pg-password", envOr("POSTGRES_PASSWORD", ""), "PostgreSQL password")
	flag.StringVar(&opt.pg.db, "pg-db", envOr("POSTGRES_DB", "adatrack_gps_db"), "PostgreSQL database")
	flag.DurationVar(&opt.timeout, "timeout", 15*time.Second, "per-step timeout")
	flag.DurationVar(&opt.wait, "wait", 30*time.Second, "how long to wait for the alert chain")
	flag.BoolVar(&opt.dryRunCfg, "no-fuel-config", false, "skip creating the fuel config (assume it exists)")
	flag.Parse()
	return opt
}

// wsEndpoint builds the documented WebSocket endpoint (PRD §8.3).
func (o options) wsEndpoint(token string) string {
	parsed, err := url.Parse(o.wsBase)
	if err != nil {
		return o.wsBase
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

// client is a thin HTTP helper.
type client struct{ hc *http.Client }

func newClient(timeout time.Duration) *client { return &client{hc: &http.Client{Timeout: timeout}} }

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
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return resp.StatusCode, data, err
}

// login authenticates against service-websocket (same JWT as api-vehicle).
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

// wsEnvelope mirrors the server→client frame (PRD §8.3).
type wsEnvelope struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data,omitempty"`
	ErrorCode string          `json:"error_code,omitempty"`
	Message   string          `json:"message,omitempty"`
	Timestamp string          `json:"timestamp,omitempty"`
}

// dialWS opens an authenticated WebSocket.
func dialWS(opt options, token string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: opt.timeout}
	conn, _, err := dialer.Dial(opt.wsEndpoint(token), nil)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", opt.wsEndpoint(token), err)
	}
	return conn, nil
}

// wsSubscribe subscribes to vehicle ids and waits for the ACK.
func wsSubscribe(conn *websocket.Conn, vehicleIDs []int64, timeout time.Duration) error {
	payload, _ := json.Marshal(map[string]any{"action": "subscribe", "vehicle_ids": vehicleIDs})
	if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		return err
	}
	_, _, err := wsReadEvent(conn, "SUBSCRIBED", timeout)
	return err
}

// wsReadEvent waits for the next frame with the given event name.
func wsReadEvent(conn *websocket.Conn, want string, timeout time.Duration) (wsEnvelope, time.Duration, error) {
	start := time.Now()
	deadline := start.Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return wsEnvelope{}, 0, err
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return wsEnvelope{}, time.Since(start), fmt.Errorf("read frame: %w", err)
		}
		var envelope wsEnvelope
		if uerr := json.Unmarshal(raw, &envelope); uerr != nil {
			return wsEnvelope{}, time.Since(start), uerr
		}
		if strings.EqualFold(envelope.Event, want) {
			return envelope, time.Since(start), nil
		}
		if time.Now().After(deadline) {
			return wsEnvelope{}, time.Since(start),
				fmt.Errorf("timed out waiting for %s (last event %q)", want, envelope.Event)
		}
	}
}
