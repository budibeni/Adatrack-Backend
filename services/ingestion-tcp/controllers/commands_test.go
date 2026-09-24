package controllers

// commands_test.go — B8 downlink: frame encoding, connection registry, dispatcher
// and ACK capture (PRD §21.2 row 1, acceptance "perintah terkirim & ACK device
// tercatat").
//
// Hermetic: the "device" is one end of a net.Pipe, so the test asserts the EXACT
// bytes written to the socket (what the device would parse) and feeds the reply
// back through the same handler the real GT06 session uses.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"adatrack_gps/ingestion-tcp/models"
	"adatrack_gps/internal"
)

// testGateway builds a dispatcher whose persistence/publish paths are disabled
// (no tenant manager, no NATS) so only the wire behaviour is under test.
func testGateway(srv *Server) *commandGateway {
	return &commandGateway{
		srv:        srv,
		ackTimeout: 30 * time.Second,
		pending:    map[string]*pendingCommand{},
	}
}

// devicePipe registers one side of a pipe in the registry and returns the device
// end (what the decoder would read).
func devicePipe(t *testing.T, srv *Server, imei string, proto models.Protocol) net.Conn {
	t.Helper()
	serverSide, deviceSide := net.Pipe()
	t.Cleanup(func() { _ = deviceSide.Close(); _ = serverSide.Close() })
	srv.Conns().Add(&DeviceConn{IMEI: imei, Protocol: proto, conn: serverSide,
		ConnectedAt: time.Now().UTC(), Remote: "pipe"})
	return deviceSide
}

func TestBuildGT06OnlineCommandFraming(t *testing.T) {
	SetGT06CommandLengthIncludesCRC(false)
	defer SetGT06CommandLengthIncludesCRC(false)

	frame := BuildGT06OnlineCommand("DYD#", 1)

	// start | length | proto | cmdLen | flag(4) | content | serial | crc | stop
	if len(frame) != 2+1+1+1+4+4+2+2+2 {
		t.Fatalf("frame length = %d, want 19 bytes", len(frame))
	}
	if frame[0] != 0x78 || frame[1] != 0x78 {
		t.Fatalf("start bytes = %02x %02x, want 78 78", frame[0], frame[1])
	}
	// Length counts proto + cmdLen + flag + content + serial (CRC excluded — the
	// convention of the verified client→server framing).
	if want := byte(1 + 1 + 4 + len("DYD#") + 2); frame[2] != want {
		t.Fatalf("length = %d, want %d", frame[2], want)
	}
	if frame[3] != 0x80 {
		t.Fatalf("protocol = 0x%02x, want 0x80 (online command)", frame[3])
	}
	if frame[4] != byte(4+len("DYD#")) {
		t.Fatalf("length-of-command = %d, want %d", frame[4], 4+len("DYD#"))
	}
	if s := string(frame[9:13]); s != "DYD#" {
		t.Fatalf("content = %q, want DYD#", s)
	}
	if got := binary.BigEndian.Uint16(frame[13:15]); got != 1 {
		t.Fatalf("serial = %d, want 1", got)
	}
	if frame[len(frame)-2] != 0x0D || frame[len(frame)-1] != 0x0A {
		t.Fatalf("stop bytes = %02x %02x, want 0d 0a", frame[len(frame)-2], frame[len(frame)-1])
	}
	// The CRC covers length||proto||content (GT06 "Error Check").
	want := crc16(frame[2 : len(frame)-4])
	if got := binary.BigEndian.Uint16(frame[len(frame)-4 : len(frame)-2]); got != want {
		t.Fatalf("crc = 0x%04x, want 0x%04x", got, want)
	}

	// The vendor §8.1 example counts the CRC in Length — the toggle switches it.
	SetGT06CommandLengthIncludesCRC(true)
	alt := BuildGT06OnlineCommand("DYD#", 1)
	if alt[2] != frame[2]+2 {
		t.Fatalf("length with CRC counted = %d, want %d", alt[2], frame[2]+2)
	}
	// Everything except the Length byte and the CRC (which covers Length) is
	// identical between the two conventions.
	for i := 3; i < len(frame)-4; i++ {
		if alt[i] != frame[i] {
			t.Fatalf("byte %d differs between conventions: %02x vs %02x", i, alt[i], frame[i])
		}
	}
}

