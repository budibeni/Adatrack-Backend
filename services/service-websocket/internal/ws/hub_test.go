package ws

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nats-io/nats.go"

	"backend/internal/auth"
	"backend/internal/config"
)

func TestHub_TenantIsolation(t *testing.T) {
	cfg := &config.Config{}
	hub := NewHub(cfg)
	go hub.Run()
	defer hub.Stop()

	clientA := &Client{
		hub:  hub,
		send: make(chan []byte, 10),
		claims: &auth.Claims{
			UserID:      101,
			CompanyCode: "COMPANY_A",
			Role:        "Admin",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			},
		},
	}

	clientB := &Client{
		hub:  hub,
		send: make(chan []byte, 10),
		claims: &auth.Claims{
			UserID:      102,
			CompanyCode: "COMPANY_B",
			Role:        "Admin",
			RegisteredClaims: jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			},
		},
	}

	hub.register <- clientA
	hub.register <- clientB
	time.Sleep(20 * time.Millisecond)

	// Broadcast payload for COMPANY_A via topic
	msgPayload := map[string]interface{}{
		"imei":         "123456789012345",
		"company_code": "COMPANY_A",
		"speed":        65.5,
	}
	payloadBytes, _ := json.Marshal(msgPayload)

	hub.broadcast <- &nats.Msg{
		Subject: "telemetry.live.COMPANY_A.123456789012345",
		Data:    payloadBytes,
	}

	select {
	case received := <-clientA.send:
		var result map[string]interface{}
		json.Unmarshal(received, &result)
		if result["company_code"] != "COMPANY_A" {
			t.Errorf("expected COMPANY_A, got %v", result["company_code"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("expected clientA to receive broadcast message")
	}

	// Verify clientB receives NOTHING (Tenant Isolation)
	select {
	case data := <-clientB.send:
		t.Fatalf("cross-tenant leakage detected! ClientB received: %s", string(data))
	case <-time.After(100 * time.Millisecond):
		// Success: clientB did not receive data from COMPANY_A
	}

	// Unregister
	hub.unregister <- clientA
	hub.unregister <- clientB
	time.Sleep(20 * time.Millisecond)
}
