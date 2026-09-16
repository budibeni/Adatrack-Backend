package ws

import (
	"context"
	"strings"

	"github.com/nats-io/nats.go"

	"backend/internal/config"
	"backend/internal/logger"
	"backend/internal/natsclient"
)

type Hub struct {
	cfg        *config.Config
	clients    map[*Client]bool
	broadcast  chan *nats.Msg
	register   chan *Client
	unregister chan *Client
	stop       chan struct{}
}

func NewHub(cfg *config.Config) *Hub {
	return &Hub{
		cfg:        cfg,
		broadcast:  make(chan *nats.Msg),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
		stop:       make(chan struct{}),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.clients[client] = true
		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
		case msg := <-h.broadcast:
			// Parse routing key (telemetry.live.<tenant>.<imei> perhaps?)
			// We will broadcast to clients of the same tenant.
			subjectParts := strings.Split(msg.Subject, ".")
			var companyCode string
			if len(subjectParts) >= 3 {
				companyCode = subjectParts[2] // Assuming telemetry.live.{companyCode}.{imei}
			}
			for client := range h.clients {
				// Only send if the client belongs to the company, or if they are SuperAdmin of the platform (but usually SuperAdmins shouldn't get all live data, but let's assume strict tenant isolation for WS).
				if client.claims.CompanyCode == companyCode {
					select {
					case client.send <- msg.Data:
					default:
						close(client.send)
						delete(h.clients, client)
					}
				}
			}
		case <-h.stop:
			return
		}
	}
}

func (h *Hub) StartConsumer(ctx context.Context) {
	sub, err := natsclient.NC.Subscribe("telemetry.live.>", func(m *nats.Msg) {
		h.broadcast <- m
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe to live telemetry", "err", err)
		return
	}
	<-ctx.Done()
	sub.Unsubscribe()
}

func (h *Hub) Stop() {
	close(h.stop)
}
