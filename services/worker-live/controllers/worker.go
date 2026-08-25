package controllers

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"ajb_gps/internal"
	"ajb_gps/worker-live/models"

	"github.com/nats-io/nats.go"
	"github.com/prometheus/client_golang/prometheus"
)

// Package-level state for the live state writer. Configure() wires the
// external clients; Start() begins consuming telemetry.raw.>.
var (
	mu      sync.Mutex
	buffer  map[string]string // key -> JSON live state
	flushCh chan struct{}
	redCli  *internal.RedisClient
	natsCli *internal.NATSClient
	ctx     context.Context
	cancel  context.CancelFunc
	flushWg sync.WaitGroup
)

// --- Metrics (PRD §8.1 worker-live) ---
var (
	vehicleStateUpdates = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "vehicle_state_updates_total",
		Help: "Total live-state updates buffered",
	})
	redisBatchSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_batch_size",
		Help: "Records per Redis batch flush",
	})
)

// RegisterMetrics registers worker-live specific collectors.
func RegisterMetrics(reg *prometheus.Registry) {
	reg.MustRegister(vehicleStateUpdates, redisBatchSize)
}

// Configure binds the Redis + NATS clients (called once by main).
func Configure(red *internal.RedisClient, nats *internal.NATSClient) {
	redCli = red
	natsCli = nats
	ctx, cancel = context.WithCancel(context.Background())
	buffer = make(map[string]string)
	flushCh = make(chan struct{}, 1)
}

// redisStateKey builds the live-state key per Key Decision 7:
//
//	adatrack_gps:{company_code}:vehicle:state:<IMEI>
//
// company_code dinormalisasi lowercase; REDIS_KEY_PREFIX bisa di-override env
// (default "adatrack_gps:" sesuai PRD §7 format).
func redisStateKey(companyCode, imei string) string {
	prefix := os.Getenv("REDIS_KEY_PREFIX")
	if prefix == "" {
		prefix = "adatrack_gps:"
	}
	code := strings.ToLower(strings.TrimSpace(companyCode))
	if code == "" {
		code = "default"
	}
	return prefix + code + ":vehicle:state:" + imei
}

// Start subscribes to telemetry.raw.> (queue group "live") and begins the
// periodic flush loop. Returns the NATS subscription for graceful shutdown.
func Start() (*nats.Subscription, error) {
	go flusher()
	return natsCli.Subscribe(natsCli.Subject("raw", ">"), "live", handleMsg)
}

// Stop drains the pending buffer and settles in-flight Redis writes.
func Stop() {
	if cancel != nil {
		cancel()
	}
	poke()
	done := make(chan struct{})
	go func() {
		flushWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("live buffer drained")
	case <-time.After(5 * time.Second):
		slog.Warn("timed out draining live buffer")
	}
}

// handleMsg buffers an incoming telemetry message for batch flush.
func handleMsg(msg *nats.Msg) error {
	var t models.TelemetryMessage
	if err := json.Unmarshal(msg.Data, &t); err != nil {
		slog.Error("failed to unmarshal telemetry", "error", err, "subject", msg.Subject)
		return nil
	}
	if t.Timestamp <= 0 {
		t.Timestamp = time.Now().Unix()
	}
	st := models.LiveState{
		IMEI:        t.IMEI,
		CompanyCode: t.CompanyCode,
		Lat:         t.Lat,
		Lon:         t.Lon,
		Speed:       t.Speed,
		Heading:     t.Heading,
		LastSeen:    time.Now().Unix(),
		Status:      CalculateStatus(t.Speed, t.Timestamp),
	}
	data, err := json.Marshal(st)
	if err != nil {
		slog.Error("failed to marshal live state", "error", err, "imei", t.IMEI)
		return nil
	}

	key := redisStateKey(t.CompanyCode, t.IMEI)
	mu.Lock()
	buffer[key] = string(data)
	full := len(buffer) >= models.MaxBuffer
	mu.Unlock()
	vehicleStateUpdates.Inc()

	if full {
		poke()
	}
	return nil
}

// poke signals the flusher to run immediately (non-blocking).
func poke() {
	select {
	case flushCh <- struct{}{}:
	default:
	}
}

// flusher batches Redis writes: MSET every FlushInterval or when poked.
func flusher() {
	tick := time.NewTicker(models.FlushInterval)
	defer tick.Stop()
	for {
		select {
		case <-flushCh:
			flushBuffer()
		case <-tick.C:
			mu.Lock()
			nonEmpty := len(buffer) > 0
			mu.Unlock()
			if nonEmpty {
				flushBuffer()
			}
		case <-ctx.Done():
			return
		}
	}
}

// flushBuffer snapshots the buffer and MSETs it to Redis with a TTL.
func flushBuffer() {
	mu.Lock()
	if len(buffer) == 0 {
		mu.Unlock()
		return
	}
	snapshot := make(map[string]interface{}, len(buffer))
	for k, v := range buffer {
		snapshot[k] = v
	}
	buffer = make(map[string]string)
	mu.Unlock()

	flushWg.Add(1)
	go func() {
		defer flushWg.Done()
		redisBatchSize.Set(float64(len(snapshot)))
		if err := redCli.MSet(ctx, snapshot, models.StateTTL); err != nil {
			slog.Error("failed to flush live states to redis", "error", err, "keys", len(snapshot))
			return
		}
		slog.Info("flushed live states", "keys", len(snapshot))
	}()
}

// CalculateStatus returns ONLINE/IDLE/OFFLINE per FR-2.2.
func CalculateStatus(speed float64, lastEvent int64) string {
	if time.Now().Unix()-lastEvent > int64(models.OfflineAfter.Seconds()) {
		return "OFFLINE"
	}
	if speed > 0 {
		return "ONLINE"
	}
	return "IDLE"
}
