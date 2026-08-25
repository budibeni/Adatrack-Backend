package controllers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Start subscribes to telemetry.raw.> (queue group "alert") and launches the
// background monitors (routes refresh, offline scan, SOS escalation).
func (wa *WorkerAlert) Start() error {
	rawSubject := wa.cfg.Subject("raw", ">")
	sub, err := wa.nac.Subscribe(rawSubject, "alert", wa.handleTelemetry)
	if err != nil {
		return err
	}
	slog.Info("worker-alert subscribed", "subject", rawSubject, "queue", "alert")

	wa.wg.Add(4)
	go func() { defer wa.wg.Done(); <-wa.ctx.Done(); wa.nac.Unsubscribe(sub) }()
	go wa.loop(wa.checkOfflineMonitors)
	go wa.loop(wa.escalationMonitor)
	go wa.loop(wa.refreshRoutes)

	wa.reloadRoutes() // seed state segera saat boot (refresh berkala via refreshRoutes)
	return nil
}

// loop runs fn until the worker context is cancelled.
func (wa *WorkerAlert) loop(fn func()) {
	defer wa.wg.Done()
	fn()
}

// SetContext wires the worker lifetime context (called by main).
func (wa *WorkerAlert) SetContext(ctx context.Context, cancel context.CancelFunc) {
	wa.ctx = ctx
	wa.cancel = cancel
}

// ServeMetrics exposes /healthz + /metrics on cfg.Server.Addr.
// /healthz = readiness: master+company pools, Redis, NATS.
func (wa *WorkerAlert) ServeMetrics() {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", wa.healthHandler)
	mux.Handle("/metrics", promhttp.HandlerFor(wa.internalRegistry(), promhttp.HandlerOpts{}))

	srv := &http.Server{Addr: wa.cfg.Server.Addr, Handler: mux}
	slog.Info("metrics server listening", "addr", wa.cfg.Server.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("metrics server failed", "error", err)
	}
}

// healthHandler implements the PRD §8.2 readiness probe.
func (wa *WorkerAlert) healthHandler(w http.ResponseWriter, _ *http.Request) {
	ctx, cancel := contextWithTimeout(5 * time.Second)
	defer cancel()

	ok := true
	var checks []string
	if err := wa.tm.Health(ctx); err != nil {
		ok = false
		checks = append(checks, "mysql:"+err.Error())
	} else {
		checks = append(checks, "mysql:ok")
	}
	if wa.redis.Ping(ctx) == nil {
		checks = append(checks, "redis:ok")
	} else {
		ok = false
		checks = append(checks, "redis:down")
	}
	if wa.nac.IsConnected() {
		checks = append(checks, "nats:ok")
	} else {
		ok = false
		checks = append(checks, "nats:down")
	}

	body := "ok " + joinChecks(checks)
	code := http.StatusOK
	if !ok {
		body = "degraded " + joinChecks(checks)
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

func joinChecks(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

// Stop cancels the context and waits for goroutines to drain.
func (wa *WorkerAlert) Stop() {
	if wa.cancel == nil {
		return
	}
	wa.cancel()
	wa.wg.Wait()
}