// TestTK103CommandEncoding covers the TK103 downlink encoder (verified against the
// upstream Tk103ProtocolEncoder command letters).
func TestTK103CommandEncoding(t *testing.T) {
	enc := tk103Decoder{}
	imei := "864201040512345"
	cases := []struct {
		cmd  models.DeviceCommand
		want string
	}{
		{models.DeviceCommand{IMEI: imei, Kind: models.CommandEngineCut}, "(" + imei + "AV010)"},
		{models.DeviceCommand{IMEI: imei, Kind: models.CommandEngineRestore}, "(" + imei + "AV011)"},
		{models.DeviceCommand{IMEI: imei, Kind: models.CommandReboot}, "(" + imei + "AT00)"},
		{models.DeviceCommand{IMEI: imei, Kind: models.CommandLocate}, "(" + imei + "AP00)"},
		{models.DeviceCommand{IMEI: imei, Kind: models.CommandSetInterval, IntervalSeconds: 20}, "(" + imei + "AR0000140000)"},
	}
	for _, tc := range cases {
		frame, err := enc.EncodeCommand(tc.cmd)
		if err != nil {
			t.Fatalf("%s: %v", tc.cmd.Kind, err)
		}
		if string(frame) != tc.want {
			t.Fatalf("%s: frame = %q, want %q", tc.cmd.Kind, frame, tc.want)
		}
	}
	if _, err := enc.EncodeCommand(models.DeviceCommand{IMEI: imei, Kind: "open_trunk"}); !errors.Is(err, ErrCommandUnsupported) {
		t.Fatalf("unsupported kind: err = %v, want ErrCommandUnsupported", err)
	}
	if _, err := enc.EncodeCommand(models.DeviceCommand{Kind: models.CommandReboot}); err == nil {
		t.Fatal("a command without an IMEI was encoded")
	}
	if _, err := enc.EncodeCommand(models.DeviceCommand{IMEI: imei, Kind: models.CommandSetInterval, IntervalSeconds: 1}); err == nil {
		t.Fatal("interval 1 s was accepted (must be 5..86400)")
	}
}

// TestDispatchRefusesSecondCommandWhileDeviceIsBusy covers the single-in-flight
// rule: a GT06 reply carries no request id, so two outstanding commands would be
// ambiguous (the live E2E run acked the older one and left the newer `sent`).
func TestDispatchRefusesSecondCommandWhileDeviceIsBusy(t *testing.T) {
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = 4
	srv := NewServer(cfg, nil, nil)
	defer srv.Shutdown()

	imei := "864201040512345"
	device := devicePipe(t, srv, imei, models.ProtoGT06)
	g := testGateway(srv)

	// net.Pipe is synchronous: the device end must be read continuously or the
	// dispatcher would block on its own write (that is what the real decoder does).
	frames := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 128)
		for {
			n, err := device.Read(buf)
			if err != nil {
				return
			}
			frames <- append([]byte(nil), buf[:n]...)
		}
	}()

	first := models.DeviceCommand{RequestID: "req-1", CompanyCode: "DEV001", VehicleID: 1,
		IMEI: imei, Kind: models.CommandEngineCut, CreatedAt: time.Now().UTC()}
	res := g.dispatch(first)
	if res.Status != models.CommandStatusSent {
		t.Fatalf("first dispatch = %s (%s), want sent", res.Status, res.Detail)
	}
	select {
	case <-frames:
	case <-time.After(2 * time.Second):
		t.Fatal("the first frame never reached the device")
	}

	second := models.DeviceCommand{RequestID: "req-2", CompanyCode: "DEV001", VehicleID: 1,
		IMEI: imei, Kind: models.CommandEngineRestore, CreatedAt: time.Now().UTC()}
	res = g.dispatch(second)
	if res.Status != models.CommandStatusFailed {
		t.Fatalf("second dispatch = %s, want failed (device busy)", res.Status)
	}
	if !strings.Contains(res.Detail, "req-1") {
		t.Fatalf("busy detail = %q, want the in-flight request id", res.Detail)
	}
	select {
	case f := <-frames:
		t.Fatalf("a second frame was written while the device was busy: % x", f)
	case <-time.After(200 * time.Millisecond):
	}

	// After the first command is acknowledged the device accepts the next one.
	g.Ack(imei, "DYD=Success!")
	if got := g.inflightFor(imei); got != nil {
		t.Fatalf("inflight after ack = %+v, want nil", got)
	}
	res = g.dispatch(second)
	if res.Status != models.CommandStatusSent {
		t.Fatalf("dispatch after ack = %s (%s), want sent", res.Status, res.Detail)
	}
	select {
	case <-frames:
	case <-time.After(2 * time.Second):
		t.Fatal("the follow-up frame never reached the device")
	}
}

