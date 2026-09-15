package controllers

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"ajb_gps/service-websocket/models"
)

// wsDial opens a WebSocket against the harness (token via the documented query
// parameter, PRD §8.3).
func wsDial(t *testing.T, h *testHarness, token, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	url := "ws" + strings.TrimPrefix(h.server.URL, "http") + "/ws/v1/adatrack"
	if token != "" {
		url += "?token=" + token
	}
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	return dialer.Dial(url, header)
}

// readEvent reads frames until `want` arrives (or the deadline expires).
func readEvent(t *testing.T, conn *websocket.Conn, want string, timeout time.Duration) models.WSEnvelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if err := conn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read %s: %v", want, err)
		}
		var envelope models.WSEnvelope
		if uerr := json.Unmarshal(raw, &envelope); uerr != nil {
			t.Fatalf("decode frame %s: %v", string(raw), uerr)
		}
		if envelope.Event == want {
			return envelope
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

// sendWS writes one client frame.
func sendWS(t *testing.T, conn *websocket.Conn, payload any) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal ws request: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatalf("write ws request: %v", err)
	}
}

// expectNoFrame asserts nothing is delivered within the window.
func expectNoFrame(t *testing.T, conn *websocket.Conn, window time.Duration, message string) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(window)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	if _, raw, err := conn.ReadMessage(); err == nil {
		t.Fatalf("%s (received %s)", message, string(raw))
	}
}

// TestWSHandshakeRequiresToken asserts an unauthenticated upgrade is refused.
func TestWSHandshakeRequiresToken(t *testing.T) {
	h := newHarness(t)

	conn, resp, err := wsDial(t, h, "", "")
	if err == nil {
		_ = conn.Close()
		t.Fatalf("unauthenticated websocket was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("handshake status = %v, want 401", resp)
	}
}

// TestWSHandshakeRejectsRevokedToken asserts the revocation check also guards the
// WebSocket handshake (FR-5.7).
func TestWSHandshakeRejectsRevokedToken(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	if resp, _ := h.do(t, http.MethodPost, "/api/v1/auth/logout", access, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("logout failed")
	}
	conn, resp, err := wsDial(t, h, access, "")
	if err == nil {
		_ = conn.Close()
		t.Fatalf("revoked token opened a websocket")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("handshake status = %v, want 401", resp)
	}
}

// TestWSOriginValidation asserts browser origins are validated (FR-5.4).
func TestWSOriginValidation(t *testing.T) {
	h := newHarness(t)
	access, _ := h.login(t, "admin@dev001.io", "Admin@123")

	conn, resp, err := wsDial(t, h, access, "http://evil.example.com")
	if err == nil {
		_ = conn.Close()
		t.Fatalf("non-allowlisted origin was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("handshake status = %v, want 403", resp)
	}

	// The allowlisted dashboard origin is accepted.
	conn, _, err = wsDial(t, h, access, "http://localhost:3000")
	if err != nil {
		t.Fatalf("allowlisted origin rejected: %v", err)
	}
	defer func() { _ = conn.Close() }()
}

// TestWSCapacityLimit asserts FR-5.4 (bounded connections → 503).
func TestWSCapacityLimit(t *testing.T) {
	h := newHarness(t)
	h.service.hub.settings.WSMaxConnections = 1

	access, _ := h.login(t, "admin@dev001.io", "Admin@123")
	first, _, err := wsDial(t, h, access, "")
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer func() { _ = first.Close() }()

	conn, resp, err := wsDial(t, h, access, "")
	if err == nil {
		_ = conn.Close()
		t.Fatalf("connection beyond WS_MAX_CONNECTIONS was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("handshake status = %v, want 503", resp)
	}
}
