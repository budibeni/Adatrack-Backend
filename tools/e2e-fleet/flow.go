package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// runChecks executes the whole B7.1/B7.2 flow plus the B7.3/B7.4 REST surface.
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
	if opt.company != "" {
		company = upper(opt.company)
	}
	add(pass("device.registered", fmt.Sprintf("imei=%s vehicle=%d company=%s", opt.imei, vehicleID, company)))

	tenant, err := openPG(opt.pg, envOr("COMPANY_DB_PREFIX", "adatrack_gps_")+lower(company))
	if err != nil {
		add(fail("db.tenant", "tenant schema unreachable", err))
		return results
	}
	defer func() { _ = tenant.Close() }()

	before, err := readMetering(ctx, tenant, vehicleID)
	if err != nil {
		add(fail("metering.baseline", "could not read the vehicle counters", err))
		return results
	}
	add(pass("metering.baseline", fmt.Sprintf("odometer=%.3f km engine_hours=%.3f",
		before.odometerKM, before.engineHours)))

	// --- drive ---------------------------------------------------------------
	plan := plannedRoute()
	base := time.Now().UTC().Add(-2 * time.Minute)
	runStart := base.Add(-time.Minute)
	if err := driveRoute(opt, base, plan); err != nil {
		add(fail("device.drive", "could not replay the planned route", err))
		return results
	}
	add(pass("device.drive", fmt.Sprintf("%d position frames + fuel + GPS jump (expected %.3f km)",
		len(plan.points)+len(plan.stops)+2, plan.expectedDistanceKM())))

	// --- FR-2.5: odometer + engine hours ------------------------------------
	add(checkMetering(ctx, tenant, vehicleID, before, plan, opt))

	// --- FR-2.6: trip + stop ------------------------------------------------
	trips, tripErr := waitForTrips(ctx, tenant, vehicleID, runStart, opt.wait)
	if tripErr != nil {
		add(fail("trip.persisted", tripErr.Error(), tripErr))
	} else {
		add(checkTrip(trips, plan))
		if len(trips) > 0 {
			stops, serr := stopsOfTrip(ctx, tenant, trips[0].id)
			if serr != nil {
				add(fail("stop.persisted", "could not read td_vehicle_stops", serr))
			} else {
				add(checkStop(stops, plan))
			}
		}
	}

	// --- B7.4/B7.3: playback + reverse geocoding ----------------------------
	add(checkPlayback(ctx, cli, opt, token, vehicleID, base))
	add(checkReverseGeocode(ctx, cli, opt, token))

	// --- cleanup ------------------------------------------------------------
	if opt.cleanup {
		// A dedicated context: the assertions above may have consumed the harness
		// deadline, and a cleanup must never be skipped because of that.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ids := make([]int64, 0, len(trips))
		for _, t := range trips {
			ids = append(ids, t.id)
		}
		if derr := deleteTrips(cleanupCtx, tenant, ids); derr != nil {
			add(fail("cleanup.trips", "could not delete the created trips", derr))
		} else if rerr := restoreMetering(cleanupCtx, tenant, vehicleID, before); rerr != nil {
			add(fail("cleanup.metering", "could not restore the vehicle counters", rerr))
		} else {
			add(pass("cleanup", fmt.Sprintf("removed %d trip(s) and restored the counters", len(ids))))
		}
	}
	return results
}

