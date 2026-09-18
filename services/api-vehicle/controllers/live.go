package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"adatrack_gps/api-vehicle/models"
)

// LiveStateReader is the Redis surface the live-state overlay needs: the
// tenant-scoped key layout shared with worker-live plus one BATCHED read.
// *RedisKV implements it; unit tests inject a stub so no Redis is required.
type LiveStateReader interface {
	// LiveStateKey returns the live-state key of one IMEI (FR-2.1).
	LiveStateKey(companyCode, imei string) string
	// MGet returns one raw JSON value per key ("" when the key is missing).
	MGet(ctx context.Context, keys ...string) ([]string, error)
}

// applyLiveState overlays the freshest telemetry from Redis onto one DB row
// (phase B6: "REST enrich live-state: overlay fuel_level & acc dari Redis").
//
// Only telemetry MIRRORS are replaced. `Vehicle.Status` (fleet life-cycle:
// active/inactive/maintenance) is NEVER touched — the live connection state
// (ONLINE/IDLE/OFFLINE, FR-2.2) travels inside `live.status` instead, so the two
// vocabularies can never collide.
//
// Position mirrors are only overwritten when the live state carries a real
// position: a fuel-only partial write (worker-live `mergeFuelState`) keeps
// lat/lon = 0 for a vehicle that never reported a fix, and that must not erase
// the last known DB position.
func applyLiveState(v *models.Vehicle, ls *models.LiveState) {
	if v == nil || ls == nil {
		return
	}
	v.Live = ls

	if ls.LastSeen > 0 {
		seen := time.Unix(ls.LastSeen, 0).UTC().Format(time.RFC3339)
		v.LastSeenAt = &seen
	}
	if ls.Lat != 0 || ls.Lon != 0 {
		lat, lon := ls.Lat, ls.Lon
		v.CurrentLat = &lat
		v.CurrentLon = &lon
	}
	if ls.Fix {
		speed := ls.Speed
		v.CurrentSpeed = &speed
	}
}

// enrichLiveStates overlays the live state of every vehicle of one page.
//
// Live state is an ENRICHMENT, never a hard dependency: a Redis outage logs a
// warning and returns the DB rows untouched instead of turning a fleet read
// into a 503 (graceful degradation). One MGET serves the whole page, so the
// overlay costs exactly one extra Redis round trip regardless of page size.
func (s *Service) enrichLiveStates(ctx context.Context, companyCode string, items []models.Vehicle) []models.Vehicle {
	if s.live == nil || len(items) == 0 {
		return items
	}
	keys := make([]string, 0, len(items))
	idx := make([]int, 0, len(items))
	for i := range items {
		if strings.TrimSpace(items[i].IMEI) == "" {
			continue
		}
		keys = append(keys, s.live.LiveStateKey(companyCode, items[i].IMEI))
		idx = append(idx, i)
	}
	if len(keys) == 0 {
		return items
	}

	values, err := s.live.MGet(ctx, keys...)
	if err != nil {
		liveStateErrors.Inc()
		slog.Warn("api-vehicle: live-state overlay skipped", "error", err, "keys", len(keys))
		return items
	}
	for n, raw := range values {
		if n >= len(idx) || strings.TrimSpace(raw) == "" {
			continue
		}
		var ls models.LiveState
		if uerr := json.Unmarshal([]byte(raw), &ls); uerr != nil {
			liveStateErrors.Inc()
			slog.Warn("api-vehicle: unreadable live state", "error", uerr,
				"key", keys[n])
			continue
		}
		applyLiveState(&items[idx[n]], &ls)
	}
	return items
}
