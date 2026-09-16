package controllers

import (
	"context"
	"errors"
)

// errNATS is reported by the readiness probe when the NATS client is disconnected.
var errNATS = errors.New("nats: disconnected")

// Readiness exposes the store readiness for the /healthz probe.
func (w *Worker) Readiness(ctx context.Context) error {
	return w.store.Readiness(ctx)
}
