package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// checkNATSRaw asserts `telemetry.raw.<IMEI>` carries the tenant-enriched payload.
func checkNATSRaw(ctx context.Context, ch chan []byte, opt options) checkResult {
	r := checkResult{Name: "nats.raw"}
	select {
	case data := <-ch:
		m, err := decodeRaw(data)
		if err != nil {
			r.Err = err
			return r
		}
		if got := fmt.Sprint(m["company_code"]); got != opt.company {
			r.Err = fmt.Errorf("company_code=%s want %s", got, opt.company)
			r.Detail = trim(data, 120)
			return r
		}
		if vid, ok := m["vehicle_id"].(float64); !ok || vid <= 0 {
			r.Err = fmt.Errorf("vehicle_id not resolved: %v", m["vehicle_id"])
			return r
		}
		r.Detail = fmt.Sprintf("company=%v vehicle=%v lat=%v lon=%v speed=%v",
			m["company_code"], m["vehicle_id"], m["lat"], m["lon"], m["speed"])
		return r
	case <-ctx.Done():
		r.Err = fmt.Errorf("no raw telemetry on %s within timeout", subject("raw", opt.imei))
		return r
	}
}

// checkNATSLive asserts worker-live published the live update.
func checkNATSLive(ctx context.Context, ch chan []byte, opt options) checkResult {
	r := checkResult{Name: "nats.live"}
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case data := <-ch:
		m, err := decodeRaw(data)
		if err != nil {
			r.Err = err
			return r
		}
		if got := fmt.Sprint(m["status"]); got == "" {
			r.Err = fmt.Errorf("live state without status: %s", trim(data, 120))
			return r
		}
		r.Detail = fmt.Sprintf("status=%v lat=%v lon=%v speed=%v",
			m["status"], m["lat"], m["lon"], m["speed"])
		return r
	case <-waitCtx.Done():
		r.Err = fmt.Errorf("no live update on %s within timeout", subject("live", opt.imei))
		return r
	}
}

// checkPostgres asserts the row landed in the tenant th_telemetry_logs.
func checkPostgres(ctx context.Context, db *sql.DB, opt options, lat, lon float64, since time.Time) checkResult {
	r := checkResult{Name: "postgres.row"}
	gotLat, gotLon, gotSpeed, err := waitForTelemetryRow(ctx, db, opt.imei, since, opt.timeout)
	if err != nil {
		r.Err = err
		return r
	}
	if diff := abs(gotLat - lat); diff > 0.0005 {
		r.Err = fmt.Errorf("latitude %.6f want %.6f", gotLat, lat)
		return r
	}
	if diff := abs(gotLon - lon); diff > 0.0005 {
		r.Err = fmt.Errorf("longitude %.6f want %.6f", gotLon, lon)
		return r
	}
	r.Detail = fmt.Sprintf("th_telemetry_logs imei=%s lat=%.6f lon=%.6f speed=%.1f",
		opt.imei, gotLat, gotLon, gotSpeed)
	return r
}

// checkRedis asserts the live-state key exists with the expected values.
func checkRedis(ctx context.Context, rdb *redis.Client, opt options, lat, lon, speed float64, acc bool) checkResult {
	r := checkResult{Name: "redis.live_state"}
	key := liveStateKey(opt.keyPrefix, opt.company, opt.imei)
	st, err := waitForLiveState(ctx, rdb, key, opt.timeout)
	if err != nil {
		r.Err = err
		return r
	}
	if diff := abs(st.Lat - lat); diff > 0.0005 {
		r.Err = fmt.Errorf("latitude %.6f want %.6f", st.Lat, lat)
		return r
	}
	if diff := abs(st.Lon - lon); diff > 0.0005 {
		r.Err = fmt.Errorf("longitude %.6f want %.6f", st.Lon, lon)
		return r
	}
	if diff := abs(st.Speed - speed); diff > 0.05 {
		r.Err = fmt.Errorf("speed %.3f want %.3f (knots quantization)", st.Speed, speed)
		return r
	}
	if st.ACC == nil || *st.ACC != acc {
		r.Err = fmt.Errorf("acc=%v want %v (ACC must round-trip into the live state)", st.ACC, acc)
		return r
	}
	r.Detail = fmt.Sprintf("%s status=%s speed=%.1f acc=%v last_seen=%d",
		key, st.Status, st.Speed, *st.ACC, st.LastSeen)
	return r
}

// abs returns the absolute value of a float64.
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