// checkMetering asserts FR-2.5: the odometer delta equals the Haversine length of
// the driven legs (GPS jump discarded) and engine hours were credited with ACC ON.
func checkMetering(ctx context.Context, tenant *sql.DB, vehicleID int64, before metering,
	plan routePlan, opt options) checkResult {
	expected := plan.expectedDistanceKM()
	after, err := waitForMetering(ctx, tenant, vehicleID, before, opt.wait)
	if err != nil {
		return fail("odometer.accumulated", err.Error(), err)
	}
	odoDelta := after.odometerKM - before.odometerKM
	engineDelta := after.engineHours - before.engineHours

	// The delta must match the route within 20% (the pipeline rounds to 3
	// decimals and the frames are replayed in order).
	if odoDelta < expected*0.8 || odoDelta > expected*1.2 {
		return fail("odometer.accumulated",
			fmt.Sprintf("delta=%.3f km, want ≈%.3f km (±20%%, GPS jump must be discarded)",
				odoDelta, expected), nil)
	}
	if engineDelta <= 0 {
		return fail("engine_hours.accumulated",
			fmt.Sprintf("delta=%.4f h, want > 0 while ACC was ON", engineDelta), nil)
	}
	return pass("odometer.accumulated",
		fmt.Sprintf("delta=%.3f km (want ≈%.3f) engine_hours=+%.4f h",
			odoDelta, expected, engineDelta))
}

