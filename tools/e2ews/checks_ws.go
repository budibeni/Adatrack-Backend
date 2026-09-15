package main

import (
	"context"
	"fmt"
	"time"
)

// checkWSPushUnderOneSecond covers the acceptance criterion
// "WS push end-to-end < 1 s dari publish worker-live" through the REAL path:
// device frame → ingestion-tcp → NATS → worker-live → service-websocket → client.
func checkWSPushUnderOneSecond(ctx context.Context, opt options, admin *session, vehicleID int64) checkResult {
	const name = "ws.push_under_1s"

	conn, _, err := dialWS(opt, admin.AccessToken, opt.timeout)
	if err != nil {
		return fail(name, "dial failed", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := wsSubscribe(conn, []int64{vehicleID}, opt.timeout); err != nil {
		return fail(name, "subscribe failed", err)
	}

	// Measure from just before the device frame is written to just after the
	// VEHICLE_UPDATE reaches the client.
	lat := -6.3000 + float64(time.Now().Unix()%50)/100000
	started := time.Now()
	if err := drive(opt.tcpAddr, opt.imei, lat, 106.9000, 55.5, true, opt.timeout); err != nil {
		return fail(name, "device frame failed", err)
	}
	envelope, waited, err := wsReadEvent(conn, "VEHICLE_UPDATE", opt.timeout)
	if err != nil {
		return fail(name, "no VEHICLE_UPDATE received", err)
	}
	elapsed := time.Since(started)
	if elapsed >= time.Second {
		return fail(name, fmt.Sprintf("push latency %d ms (>= 1000 ms, read waited %d ms)",
			elapsed.Milliseconds(), waited.Milliseconds()), nil)
	}

	update, derr := decodeUpdate(envelope)
	if derr != nil {
		return fail(name, "payload decode failed", derr)
	}
	if update.VehicleID != vehicleID {
		return fail(name, fmt.Sprintf("vehicle_id=%d want %d", update.VehicleID, vehicleID), nil)
	}
	if update.IMEI != opt.imei {
		return fail(name, fmt.Sprintf("imei=%s want %s", update.IMEI, opt.imei), nil)
	}
	if update.Speed <= 0 {
		return fail(name, fmt.Sprintf("speed=%.1f (expected the driven value)", update.Speed), nil)
	}
	if _, perr := time.Parse(time.RFC3339, update.Timestamp); perr != nil {
		return fail(name, fmt.Sprintf("timestamp %q is not RFC3339", update.Timestamp), nil)
	}
	if update.PlateNumber == "" {
		return fail(name, "plate_number missing from the FR-5.2 payload", nil)
	}
	detail := fmt.Sprintf("latency=%d ms event=%s speed=%.1f plate=%q",
		elapsed.Milliseconds(), envelope.Event, update.Speed, update.PlateNumber)
	return pass(name, detail)
}

// checkWSReconnectResubscribe covers "reconnect + resubscribe aman" (FR-5.3).
func checkWSReconnectResubscribe(ctx context.Context, opt options, admin *session, vehicleID int64) checkResult {
	const name = "ws.reconnect_resubscribe"

	// First connection: subscribe then drop it (simulating a network blip).
	first, _, err := dialWS(opt, admin.AccessToken, opt.timeout)
	if err != nil {
		return fail(name, "first dial failed", err)
	}
	if _, err := wsSubscribe(first, []int64{vehicleID}, opt.timeout); err != nil {
		_ = first.Close()
		return fail(name, "first subscribe failed", err)
	}
	_ = first.Close()

	// Reconnect and resubscribe: the update must flow again.
	second, _, err := dialWS(opt, admin.AccessToken, opt.timeout)
	if err != nil {
		return fail(name, "reconnect failed", err)
	}
	defer func() { _ = second.Close() }()
	if _, err := wsSubscribe(second, []int64{vehicleID}, opt.timeout); err != nil {
		return fail(name, "resubscribe failed", err)
	}

	lat := -6.4000 + float64(time.Now().Unix()%50)/100000
	if err := drive(opt.tcpAddr, opt.imei, lat, 106.9500, 33.3, true, opt.timeout); err != nil {
		return fail(name, "device frame failed", err)
	}
	envelope, _, err := wsReadEvent(second, "VEHICLE_UPDATE", opt.timeout)
	if err != nil {
		return fail(name, "no update after reconnect+resubscribe", err)
	}
	update, derr := decodeUpdate(envelope)
	if derr != nil {
		return fail(name, "payload decode failed", derr)
	}
	return pass(name, fmt.Sprintf("resubscribed and received vehicle_id=%d speed=%.1f", update.VehicleID, update.Speed))
}

// checkWSUnauthorizedVehicle asserts the WS RBAC filter (PRD §3.1/§8.3):
// the driver may not subscribe to a vehicle it does not own.
func checkWSUnauthorizedVehicle(ctx context.Context, opt options, driver *session) checkResult {
	const name = "ws.unauthorized_vehicle"

	conn, _, err := dialWS(opt, driver.AccessToken, opt.timeout)
	if err != nil {
		return fail(name, "dial failed", err)
	}
	defer func() { _ = conn.Close() }()

	// The driver only owns one vehicle; probe every other id it can discover from
	// the admin API is impossible (row-level), so probe id 1..3 and require at
	// least one UNAUTHORIZED_VEHICLE rejection.
	if err := wsWrite(conn, map[string]any{"action": "subscribe", "vehicle_ids": []int64{1, 2, 3}}); err != nil {
		return fail(name, "subscribe write failed", err)
	}
	envelope, _, err := wsReadEvent(conn, "ERROR", opt.timeout)
	if err != nil {
		return fail(name, "no ERROR frame for an unauthorized subscription", err)
	}
	if envelope.ErrorCode != "UNAUTHORIZED_VEHICLE" {
		return fail(name, "error_code="+envelope.ErrorCode+" (want UNAUTHORIZED_VEHICLE)", nil)
	}
	return pass(name, "rejected with ERROR UNAUTHORIZED_VEHICLE")
}
