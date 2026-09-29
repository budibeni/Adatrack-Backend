package controllers

// commanddispatch_race_test.go — regression for the pending-registration race
// that made `make e2e-commands` flaky on 2026-09-29.
//
// Root cause: dispatch() wrote the frame to the socket BEFORE registering the
// command in the pending set. On a fast link (loopback, or a quick device) the
// 0x21 reply is read by the connection goroutine while the dispatcher is still on
// the stack, so Ack() found an empty pending set, discarded the reply as
// "unsolicited", and the row stayed `sent` until the sweeper marked it `timeout`
// — the E2E check then failed with "did not reach status acked" even though the
// device had answered.
//
// The fake device below calls Ack() SYNCHRONOUSLY from inside Write, which is
// exactly that interleaving: with the old ordering the ACK is never matched.

import (
	"net"
	"testing"
	"time"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

// replyDuringWriteConn is a net.Conn whose Write triggers the device reply before
// returning (the frame is captured, never actually sent).
type replyDuringWriteConn struct {
	net.Conn
	onWrite func()
	frames  [][]byte
}

func (c *replyDuringWriteConn) Write(b []byte) (int, error) {
	c.frames = append(c.frames, append([]byte(nil), b...))
	if c.onWrite != nil {
		c.onWrite()
	}
	return len(b), nil
}

func TestDispatchRegistersPendingBeforeWrite(t *testing.T) {
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = 4
	srv := NewServer(cfg, nil, nil)
	defer srv.Shutdown()

	imei := "864201040512345"
	g := testGateway(srv)

	var results []models.CommandResult
	g.onResult = func(res models.CommandResult) { results = append(results, res) }

	serverSide, deviceSide := net.Pipe()
	t.Cleanup(func() { _ = deviceSide.Close(); _ = serverSide.Close() })

	conn := &replyDuringWriteConn{Conn: serverSide}
	conn.onWrite = func() { g.Ack(imei, "DYD=Success!") }
	srv.Conns().Add(&DeviceConn{IMEI: imei, Protocol: models.ProtoGT06, conn: conn,
		ConnectedAt: time.Now().UTC(), Remote: "reply-during-write"})

	cmd := models.DeviceCommand{RequestID: "race-1", CompanyCode: "DEV001", VehicleID: 1,
		IMEI: imei, Kind: models.CommandEngineCut, CreatedAt: time.Now().UTC()}

	res := g.dispatch(cmd)
	if res.Status != models.CommandStatusSent {
		t.Fatalf("dispatch = %s (%s), want sent", res.Status, res.Detail)
	}
	if len(conn.frames) != 1 {
		t.Fatalf("frames written = %d, want 1", len(conn.frames))
	}

	// The decisive assertion: a reply that arrives DURING Write must still be
	// matched to this command.
	if len(results) != 1 || results[0].Status != models.CommandStatusAcked {
		t.Fatalf("reply sent during Write was not matched (onResult=%+v) — "+
			"the command was registered after the write again", results)
	}
	if results[0].RequestID != cmd.RequestID {
		t.Fatalf("matched request_id = %q, want %q", results[0].RequestID, cmd.RequestID)
	}

	g.mu.Lock()
	left := len(g.pending)
	g.mu.Unlock()
	if left != 0 {
		t.Fatalf("pending still holds %d entry/entries after the ACK was matched", left)
	}
}

// TestDispatchWriteFailureDropsOwnPendingEntry makes sure the pre-write
// registration does not leak: when the socket write fails, the entry added before
// the write must be removed again.
func TestDispatchWriteFailureDropsOwnPendingEntry(t *testing.T) {
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = 4
	srv := NewServer(cfg, nil, nil)
	defer srv.Shutdown()

	imei := "864201040599998"
	g := testGateway(srv)

	serverSide, deviceSide := net.Pipe()
	_ = deviceSide.Close()
	_ = serverSide.Close() // writing to a closed pipe fails

	srv.Conns().Add(&DeviceConn{IMEI: imei, Protocol: models.ProtoGT06, conn: serverSide,
		ConnectedAt: time.Now().UTC(), Remote: "closed-pipe"})

	res := g.dispatch(models.DeviceCommand{RequestID: "fail-1", CompanyCode: "DEV001",
		VehicleID: 1, IMEI: imei, Kind: models.CommandEngineCut, CreatedAt: time.Now().UTC()})
	if res.Status != models.CommandStatusFailed {
		t.Fatalf("dispatch on a closed socket = %s, want failed", res.Status)
	}

	g.mu.Lock()
	left := len(g.pending)
	g.mu.Unlock()
	if left != 0 {
		t.Fatalf("pending leaked %d entry/entries after a failed write", left)
	}
}
