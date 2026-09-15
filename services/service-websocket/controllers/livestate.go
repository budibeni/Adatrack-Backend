package controllers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"

	"ajb_gps/internal"
	"ajb_gps/service-websocket/models"
)

// liveStateErrors counts Redis read failures while enriching REST responses /
// fanning out live updates (PRD §10.1). Read failures degrade gracefully (the
// fleet list is still returned) but are never silent.
var liveStateErrors = prometheus.NewCounter(prometheus.CounterOpts{
	Name: "live_state_read_errors_total",
	Help: "Redis live-state read failures (graceful degradation: response served without live data)",
})

// LiveStateStore reads the worker-live state from Redis (FR-2.1). The interface
// keeps the REST handlers testable without Redis.
type LiveStateStore interface {
	// LiveStates returns the live state per IMEI; missing keys are simply absent.
	LiveStates(ctx context.Context, companyCode string, imeis []string) map[string]models.LiveState
}

// RedisLiveState implements LiveStateStore on top of the shared Redis client.
type RedisLiveState struct {
	red *internal.RedisClient
}

// NewRedisLiveState wraps the shared Redis client.
func NewRedisLiveState(red *internal.RedisClient) *RedisLiveState {
	return &RedisLiveState{red: red}
}

// LiveStates reads every key in ONE MGET round trip and unmarshals the values
// (a malformed/absent value is skipped, never fatal).
func (r *RedisLiveState) LiveStates(ctx context.Context, companyCode string, imeis []string) map[string]models.LiveState {
	out := make(map[string]models.LiveState, len(imeis))
	if len(imeis) == 0 {
		return out
	}
	keys := make([]string, 0, len(imeis))
	for _, imei := range imeis {
		keys = append(keys, r.red.LiveStateKey(companyCode, imei))
	}
	values, err := r.red.Cmdable().MGet(ctx, keys...).Result()
	if err != nil && err != redis.Nil {
		liveStateErrors.Inc()
		slog.Warn("service-websocket: live-state read failed; serving response without live data",
			"company", companyCode, "error", err)
		return out
	}
	for i, raw := range values {
		if i >= len(imeis) || raw == nil {
			continue
		}
		str, ok := raw.(string)
		if !ok || str == "" {
			continue
		}
		var st models.LiveState
		if uerr := json.Unmarshal([]byte(str), &st); uerr != nil {
			slog.Debug("service-websocket: unreadable live state", "imei", imeis[i], "error", uerr)
			continue
		}
		out[imeis[i]] = st
	}
	return out
}
