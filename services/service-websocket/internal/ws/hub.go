package ws

import (
	"context"

	"github.com/nats-io/nats.go"

	"backend/internal/config"
	"backend/internal/logger"
	"backend/internal/natsclient"
)

type Hub struct {
	cfg        *config.Config
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	stop       chan struct{}
}

func NewHub(cfg *config.Config) *Hub {
	return &Hub{
		cfg:        cfg,
		broadcast:  make(chan []byte),
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
		case message := <-h.broadcast:
			for client := range h.clients {
				// Needs RBAC filtering per client based on vehicles allowed
				select {
				case client.send <- message:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		case <-h.stop:
			return
		}
	}
}

func (h *Hub) StartConsumer(ctx context.Context) {
	sub, err := natsclient.NC.Subscribe("telemetry.live.>", func(m *nats.Msg) {
		h.broadcast <- m.Data
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
