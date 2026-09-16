package consumer

import (

	"github.com/nats-io/nats.go"

	"backend/internal/logger"
	"backend/internal/natsclient"
)

type Worker struct {
	sub *nats.Subscription
}

func NewWorker() *Worker {
	return &Worker{}
}

func (w *Worker) Start() {
	var err error
	w.sub, err = natsclient.NC.Subscribe("telemetry.raw.>", func(m *nats.Msg) {
		w.processTelemetry(m.Data)
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe in worker-alert", "err", err)
	}
}

func (w *Worker) processTelemetry(data []byte) {
	// Parse telemetry, run alert rules (geofence, speed, sos, etc)
	// Output to alert.* stream if alert generated
}

func (w *Worker) Stop() {
	if w.sub != nil {
		w.sub.Unsubscribe()
	}
}
