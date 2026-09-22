package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// alertFrame is one `alert.fuel.<company>` payload (worker-alert → NATS).
type alertFrame struct {
	ID          int64  `json:"id"`
	Type        string `json:"type"`
	Severity    string `json:"severity"`
	VehicleID   int64  `json:"vehicle_id"`
	CompanyCode string `json:"company_code"`
}

// runChecks executes the whole B5a flow.
func runChecks(ctx context.Context, opt options) []checkResult {
	var results []checkResult
	add := func(r checkResult) { results = append(results, r) }

	cli := newClient(opt.timeout)
	token, err := cli.login(opt.wsBase, opt.adminMail, opt.adminPass)
	if err != nil {
		add(fail("auth.login", "tenant admin session is required", err))
		return results
	}
	add(pass("auth.login", "tenant admin session established"))

	// --- fixtures: device mapping, fuel config, dedup slot -------------------
	master, err := openPG(opt.pg, envOr("MASTER_DB_NAME", "adatrack_gps_master"))
	if err != nil {
		add(fail("db.master", "PostgreSQL master unreachable", err))
		return results
	}
	defer func() { _ = master.Close() }()

	vehicleID, company, err := vehicleIDForIMEI(ctx, master, opt.imei)
	if err != nil {
		add(fail("device.registered", "IMEI must be registered", err))
		return results
	}
	if opt.company != "" && upper(opt.company) != company {
		company = upper(opt.company)
	}
	add(pass("device.registered", fmt.Sprintf("imei=%s vehicle=%d company=%s", opt.imei, vehicleID, company)))

	tenant, err := openPG(opt.pg, envOr("COMPANY_DB_PREFIX", "adatrack_gps_")+lower(company))
	if err != nil {
		add(fail("db.tenant", "tenant schema unreachable", err))
		return results
	}
	defer func() { _ = tenant.Close() }()

	add(checkFuelConfig(ctx, cli, opt, tenant, token, vehicleID))
	if n, rerr := resolveOpenFuelAlerts(ctx, tenant); rerr != nil {
		add(fail("alerts.dedup_reset", "could not resolve previous fuel alerts", rerr))
	} else {
		add(pass("alerts.dedup_reset", fmt.Sprintf("resolved %d open fuel alert(s) so the dedup slot is free", n)))
	}

	// --- NATS + WS listeners ------------------------------------------------
	nc, err := nats.Connect(envOr("E2E_NATS_URL", "nats://127.0.0.1:4222"), nats.Name("adatrack-e2e-fuel"))
	if err != nil {
		add(fail("nats.connect", "NATS is required for the alert assertion", err))
		return results
	}
	defer nc.Close()
	alertCh := make(chan alertFrame, 8)
	subject := "alert.fuel." + company
	fuelSub, serr := nc.Subscribe(subject, func(m *nats.Msg) {
		var frame alertFrame
		if json.Unmarshal(m.Data, &frame) == nil {
			select {
			case alertCh <- frame:
			default:
			}
		}
	})
	if serr != nil {
		add(fail("nats.subscribe", "could not subscribe to "+subject, serr))
		return results
	}
	defer func() { _ = fuelSub.Unsubscribe() }()
	_ = nc.FlushTimeout(3 * time.Second)

	conn, werr := dialWS(opt, token)
	if werr != nil {
		add(fail("ws.connect", "WebSocket session is required for the notify fan-out", werr))
		return results
	}
	defer func() { _ = conn.Close() }()
	if err := wsSubscribe(conn, []int64{vehicleID}, opt.timeout); err != nil {
		add(fail("ws.subscribe", "subscribe failed", err))
		return results
	}
	add(pass("ws.subscribe", fmt.Sprintf("subscribed to vehicle %d", vehicleID)))

	// --- drive the device ---------------------------------------------------
	started := time.Now().UTC().Add(-2 * time.Second)
	if err := driveFuel(opt); err != nil {
		add(fail("device.fuel_frames", "could not drive the device frames", err))
		return results
	}
	add(pass("device.fuel_frames", fmt.Sprintf("0x94/0x0D frames sent (%.0f cm → %.0f cm)", opt.high, opt.low)))

	// --- assertions ---------------------------------------------------------
	add(checkFuelLogs(ctx, tenant, opt, started))
	add(checkLiveState(ctx, opt, company))
	add(checkAlertPublished(alertCh, opt, vehicleID))
	add(checkWSCallback(conn, opt, vehicleID))
	add(checkFuelHistory(ctx, cli, opt, token, vehicleID, started))

	return results
}

// checkFuelConfig ensures a fuel config covers the vehicle (FR-7.6).
func checkFuelConfig(ctx context.Context, cli *client, opt options, tenant *sql.DB, token string, vehicleID int64) checkResult {
	exists, err := fuelConfigExists(ctx, tenant, vehicleID)
	if err != nil {
		return fail("fuel.config", "config lookup failed", err)
	}
	if exists {
		return pass("fuel.config", "an enabled fuel config already covers the vehicle")
	}
	if opt.dryRunCfg {
		return fail("fuel.config", "no enabled fuel config and --no-fuel-config was set", nil)
	}
	body, _ := json.Marshal(map[string]any{
		"drop_threshold_percent": 10, "refuel_threshold_percent": 10,
		"window_seconds": 300, "severity": "critical", "enabled": true,
	})
	status, raw, rerr := cli.request(http.MethodPost, opt.apiBase+"/api/v1/fuel-configs", body,
		withJSON(bearer(token)))
	if rerr != nil {
		return fail("fuel.config", "create request failed", rerr)
	}
	if status != http.StatusCreated && status != http.StatusConflict {
		return fail("fuel.config", fmt.Sprintf("create status %d (%s)", status, trim(raw, 160)), nil)
	}
	return pass("fuel.config", fmt.Sprintf("tenant-wide fuel config ensured (status %d)", status))
}

