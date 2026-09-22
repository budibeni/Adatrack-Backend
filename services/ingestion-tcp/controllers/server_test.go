package controllers

// server_test.go — siklus hidup + dispatch server TCP (PRD Module 1, FR-1.1..FR-1.6).
//
// Sebelum file ini, AcceptLoop / handleConn / connClose / readDeadlined / Shutdown
// semuanya 0 %: yang diuji hanya parser paket, bukan jalur yang benar-benar
// dilewati perangkat. Test memakai listener loopback sungguhan (127.0.0.1:0) dan
// berhenti pada pembacaan paket (idle/EOF), sehingga TIDAK ada telemetri yang
// dipublikasikan ke NATS dan run endurance tidak terpengaruh.

import (
	"io"
	"net"
	"testing"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newTestServer builds a Server with only the knobs the accept loop reads
// (FR-1.1 budget, FR-1.3 idle timeout). tenants/nats stay nil because the idle and
// EOF paths exercised here never reach them.
func newTestServer(t *testing.T, maxConn int, idle time.Duration) *Server {
	t.Helper()
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = maxConn
	cfg.TCP.IdleTimeout = idle
	s := NewServer(cfg, nil, nil)
	t.Cleanup(s.Shutdown)
	return s
}

// waitFor polls cond until it holds (or fails the test).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout menunggu %s", what)
}

// TestServerConfigAndIdempotentShutdown asserts the accessors and that Shutdown is
// safe to call repeatedly (closeOnce) — it is called from signal handlers AND from
// deferred cleanup paths.
func TestServerConfigAndIdempotentShutdown(t *testing.T) {
	s := newTestServer(t, 7, 3*time.Second)

	if got := s.Config(); got == nil || got.TCP.MaxConnections != 7 {
		t.Fatalf("Config().TCP.MaxConnections = %v, want 7", got)
	}
	if got := s.DroppedFrames(); got != 0 {
		t.Fatalf("DroppedFrames() = %d, want 0", got)
	}

	s.Shutdown()
	s.Shutdown() // tidak boleh panic (closeOnce)
	if s.ctx.Err() == nil {
		t.Fatal("ctx belum dibatalkan setelah Shutdown")
	}
	if got := s.DroppedFrames(); got != 0 {
		t.Fatalf("DroppedFrames() setelah Shutdown = %d, want 0", got)
	}
}

// TestAcceptLoopDispatchesAndReleasesBudget covers the happy path: a connection is
// accepted, dispatched to the GT06 handler and its budget slot released when the
// device disconnects (FR-1.1 connection accounting).
func TestAcceptLoopDispatchesAndReleasesBudget(t *testing.T) {
	s := newTestServer(t, 2, 5*time.Second)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go s.AcceptLoop(ln, models.ProtoGT06)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	waitFor(t, "slot budget terisi", func() bool { return len(s.connBudget) == 1 })

	// Perangkat diam lalu putus: handler melihat EOF dan melepas slot.
	_ = conn.Close()
	waitFor(t, "slot budget dilepas", func() bool { return len(s.connBudget) == 0 })
}

// TestAcceptLoopRejectsBeyondBudget asserts FR-1.1: saat budget penuh koneksi baru
// ditolak (ditutup) dan penghitungan rejection naik — bukan menggantung.
func TestAcceptLoopRejectsBeyondBudget(t *testing.T) {
	s := newTestServer(t, 1, 10*time.Second)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go s.AcceptLoop(ln, models.ProtoTeltonika)

	first, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial first: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	waitFor(t, "slot budget terisi", func() bool { return len(s.connBudget) == 1 })

	before := testutil.ToFloat64(rejectedTotal.WithLabelValues("max_conn"))

	second, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial second: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, rerr := second.Read(make([]byte, 1)); rerr == nil {
		t.Fatal("koneksi melebihi budget tidak ditutup server")
	} else if ne, ok := rerr.(net.Error); ok && ne.Timeout() {
		t.Fatal("koneksi melebihi budget dibiarkan menggantung (timeout, bukan ditutup)")
	}

	if after := testutil.ToFloat64(rejectedTotal.WithLabelValues("max_conn")); after <= before {
		t.Fatalf("rejectedTotal{max_conn} tidak naik: %v -> %v", before, after)
	}

	// Koneksi pertama harus tetap hidup (tidak ikut diputus oleh penolakan).
	_ = first.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, rerr := first.Read(make([]byte, 1)); rerr == io.EOF {
		t.Fatal("koneksi yang sudah diterima ikut diputus saat budget penuh")
	}
}

// TestShutdownStopsAcceptLoop asserts the accept loop exits after Shutdown instead
// of leaking a goroutine per listener.
//
// Catatan: pada Shutdown yang masih menyisakan slot budget, `select` di AcceptLoop
// memilih secara acak antara "dispatch koneksi" dan "kembali" — keduanya siap.
// Jadi test ini mendial berulang sampai loop benar-benar keluar, bukan menebak
// cabang mana yang dipilih.
func TestShutdownStopsAcceptLoop(t *testing.T) {
	s := newTestServer(t, 2, 2*time.Second)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	done := make(chan struct{})
	go func() {
		s.AcceptLoop(ln, models.ProtoGT06)
		close(done)
	}()

	s.Shutdown()

	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case <-done:
			if got := len(s.connBudget); got != 0 {
				t.Fatalf("slot budget tersisa %d setelah Shutdown, want 0", got)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("AcceptLoop tidak berhenti setelah Shutdown")
		}
		// Setiap dial memberi loop satu kesempatan melihat ctx.Done.
		if conn, derr := net.Dial("tcp", ln.Addr().String()); derr == nil {
			_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			_, _ = conn.Read(make([]byte, 1))
			_ = conn.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
}
