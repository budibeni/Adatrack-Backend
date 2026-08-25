package controllers

import (
	"strings"
	"sync"

	"ajb_gps/internal"
)

// ---------------------------------------------------------------------------
// hub — WebSocket connection registry + RBAC-filtered, tenant-safe broadcast.
//
// Subject convention: vehicle.update.<vehicle_id> (FR-5.x). Broadcast fans a
// payload out only to clients of the SAME company whose allowed-vehicle set
// contains vehicle_id (FR-5.1 / FR-5.2 + tenant isolation PRD §6). Respects
// FR-5.4 resource limits via client.send buffer (drop-oldest + log).
// ---------------------------------------------------------------------------

// client represents one authenticated WebSocket connection.
type client struct {
	conn        wsConn
	send        chan []byte
	userID      uint64
	companyCode string
	allowed     map[uint64]struct{} // vehicle IDs user may see (empty for ADMIN)
	allowAll    bool                // ADMIN role sees everything within the company
	subs        map[uint64]struct{} // explicit SUBSCRIBE set (empty = all allowed)
	done        chan struct{}
	once        sync.Once
	h           *hub
}

// wsConn abstracts the websocket connection so the hub can be unit-tested
// without a real socket.
type wsConn interface {
	WriteMessage(messageType int, data []byte) error
	Close() error
}

func newClient(conn wsConn, sendChan chan []byte, userID uint64, companyCode string,
	allowed map[uint64]struct{}, allowAll bool, h *hub) *client {
	if allowed == nil {
		allowed = make(map[uint64]struct{})
	}
	return &client{
		conn:        conn,
		send:        sendChan,
		userID:      userID,
		companyCode: strings.ToUpper(companyCode),
		allowed:     allowed,
		allowAll:    allowAll,
		subs:        make(map[uint64]struct{}),
		done:        make(chan struct{}),
		h:           h,
	}
}

type hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	maxConn int
	maxQ    int
}

func newHub(maxConn, maxQueue int) *hub {
	return &hub{
		clients: make(map[*client]struct{}),
		maxConn: maxConn,
		maxQ:    maxQueue,
	}
}

// register adds a connection, rejecting when at capacity (FR-5.4).
func (h *hub) register(cl *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.maxConn {
		return false
	}
	if _, ok := h.clients[cl]; ok {
		return false
	}
	h.clients[cl] = struct{}{}
	return true
}

// unregister removes a connection and signals its pumps to stop.
func (h *hub) unregister(cl *client) {
	h.mu.Lock()
	if _, ok := h.clients[cl]; ok {
		delete(h.clients, cl)
	}
	h.mu.Unlock()
	cl.close()
}

// closeAll shuts down every connection (graceful shutdown).
func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for cl := range h.clients {
		cl.close()
	}
}

// count returns the number of active connections.
func (h *hub) count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// broadcast delivers payload only to clients in the SAME company who are
// allowed to see vehicleID (cross-tenant updates are never leaked).
func (h *hub) broadcast(companyCode string, vehicleID uint64, payload []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for cl := range h.clients {
		if cl.companyCode != strings.ToUpper(companyCode) {
			continue
		}
		if cl.canReceive(vehicleID) {
			cl.enqueue(payload)
		}
	}
}

// canReceive returns true when the client may receive updates for this vehicle.
func (cl *client) canReceive(vehicleID uint64) bool {
	if cl.allowAll {
		return true
	}
	if len(cl.subs) > 0 {
		_, ok := cl.subs[vehicleID]
		return ok
	}
	_, ok := cl.allowed[vehicleID]
	return ok
}

// enqueue adds a message to the outbound queue. When the queue is full the
// oldest pending messages get dropped and a warning is logged (FR-5.4).
func (cl *client) enqueue(payload []byte) {
	select {
	case cl.send <- payload:
		return
	default:
	}
	// Queue penuh -> drop oldest, kirim yang terbaru.
	select {
	case <-cl.send:
		internal.WSMessagesTotal.WithLabelValues("vehicle.update", "dropped").Inc()
	default:
	}
	select {
	case cl.send <- payload:
	default:
		// Masih penuh -> drop pesan ini + log (FR-4.2: log every drop).
		internal.WSMessagesTotal.WithLabelValues("vehicle.update", "dropped").Inc()
	}
}

// close idempotently closes the connection.
func (cl *client) close() {
	cl.once.Do(func() {
		close(cl.done)
		_ = cl.conn.Close()
	})
}