// checkFuelLogs asserts FR-7.4 (td_fuel_logs rows + calibrated level).
func checkFuelLogs(ctx context.Context, tenant *sql.DB, opt options, since time.Time) checkResult {
	deadline := time.Now().Add(opt.wait)
	var (
		count   int
		level   *float64
		volume  *float64
		lastErr error
	)
	for time.Now().Before(deadline) {
		count, level, volume, lastErr = fuelRowsSince(ctx, tenant, opt.imei, since)
		if lastErr == nil && count >= 2 && level != nil {
			return pass("persist.td_fuel_logs", fmt.Sprintf("%d rows, latest level=%.1f%% volume=%.1f", count, *level, deref(volume)))
		}
		select {
		case <-ctx.Done():
			return fail("persist.td_fuel_logs", "context cancelled", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if lastErr != nil {
		return fail("persist.td_fuel_logs", "query failed", lastErr)
	}
	return fail("persist.td_fuel_logs", fmt.Sprintf("rows=%d level=%v after %s (is FUEL_TANK_HEIGHT_CM set?) — the fuel-only packet must persist to td_fuel_logs", count, level, opt.wait), nil)
}

// checkLiveState asserts FR-7.5 (worker-live merges fuel into the live state).
func checkLiveState(ctx context.Context, opt options, company string) checkResult {
	rdb := redis.NewClient(&redis.Options{
		Addr:     envOr("E2E_REDIS_ADDR", "127.0.0.1:6380"),
		Password: envOr("REDIS_PASSWORD", ""),
	})
	defer func() { _ = rdb.Close() }()
	key := liveStateKey(envOr("REDIS_KEY_PREFIX", "adatrack_gps:"), company, opt.imei)
	state, err := waitForLiveFuel(ctx, rdb, key, opt.wait)
	if err != nil {
		return fail("live.fuel_state", err.Error(), err)
	}
	return pass("live.fuel_state", fmt.Sprintf("%s fuel_level=%.1f%% volume=%.1f lat=%.5f lon=%.5f",
		key, *state.FuelLevel, deref(state.FuelVolume), state.Lat, state.Lon))
}

// checkAlertPublished asserts FR-7.6 (alert.fuel.<company> with fuel_drop).
func checkAlertPublished(ch <-chan alertFrame, opt options, vehicleID int64) checkResult {
	deadline := time.After(opt.wait)
	for {
		select {
		case frame := <-ch:
			if frame.Type == "fuel_drop" || frame.Type == "refuel" {
				return pass("alert.fuel_published", fmt.Sprintf("type=%s severity=%s vehicle=%d (want %d) alert_id=%d",
					frame.Type, frame.Severity, frame.VehicleID, vehicleID, frame.ID))
			}
		case <-deadline:
			return fail("alert.fuel_published", fmt.Sprintf("no alert.fuel.%s frame within %s", opt.company, opt.wait), nil)
		}
	}
}

// checkWSCallback asserts the notify.alert fan-out reached the dashboard client.
func checkWSCallback(conn *websocket.Conn, opt options, vehicleID int64) checkResult {
	want := "notify.alert." + strconv.FormatInt(vehicleID, 10)
	envelope, latency, err := wsReadEvent(conn, want, opt.wait)
	if err != nil {
		return fail("ws.notify_alert", "no notify frame received", err)
	}
	var payload struct {
		Type     string `json:"type"`
		Severity string `json:"severity"`
	}
	if derr := json.Unmarshal(envelope.Data, &payload); derr != nil {
		return fail("ws.notify_alert", "could not decode the notification", derr)
	}
	if payload.Type != "fuel_drop" && payload.Type != "refuel" {
		return fail("ws.notify_alert", "unexpected alert type "+payload.Type, nil)
	}
	return pass("ws.notify_alert", fmt.Sprintf("%s type=%s severity=%s in %dms", want, payload.Type, payload.Severity, latency.Milliseconds()))
}

// checkFuelHistory asserts the FR-7.7 REST history endpoint.
func checkFuelHistory(ctx context.Context, cli *client, opt options, token string, vehicleID int64, since time.Time) checkResult {
	path := fmt.Sprintf("/api/v1/vehicles/%d/fuel/history?from=%s&to=%s", vehicleID,
		since.Add(-time.Hour).Format(time.RFC3339), time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
	status, raw, err := cli.request(http.MethodGet, opt.apiBase+path, nil, bearer(token))
	if err != nil || status != http.StatusOK {
		return fail("rest.fuel_history", fmt.Sprintf("status %d (%s)", status, trim(raw, 160)), err)
	}
	var env struct {
		Data []struct {
			FuelLevel *float64 `json:"fuel_level"`
			Timestamp string   `json:"timestamp"`
		} `json:"data"`
		Pagination *struct {
			Total int64 `json:"total"`
		} `json:"pagination"`
	}
	if derr := json.Unmarshal(raw, &env); derr != nil {
		return fail("rest.fuel_history", "could not decode the history", derr)
	}
	if len(env.Data) == 0 {
		return fail("rest.fuel_history", "history returned no rows", nil)
	}
	return pass("rest.fuel_history", fmt.Sprintf("%d row(s) returned for vehicle %d", len(env.Data), vehicleID))
}

// deref returns the value of an optional float (0 when absent).
func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

// withJSON adds the JSON content type.
func withJSON(base map[string]string) map[string]string {
	base["Content-Type"] = "application/json"
	return base
}

// upper/lower normalise tenant codes.
func upper(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }
func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
