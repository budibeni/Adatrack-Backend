package controllers

import (
	"sync"
	"testing"
)

// fakeConn implements wsConn for hub tests without a real socket.
type fakeConn struct {
	mu       sync.Mutex
	messages [][]byte
	closed   bool
}

func (f *fakeConn) WriteMessage(_ int, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, data)
	return nil
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func newTestClient(f *fakeConn, send chan []byte, userID uint64, company string,
	allowed map[uint64]struct{}, allowAll bool) *client {
	return newClient(f, send, userID, company, allowed, allowAll, nil)
}

// Broadcast hanya sampai ke client yang berhak atas vehicle tsb (RBAC WS)
// DAN berada di company yang sama (isolasi tenant).
func TestHubBroadcastRBAC(t *testing.T) {
	h := newHub(100, 16)

	f1 := &fakeConn{}
	c1 := newTestClient(f1, make(chan []byte, 16), 1, "DEV001", map[uint64]struct{}{10: {}}, false)
	f2 := &fakeConn{}
	c2 := newTestClient(f2, make(chan []byte, 16), 2, "DEV001", map[uint64]struct{}{20: {}}, false) // tak berhak atas 10
	f3 := &fakeConn{}                                                                               // ADMIN boleh semua
	c3 := newTestClient(f3, make(chan []byte, 16), 3, "DEV001", nil, true)
	f4 := &fakeConn{} // company berbeda → TIDAK boleh terima update DEV001
	c4 := newTestClient(f4, make(chan []byte, 16), 4, "LOGI002", map[uint64]struct{}{10: {}}, false)

	if !h.register(c1) || !h.register(c2) || !h.register(c3) || !h.register(c4) {
		t.Fatal("failed to register clients")
	}
	defer h.unregister(c1)
	defer h.unregister(c2)
	defer h.unregister(c3)
	defer h.unregister(c4)

	h.broadcast("DEV001", 10, []byte("vehicle:10-update"))

	if got := len(c1.send); got != 1 {
		t.Errorf("c1 should receive 1 update, got %d", got)
	}
	if got := len(c2.send); got != 0 {
		t.Errorf("c2 (no access) should receive 0 updates, got %d", got)
	}
	if got := len(c3.send); got != 1 {
		t.Errorf("admin c3 should receive 1 update, got %d", got)
	}
	if got := len(c4.send); got != 0 {
		t.Errorf("c4 (different company) must NOT receive cross-tenant update, got %d", got)
	}
}

// Queue melebihi kapasitas → pesan terlama di-drop (FR-5.4 drop-oldest).
func TestEnqueueDropsOldestWhenFull(t *testing.T) {
	send := make(chan []byte, 2)
	f := &fakeConn{}
	cl := newTestClient(f, send, 1, "DEV001", nil, true)

	cl.enqueue([]byte("msg-1"))
	cl.enqueue([]byte("msg-2"))
	cl.enqueue([]byte("msg-3")) // queue penuh -> msg-1 di-drop

	if got := len(send); got != 2 {
		t.Fatalf("expected queue length 2, got %d", got)
	}
	first := <-send
	if string(first) != "msg-2" {
		t.Errorf("expected oldest (msg-1) dropped, first queued = %q", first)
	}
	second := <-send
	if string(second) != "msg-3" {
		t.Errorf("expected newest retained, got %q", second)
	}
}

// Kapasitas koneksi maksimum (maxConn) harus dibatasi (FR-5.4).
func TestHubRegisterEnforcesMaxConnections(t *testing.T) {
	h := newHub(1, 4)

	c1 := newClient(&fakeConn{}, make(chan []byte, 4), 1, "DEV001", map[uint64]struct{}{}, false, h)
	if !h.register(c1) {
		t.Fatal("first register should succeed")
	}
	c2 := newClient(&fakeConn{}, make(chan []byte, 4), 2, "DEV001", map[uint64]struct{}{}, false, h)
	if h.register(c2) {
		t.Fatal("register beyond max connections should fail")
	}
	if h.count() != 1 {
		t.Errorf("expected 1 active connection, got %d", h.count())
	}
}

// Explicit SUBSCRIBE mempersempit rute yang diterima; tanpa subscribe menerima
// semua vehicle yang diizinkan (FR-5.1).
func TestClientExplicitSubscribeAndUnsubscribe(t *testing.T) {
	allowed := map[uint64]struct{}{10: {}, 20: {}}
	f := &fakeConn{}
	c := newTestClient(f, make(chan []byte, 8), 1, "DEV001", allowed, false)

	if !c.canReceive(10) || !c.canReceive(20) {
		t.Fatal("default should allow all permitted vehicles")
	}

	c.subs[10] = struct{}{}
	if !c.canReceive(10) {
		t.Error("explicit SUBSCRIBE should enable vehicle 10")
	}
	if c.canReceive(20) {
		t.Error("explicit subscribe set should exclude vehicle 20")
	}

	delete(c.subs, 10)
	if !c.canReceive(20) {
		t.Error("unsubscribed back to default should allow permitted vehicles again")
	}
}
