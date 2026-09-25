package ws

import (
	"context"
	"encoding/json"
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
			subjectParts := strings.Split(msg.Subject, ".")
			var companyCode string
			if len(subjectParts) >= 4 {
				companyCode = subjectParts[2] // telemetry.live.{companyCode}.{imei}
			} else {
				var payload struct {
					CompanyCode string `json:"company_code"`
				}
				if err := json.Unmarshal(msg.Data, &payload); err == nil {
					companyCode = payload.CompanyCode
				}
			}
			if companyCode == "" {
				continue
			}
			
			// Format as VEHICLE_UPDATE
			var sendData []byte = msg.Data
			if strings.HasPrefix(msg.Subject, "telemetry.live.") {
				var rawPayload map[string]interface{}
				if err := json.Unmarshal(msg.Data, &rawPayload); err == nil {
					if accStatus, ok := rawPayload["acc_status"].(float64); ok {
						rawPayload["acc"] = (accStatus == 1)
						delete(rawPayload, "acc_status")
					}
					
					finalPayload := map[string]interface{}{
						"event": "VEHICLE_UPDATE",
						"data":  rawPayload,
					}
					sendData, _ = json.Marshal(finalPayload)
				}
			}

			for client := range h.clients {
				if client.claims != nil && (client.claims.CompanyCode == companyCode || (client.claims.Role == "SuperAdmin" && client.claims.CompanyCode == "DEFAULT")) {
					select {
					case client.send <- sendData:
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
	
	alertSub, err := natsclient.NC.Subscribe("alert.all", func(m *nats.Msg) {
		h.broadcast <- m
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe to alerts", "err", err)
	}

	mediaSub, err := natsclient.NC.Subscribe("media.event.>", func(m *nats.Msg) {
		h.broadcast <- m
	})
	if err != nil {
		logger.Log.Error("Failed to subscribe to media events", "err", err)
	}

	<-ctx.Done()
	sub.Unsubscribe()
	if alertSub != nil {
		alertSub.Unsubscribe()
	}
	if mediaSub != nil {
		mediaSub.Unsubscribe()
	}
}

func (h *Hub) Stop() {
	close(h.stop)
}
