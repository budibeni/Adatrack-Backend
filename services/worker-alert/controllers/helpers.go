package controllers

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// durationFromEnv reads a minutes-based env var into a Duration. Accepts a
// plain integer (minutes) or a Go duration string ("90s", "2m").
func durationFromEnv(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Minute
	}
	return def
}

// contextWithTimeout is a small wrapper for 3–5s DB/Redis calls.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func jsonUnmarshal(b []byte, v interface{}) error { return json.Unmarshal(b, v) }

// haversineMeters computes the great-circle distance between two coordinates.
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371000.0
	toRad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadius * c
}

// withinRadius reports whether point (lat,lon) lies within radiusM of center.
func withinRadius(cLat, cLon, radiusM, lat, lon float64) bool {
	return radiusM > 0 && haversineMeters(cLat, cLon, lat, lon) <= radiusM
}

// redisKeyPrefix mirrors PRD §7 / worker-live: "adatrack_gps:" default.
func (wa *WorkerAlert) redisKeyPrefix() string {
	if p := strings.TrimSpace(os.Getenv("REDIS_KEY_PREFIX")); p != "" {
		return p
	}
	return "adatrack_gps:"
}

// redisStateKey builds the live-state key exactly as worker-live writes it:
// {prefix}{company}:vehicle:state:{imei}.
func (wa *WorkerAlert) redisStateKey(company, imei string) string {
	code := strings.ToLower(strings.TrimSpace(company))
	if code == "" {
		code = "default"
	}
	return wa.redisKeyPrefix() + code + ":vehicle:state:" + imei
}

// geofenceStateKey builds the geofence entry/exit state hash key.
func (wa *WorkerAlert) geofenceStateKey(company, imei string) string {
	code := strings.ToLower(strings.TrimSpace(company))
	if code == "" {
		code = "default"
	}
	return wa.redisKeyPrefix() + code + ":geofence_state:" + imei
}

// fuelStateKey builds the B5a fuel-sensor state key holding the last reading:
// {prefix}{company}:fuel_state:{imei}.
func (wa *WorkerAlert) fuelStateKey(company, imei string) string {
	code := strings.ToLower(strings.TrimSpace(company))
	if code == "" {
		code = "default"
	}
	return wa.redisKeyPrefix() + code + ":fuel_state:" + imei
}

// sosEscalationKey builds the per-alert escalation counter key.
func (wa *WorkerAlert) sosEscalationKey(company string, alertID uint64) string {
	code := strings.ToLower(strings.TrimSpace(company))
	if code == "" {
		code = "default"
	}
	return wa.redisKeyPrefix() + code + ":sos_escalation:" + uintToString(alertID)
}

func uintToString(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
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
