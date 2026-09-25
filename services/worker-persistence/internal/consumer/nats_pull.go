package consumer

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"backend/internal/logger"
	"backend/internal/models"
	"backend/internal/natsclient"
	"backend/worker-persistence/internal/db"
	"github.com/nats-io/nats.go"
)

type Worker struct {
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

func NewWorker() *Worker {
	ctx, cancel := context.WithCancel(context.Background())
	return &Worker{ctx: ctx, cancel: cancel}
}

func (w *Worker) Start() {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		
		sub, err := natsclient.JS.PullSubscribe("telemetry.raw.>", "worker-persist-group", nats.BindStream("TELEMETRY"))
		if err != nil {
			logger.Log.Error("Failed to pull subscribe", "err", err)
			return
		}

		logger.Log.Info("Worker Persistence NATS Pull Subscriber started")

		for {
			select {
			case <-w.ctx.Done():
				return
			default:
				// Fetch up to 500 messages at a time (Max batch size), wait max 5 seconds
				msgs, err := sub.Fetch(500, nats.MaxWait(5*time.Second))
				if err != nil {
					if err != nats.ErrTimeout {
						logger.Log.Error("Fetch error", "err", err)
						time.Sleep(1 * time.Second) // backoff
					}
					continue
				}

				var payloads []models.TelemetryPayload
				for _, msg := range msgs {
					var p models.TelemetryPayload
					if err := json.Unmarshal(msg.Data, &p); err == nil {
						payloads = append(payloads, p)
					}
				}

				// Process and Ack
				if err := db.BatchInsert(w.ctx, payloads); err == nil {
					for _, msg := range msgs {
						msg.Ack()
					}
					logger.Log.Info("Batch persisted successfully", "count", len(payloads))
				}
			}
		}
	}()
}

func (w *Worker) Stop() {
	logger.Log.Info("Stopping Worker Persistence...")
	w.cancel()
	w.wg.Wait()
	logger.Log.Info("Worker Persistence stopped gracefully")
}