// waitForMetering polls the vehicle counters until the odometer moved past the
// baseline (the worker-live fleet flusher runs every FLEET_FLUSH_SECONDS).
func waitForMetering(ctx context.Context, tenant *sql.DB, vehicleID int64, before metering, timeout time.Duration) (metering, error) {
	deadline := time.Now().Add(timeout)
	var last metering
	for time.Now().Before(deadline) {
		m, err := readMetering(ctx, tenant, vehicleID)
		if err != nil {
			return metering{}, err
		}
		last = m
		if m.odometerKM > before.odometerKM {
			return m, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return last, fmt.Errorf("odometer did not move within %s (worker-live flush pending?)", timeout)
}

// waitForTrips polls `th_vehicle_trips` until the ride is stored (the flusher
// batches, so a slow first flush is expected and not a failure).
func waitForTrips(ctx context.Context, tenant *sql.DB, vehicleID int64, since time.Time, timeout time.Duration) ([]tripRow, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		trips, err := tripsSince(ctx, tenant, vehicleID, since)
		if err != nil {
			return nil, err
		}
		if len(trips) > 0 {
			return trips, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("no th_vehicle_trips row within %s", timeout)
}

// checkTrip asserts the FR-2.6 trip header: distance/max speed/duration and the
// single confirmed stop of the planned drive.
func checkTrip(trips []tripRow, plan routePlan) checkResult {
	trip := trips[0]
	if trip.distanceKM <= 0 {
		return fail("trip.persisted", fmt.Sprintf("distance_km = %.3f, want > 0", trip.distanceKM), nil)
	}
	if trip.durationSeconds <= 0 {
		return fail("trip.persisted", fmt.Sprintf("duration_seconds = %d, want > 0", trip.durationSeconds), nil)
	}
	if trip.maxSpeedKMH < 40 || trip.maxSpeedKMH > 50 {
		return fail("trip.persisted", fmt.Sprintf("max_speed_kmh = %.1f, want ≈45 (planned)", trip.maxSpeedKMH), nil)
	}
	if trip.stopCount != 1 {
		return fail("trip.persisted", fmt.Sprintf("stop_count = %d, want 1 (the planned standstill)", trip.stopCount), nil)
	}
	return pass("trip.persisted", fmt.Sprintf("id=%d distance=%.3f km duration=%d s max=%.1f km/h stops=%d",
		trip.id, trip.distanceKM, trip.durationSeconds, trip.maxSpeedKMH, trip.stopCount))
}

// checkStop asserts the FR-2.6 stop detail (td_vehicle_stops).
func checkStop(stops []stopRow, plan routePlan) checkResult {
	if len(stops) == 0 {
		return fail("stop.persisted", "td_vehicle_stops returned no row for the trip", nil)
	}
	want := plan.stopDurationSeconds()
	got := stops[0].durationSeconds
	if got < want-10 || got > want+10 {
		return fail("stop.persisted", fmt.Sprintf("duration = %d s, want ≈%d s (planned standstill)", got, want), nil)
	}
	return pass("stop.persisted", fmt.Sprintf("duration=%d s (want ≈%d) lat=%.5f lon=%.5f",
		got, want, stops[0].lat, stops[0].lon))
}

// checkPlayback asserts B7.4 (reduced playback) and B7.3 (address enrichment)
// through the authenticated REST endpoint.
func checkPlayback(ctx context.Context, cli *client, opt options, token string, vehicleID int64, base time.Time) checkResult {
	path := fmt.Sprintf("/api/v1/vehicles/%d/playback?from=%s&to=%s&tolerance_m=5",
		vehicleID, base.Add(-time.Hour).Format(time.RFC3339),
		time.Now().UTC().Add(time.Hour).Format(time.RFC3339))

	deadline := time.Now().Add(opt.wait)
	var lastErr error
	for time.Now().Before(deadline) {
		status, raw, err := cli.request(http.MethodGet, opt.wsBase+path, nil, bearer(token))
		switch {
		case err != nil:
			lastErr = err
		case status != http.StatusOK:
			lastErr = fmt.Errorf("status %d (%s)", status, trim(raw, 160))
		default:
			var env struct {
				Data struct {
					TotalPoints      int     `json:"total_points"`
					ReturnedPoints   int     `json:"returned_points"`
					ReductionPercent float64 `json:"reduction_percent"`
					DistanceKM       float64 `json:"distance_km"`
					Truncated        bool    `json:"truncated"`
					Points           []struct {
						Address string `json:"address"`
						City    string `json:"city"`
					} `json:"points"`
				} `json:"data"`
			}
			if derr := json.Unmarshal(raw, &env); derr != nil {
				return fail("playback.reduced", "could not decode the playback response", derr)
			}
			data := env.Data
			if len(data.Points) == 0 {
				// worker-persistence may still be flushing the telemetry batch.
				lastErr = fmt.Errorf("playback returned no points yet")
				break
			}
			if data.ReturnedPoints == 0 || data.ReturnedPoints > data.TotalPoints {
				return fail("playback.reduced", fmt.Sprintf("returned=%d total=%d",
					data.ReturnedPoints, data.TotalPoints), nil)
			}
			if data.DistanceKM <= 0 {
				return fail("playback.reduced", fmt.Sprintf("distance_km = %.3f, want > 0", data.DistanceKM), nil)
			}
			if data.Truncated {
				return fail("playback.reduced", "playback reported truncation for a 8-point route", nil)
			}
			if data.Points[0].Address == "" || data.Points[0].City == "" {
				return fail("playback.address", "the first playback point carries no offline address", nil)
			}
			return pass("playback.reduced", fmt.Sprintf(
				"total=%d returned=%d (%.1f%% reduced) distance=%.3f km address=%q",
				data.TotalPoints, data.ReturnedPoints, data.ReductionPercent, data.DistanceKM,
				data.Points[0].Address))
		}
		select {
		case <-ctx.Done():
			return fail("playback.reduced", lastErr.Error(), lastErr)
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fail("playback.reduced", lastErr.Error(), lastErr)
}

// checkReverseGeocode asserts the standalone B7.3 endpoint.
func checkReverseGeocode(_ context.Context, cli *client, opt options, token string) checkResult {
	status, raw, err := cli.request(http.MethodGet,
		opt.wsBase+"/api/v1/geocode/reverse?lat=-6.2010&lon=106.8000", nil, bearer(token))
	if err != nil || status != http.StatusOK {
		return fail("geocode.reverse", fmt.Sprintf("status %d (%s)", status, trim(raw, 160)), err)
	}
	var env struct {
		Data struct {
			Address struct {
				Address  string `json:"address"`
				Level    string `json:"level"`
				Resolved bool   `json:"resolved"`
			} `json:"address"`
		} `json:"data"`
	}
	if derr := json.Unmarshal(raw, &env); derr != nil {
		return fail("geocode.reverse", "could not decode the geocode response", derr)
	}
	if !env.Data.Address.Resolved || env.Data.Address.Address == "" {
		return fail("geocode.reverse", fmt.Sprintf("resolved=%v address=%q (reference data missing?)",
			env.Data.Address.Resolved, env.Data.Address.Address), nil)
	}
	return pass("geocode.reverse", fmt.Sprintf("%q level=%s",
		env.Data.Address.Address, env.Data.Address.Level))
}