// TestHandleRequestDropsStaleCommand covers the backlog guard: a JetStream consumer
// recreation redelivers the whole stream, and an hours-old remote command must not
// reach a moving vehicle.
func TestHandleRequestDropsStaleCommand(t *testing.T) {
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = 4
	srv := NewServer(cfg, nil, nil)
	defer srv.Shutdown()

	g := testGateway(srv)
	g.maxAge = 1 * time.Minute
	results := make(chan models.CommandResult, 4)
	g.onResult = func(res models.CommandResult) { results <- res }

	stale := models.DeviceCommand{RequestID: "req-old", CompanyCode: "DEV001",
		IMEI: "864201040512345", Kind: models.CommandEngineCut,
		CreatedAt: time.Now().UTC().Add(-2 * time.Hour)}
	if err := g.handleRequest(&nats.Msg{Subject: models.CommandSubjectRequest("DEV001"),
		Data: mustJSON(t, stale)}); err != nil {
		t.Fatalf("handleRequest(stale) = %v, want nil (the message must be acknowledged)", err)
	}
	select {
	case res := <-results:
		if res.RequestID != "req-old" || res.Status != models.CommandStatusFailed {
			t.Fatalf("stale result = %+v, want failed", res)
		}
		if !strings.Contains(res.Detail, "COMMAND_MAX_AGE_SECONDS") {
			t.Fatalf("stale detail = %q, want the max-age reason", res.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a stale command was not recorded (it would be re-sent on redelivery)")
	}

	// A command inside the window is dispatched normally. No device is registered in
	// this test, so the recorded outcome is `offline` — the point is that it is NOT
	// reported as stale.
	fresh := stale
	fresh.RequestID = "req-new"
	fresh.CreatedAt = time.Now().UTC()
	if err := g.handleRequest(&nats.Msg{Subject: models.CommandSubjectRequest("DEV001"),
		Data: mustJSON(t, fresh)}); err != nil {
		t.Fatalf("handleRequest(fresh) = %v, want nil", err)
	}
	select {
	case res := <-results:
		if res.Status != models.CommandStatusOffline {
			t.Fatalf("fresh result = %+v, want offline (device not connected)", res)
		}
		if strings.Contains(res.Detail, "COMMAND_MAX_AGE_SECONDS") {
			t.Fatalf("a fresh command was dropped as stale: %q", res.Detail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a fresh command produced no outcome")
	}
}

// mustJSON marshals a command for a synthetic NATS message.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestGT06CommandContentWhitelist(t *testing.T) {
	cases := []struct {
		cmd  models.DeviceCommand
		want string
	}{
		{models.DeviceCommand{Kind: models.CommandEngineCut}, "DYD#"},
		{models.DeviceCommand{Kind: models.CommandEngineRestore}, "HFYD#"},
		{models.DeviceCommand{Kind: models.CommandReboot}, "RESET#"},
		{models.DeviceCommand{Kind: models.CommandLocate}, "DWXX#"},
		{models.DeviceCommand{Kind: models.CommandSetInterval, IntervalSeconds: 20}, "TIMER,20#"},
	}
	for _, tc := range cases {
		got, err := GT06CommandContent(tc.cmd)
		if err != nil {
			t.Fatalf("%s: %v", tc.cmd.Kind, err)
		}
		if got != tc.want {
			t.Fatalf("%s: content = %q, want %q", tc.cmd.Kind, got, tc.want)
		}
	}
	if _, err := GT06CommandContent(models.DeviceCommand{Kind: "open_trunk"}); !errors.Is(err, ErrCommandUnsupported) {
		t.Fatalf("unsupported command: err = %v, want ErrCommandUnsupported", err)
	}
	if _, err := GT06CommandContent(models.DeviceCommand{Kind: models.CommandSetInterval, IntervalSeconds: 1}); err == nil {
		t.Fatal("interval 1 s was accepted (must be 5..86400)")
	}
}

func TestDeviceCommandValidate(t *testing.T) {
	valid := models.DeviceCommand{CompanyCode: "DEV001", IMEI: "864201040512345", Kind: models.CommandEngineCut}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
	bad := []models.DeviceCommand{
		{IMEI: "864201040512345", Kind: models.CommandEngineCut},
		{CompanyCode: "DEV001", Kind: models.CommandEngineCut},
		{CompanyCode: "DEV001", IMEI: "864201040512345", Kind: "open_trunk"},
		{CompanyCode: "DEV001", IMEI: "864201040512345", Kind: models.CommandSetInterval, IntervalSeconds: 3},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("invalid command %d accepted: %+v", i, c)
		}
	}
}

func TestConnRegistryReconnectSemantics(t *testing.T) {
	reg := NewConnRegistry(2)
	old, oldDev := net.Pipe()
	newer, newerDev := net.Pipe()
	defer func() { _ = oldDev.Close(); _ = newerDev.Close() }()

	first := &DeviceConn{IMEI: "1", Protocol: models.ProtoGT06, conn: old, ConnectedAt: time.Now()}
	second := &DeviceConn{IMEI: "1", Protocol: models.ProtoXexun, conn: newer, ConnectedAt: time.Now()}

	reg.Add(first)
	reg.Add(second) // a reconnect replaces the stale socket
	if got, ok := reg.Get("1"); !ok || got != second {
		t.Fatal("registry did not keep the newest connection")
	}
	// The old handler must not evict the newer socket when it finally closes.
	reg.Remove(first)
	if !reg.Online("1") {
		t.Fatal("the stale handler removed the live connection")
	}
	reg.Remove(second)
	if reg.Online("1") || reg.Len() != 0 {
		t.Fatal("the current connection was not removed")
	}
}

func TestDispatchWritesFrameToRegisteredDevice(t *testing.T) {
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = 4
	srv := NewServer(cfg, nil, nil)
	defer srv.Shutdown()

	imei := "864201040512345"
	device := devicePipe(t, srv, imei, models.ProtoGT06)
	g := testGateway(srv)

	cmd := models.DeviceCommand{
		RequestID: "req-sent", CompanyCode: "DEV001", VehicleID: 1, IMEI: imei,
		Kind: models.CommandEngineCut, CreatedAt: time.Now().UTC(),
	}

	// The device end reads whatever the dispatcher writes (pipe = no buffering).
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := device.Read(buf)
		if err != nil {
			return
		}
		got <- buf[:n]
	}()

	res := g.dispatch(cmd)
	if res.Status != models.CommandStatusSent {
		t.Fatalf("dispatch status = %s (%s), want sent", res.Status, res.Detail)
	}
	select {
	case frame := <-got:
		if len(frame) != 19 || frame[3] != 0x80 || string(frame[9:13]) != "DYD#" {
			t.Fatalf("device received % x, want the 0x80 DYD# online command", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no command frame was written to the device socket")
	}
	if len(g.pending) != 1 {
		t.Fatalf("pending set = %d, want 1 (the command waits for its ACK)", len(g.pending))
	}
}

func TestDispatchOfflineAndUnsupportedProtocol(t *testing.T) {
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = 4
	srv := NewServer(cfg, nil, nil)
	defer srv.Shutdown()
	g := testGateway(srv)

	// No live connection → `offline` (a normal outcome, not a failure).
	res := g.dispatch(models.DeviceCommand{
		RequestID: "r1", CompanyCode: "DEV001", IMEI: "864201040512345", Kind: models.CommandEngineCut,
	})
	if res.Status != models.CommandStatusOffline {
		t.Fatalf("status = %s, want offline", res.Status)
	}

	// Teltonika has no documented downlink encoder → explicit `failed: unsupported`
	// instead of inventing a frame.
	imei := "864201040512999"
	_ = devicePipe(t, srv, imei, models.ProtoTeltonika)
	res = g.dispatch(models.DeviceCommand{
		RequestID: "r2", CompanyCode: "DEV001", IMEI: imei, Kind: models.CommandReboot,
	})
	if res.Status != models.CommandStatusFailed || !strings.Contains(res.Detail, "downlink command not supported") {
		t.Fatalf("status/detail = %s/%q, want failed/unsupported", res.Status, res.Detail)
	}
}

func TestAckCapturesDeviceReply(t *testing.T) {
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = 4
	srv := NewServer(cfg, nil, nil)
	defer srv.Shutdown()

	imei := "864201040512345"
	_ = devicePipe(t, srv, imei, models.ProtoGT06)
	g := testGateway(srv)
	srv.gateway = g

	cmd := models.DeviceCommand{
		RequestID: "req-ack", CompanyCode: "DEV001", VehicleID: 1, IMEI: imei,
		Kind: models.CommandEngineCut,
	}
	g.mu.Lock()
	g.pending[cmd.RequestID] = &pendingCommand{cmd: cmd, sentAt: time.Now().UTC(), proto: models.ProtoGT06}
	g.mu.Unlock()

	// The GT06 terminal answers with the content it executed.
	g.Ack(imei, "DYD=Success!")
	if len(g.pending) != 0 {
		t.Fatalf("pending set = %d after the ACK, want 0", len(g.pending))
	}

	// A failing reply must clear the pending command as well (no stuck `sent`).
	g.mu.Lock()
	g.pending["req-fail"] = &pendingCommand{cmd: cmd, sentAt: time.Now().UTC(), proto: models.ProtoGT06}
	g.mu.Unlock()
	g.Ack(imei, "DYD=Unvalued Fix")
	if len(g.pending) != 0 {
		t.Fatalf("pending set = %d after a failing reply, want 0", len(g.pending))
	}

	// An unsolicited reply for an IMEI with no pending command is ignored.
	g.Ack("999999999999999", "DYD=Success!")
}

func TestSweepExpiredMarksPendingAsTimeout(t *testing.T) {
	cfg := &internal.Config{}
	cfg.TCP.MaxConnections = 4
	srv := NewServer(cfg, nil, nil)
	defer srv.Shutdown()

	g := testGateway(srv)
	g.ackTimeout = time.Millisecond
	g.pending["stale"] = &pendingCommand{
		cmd:    models.DeviceCommand{RequestID: "stale", CompanyCode: "DEV001", IMEI: "1", Kind: models.CommandReboot},
		sentAt: time.Now().Add(-time.Minute).UTC(),
	}
	g.sweepExpired()
	if len(g.pending) != 0 {
		t.Fatalf("pending set = %d after the sweep, want 0 (expired)", len(g.pending))
	}
}

func TestCommandTransitionTimes(t *testing.T) {
	// `offline` never reached a device: both timestamps stay NULL.
	if sent, acked := commandTransitionTimes(models.CommandResult{Status: models.CommandStatusOffline}); sent != nil || acked != nil {
		t.Fatalf("offline → sent=%v acked=%v, want nil/nil", sent, acked)
	}
	// `sent`/`timeout` stamped sent_at only.
	for _, st := range []string{models.CommandStatusSent, models.CommandStatusTimeout} {
		sent, acked := commandTransitionTimes(models.CommandResult{Status: st})
		if sent == nil || acked != nil {
			t.Fatalf("%s → sent=%v acked=%v, want sent/nil", st, sent, acked)
		}
	}
	// `acked` stamps both.
	sent, acked := commandTransitionTimes(models.CommandResult{Status: models.CommandStatusAcked, ACK: "DYD=Success!"})
	if sent == nil || acked == nil {
		t.Fatalf("acked → sent=%v acked=%v, want both", sent, acked)
	}
	// A device-reported failure (`failed` + ACK) also stamps both, because the frame
	// did reach the device; a local encode/write failure stamps nothing.
	sent, acked = commandTransitionTimes(models.CommandResult{Status: models.CommandStatusFailed, ACK: "DYD=Unvalued Fix"})
	if sent == nil || acked == nil {
		t.Fatalf("failed-with-ack → sent=%v acked=%v, want both", sent, acked)
	}
	if sent, acked = commandTransitionTimes(models.CommandResult{Status: models.CommandStatusFailed, Detail: "encode: boom"}); sent != nil || acked != nil {
		t.Fatalf("failed-before-device → sent=%v acked=%v, want nil/nil", sent, acked)
	}
}

func TestParseGT06CommandReplyAndExtract(t *testing.T) {
	if ok, detail := parseGT06CommandReply("DYD=Success!"); !ok || detail != "DYD=Success!" {
		t.Fatalf("success reply parsed as %v/%q", ok, detail)
	}
	for _, fail := range []string{"HFYD=Fail!", "DYD=Unvalued Fix", "DYD=Speed Limit, Speed 40km/h", "Command Error!"} {
		if ok, _ := parseGT06CommandReply(fail); ok {
			t.Errorf("%q was interpreted as a success", fail)
		}
	}
	if _, detail := parseGT06CommandReply("   "); detail != "empty device reply" {
		t.Fatalf("empty reply detail = %q", detail)
	}

	// A real 0x21 payload: flag(4) + content code + ASCII content + language +
	// serial; only the printable run matters.
	payload := []byte{0x00, 0x00, 0x00, 0x00, 0x01, 'D', 'Y', 'D', '=', 'S', 'u', 'c', 'c', 'e', 's', 's', '!', 0x00, 0x02, 0x00, 0x07}
	if got := extractCommandReply(payload); got != "DYD=Success!" {
		t.Fatalf("extractCommandReply = %q, want DYD=Success!", got)
	}
}
