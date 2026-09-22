// Package main — WebSocket fan-out load profile (PRD §16 "WS load 50×1200"),
// the B4 performance item that was still outstanding.
//
// It drives the REAL path (device frame → ingestion-tcp → NATS → worker-live →
// service-websocket → N subscribers) and asserts the platform-wide "0 loss"
// rule from three independent angles:
//
// client side : every subscriber receives exactly the published count
// server side : ws_message_sent_total grows by clients × published
// drops       : ws_message_dropped_total does not move (drop-oldest, FR-5.4)
//
// The last published frame is a MARKER: only one frame is in flight when it is
// written, so each subscriber attributes its own end-to-end latency to it
// without needing a sequence number inside the GT06 payload.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// metricSample is one Prometheus text-format sample.
type metricSample struct {
	name   string
	labels map[string]string
	value  float64
}

// scrapeMetrics fetches /metrics from the service under test.
func scrapeMetrics(base string, timeout time.Duration) ([]metricSample, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(strings.TrimRight(base, "/") + "/metrics")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /metrics: %s", resp.Status)
	}
	samples, err := parseMetrics(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, fmt.Errorf("GET /metrics returned no samples")
	}
	return samples, nil
}

// parseMetrics reads the Prometheus text exposition format, skipping comments and
// any line it cannot parse (an unrelated series must never break the run).
func parseMetrics(r io.Reader) ([]metricSample, error) {
	var out []metricSample
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, rest, err := splitSample(line)
		if err != nil {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		out = append(out, metricSample{name: name, labels: labels, value: value})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// splitSample splits `name{labels} value` into name, labels and the remainder.
func splitSample(line string) (string, map[string]string, string, error) {
	open := strings.IndexByte(line, '{')
	if open < 0 {
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			return "", nil, "", fmt.Errorf("malformed sample %q", line)
		}
		return line[:sp], nil, line[sp+1:], nil
	}
	rel := strings.IndexByte(line[open:], '}')
	if rel < 0 {
		return "", nil, "", fmt.Errorf("unterminated labels in %q", line)
	}
	closeIdx := open + rel
	labels := map[string]string{}
	for _, pair := range splitLabels(line[open+1 : closeIdx]) {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		labels[strings.TrimSpace(kv[0])] = strings.Trim(strings.TrimSpace(kv[1]), "\"")
	}
	return line[:open], labels, line[closeIdx+1:], nil
}

// splitLabels splits a label list on commas outside quoted values.
func splitLabels(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case r == ',' && !inQuotes:
			parts = append(parts, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		parts = append(parts, cur.String())
	}
	return parts
}

// counterSum sums every series of a counter, optionally filtered by one label.
// An empty labelKey sums the whole metric family.
func counterSum(samples []metricSample, name, labelKey, labelValue string) float64 {
	var total float64
	for _, s := range samples {
		if s.name != name {
			continue
		}
		if labelKey != "" && s.labels[labelKey] != labelValue {
			continue
		}
		total += s.value
	}
	return total
}

// gaugeValue returns the first series of a gauge.
func gaugeValue(samples []metricSample, name string) (float64, bool) {
	for _, s := range samples {
		if s.name == name {
			return s.value, true
		}
	}
	return 0, false
}

// wsSubscriber is one load-test client.
type wsSubscriber struct {
	connected  bool
	subscribed bool
	received   int
	lastAt     time.Time
	err        error
}

// wsLoadPlan is the resolved profile (clients × messages at a target rate).
type wsLoadPlan struct {
	clients  int
	messages int
	rate     int
}

// published is the number of device frames the publisher must deliver: the load
// messages plus the single latency marker.
func (p wsLoadPlan) published() int { return p.messages + 1 }

// runWSLoad executes the profile and reports every assertion in one check.
func runWSLoad(ctx context.Context, opt options, admin *session, vehicleID int64) checkResult {
	plan := wsLoadPlan{clients: opt.wsClients, messages: opt.wsMessages, rate: opt.wsRate}
	name := fmt.Sprintf("ws.load_%dx%d", plan.clients, plan.messages)

	if plan.clients <= 0 || plan.messages <= 0 || plan.rate <= 0 {
		return fail(name, "invalid profile (clients/messages/rate must be > 0)", nil)
	}

	before, err := scrapeMetrics(opt.baseURL, opt.timeout)
	if err != nil {
		return fail(name, "cannot scrape /metrics before the run", err)
	}
	// Baselines are read here (not after the run) because the leak check below
	// needs them while it is still measuring the settle.
	gBefore, _ := gaugeValue(before, "adatrack_goroutines")
	mBefore, _ := gaugeValue(before, "adatrack_memory_allocated_bytes")

	subs := make([]*wsSubscriber, plan.clients)
	conns := make([]*websocket.Conn, plan.clients)

	// Phase 1 — connect + subscribe, sequential on purpose: 50 handshakes cost
	// milliseconds and it keeps the readiness gate race-free (a WaitGroup that
	// only completes after the readers finish would deadlock: the readers wait
	// for the publisher, which waits for the WaitGroup).
	for i := 0; i < plan.clients; i++ {
		sub := &wsSubscriber{}
		subs[i] = sub

		conn, _, derr := dialWS(opt, admin.AccessToken, opt.timeout)
		if derr != nil {
			sub.err = fmt.Errorf("dial: %w", derr)
			break
		}
		conns[i] = conn
		sub.connected = true

		if _, serr := wsSubscribe(conn, []int64{vehicleID}, opt.timeout); serr != nil {
			sub.err = fmt.Errorf("subscribe: %w", serr)
			break
		}
		sub.subscribed = true
	}
	if !allReady(subs) {
		for _, c := range conns {
			if c != nil {
				_ = c.Close()
			}
		}
		return fail(name, fmt.Sprintf("%d/%d subscribers connected and subscribed",
			readyCount(subs), plan.clients), firstErr(subs))
	}

	// Phase 2 — readers count frames while the publisher drives the device path.
	// Each reader reports through a channel so no counter is touched by two
	// goroutines at once.
	type outcome struct {
		idx      int
		received int
		lastAt   time.Time
		err      error
	}
	results := make(chan outcome, plan.clients)
	for i := 0; i < plan.clients; i++ {
		go func(i int) {
			var received int
			var lastAt time.Time
			err := drainUpdates(conns[i], plan.published(), &received, &lastAt, ctx)
			results <- outcome{idx: i, received: received, lastAt: lastAt, err: err}
		}(i)
	}

	sent, publishErr, markerAt, elapsed := publishFrames(ctx, opt, plan)

	// Collect every outcome (bounded): the fan-out is complete once all readers
	// report. The channel is buffered, so a straggler never blocks forever.
	remaining := plan.clients
	grace := time.After(45 * time.Second)
	for remaining > 0 {
		select {
		case o := <-results:
			sub := subs[o.idx]
			sub.received = o.received
			sub.lastAt = o.lastAt
			if o.err != nil && sub.err == nil {
				sub.err = o.err
			}
			remaining--
		case <-grace:
			remaining = 0
		case <-ctx.Done():
			remaining = 0
		}
	}
	for _, c := range conns {
		if c != nil {
			_ = c.Close()
		}
	}
	// 50 freshly closed connections briefly keep their read/write pumps alive, so
	// sample immediately (transient) and then wait for the count to settle: only a
	// count that STAYS elevated is a leak (FR-4.4).
	gTransient := 0.0
	if tr, terr := scrapeMetrics(opt.baseURL, opt.timeout); terr == nil {
		gTransient, _ = gaugeValue(tr, "adatrack_goroutines")
	}
	gSettled := settleGoroutines(opt.baseURL, 10*time.Second, opt.timeout)

	after, aerr := scrapeMetrics(opt.baseURL, opt.timeout)
	if aerr != nil {
		return fail(name, "cannot scrape /metrics after the run", aerr)
	}

	// --- client-side accounting -------------------------------------------
	minRecv, maxRecv, short, latency := plan.published(), 0, 0, make([]float64, 0, plan.clients)
	for _, sub := range subs {
		if sub.received < minRecv {
			minRecv = sub.received
		}
		if sub.received > maxRecv {
			maxRecv = sub.received
		}
		if sub.received != plan.published() {
			short++
			continue
		}
		if !sub.lastAt.IsZero() {
			latency = append(latency, float64(sub.lastAt.Sub(markerAt).Milliseconds()))
		}
	}

	// --- server-side accounting -------------------------------------------
	sentDelta := counterSum(after, "ws_message_sent_total", "event", "VEHICLE_UPDATE") -
		counterSum(before, "ws_message_sent_total", "event", "VEHICLE_UPDATE")
	dropDelta := counterSum(after, "ws_message_dropped_total", "", "") -
		counterSum(before, "ws_message_dropped_total", "", "")
	expectedDeliveries := float64(plan.clients * plan.published())

	mAfter, _ := gaugeValue(after, "adatrack_memory_allocated_bytes")
	subsAfter, _ := gaugeValue(after, "ws_subscriptions_active")
	connsAfter, _ := gaugeValue(after, "ws_connections_active")

	detail := fmt.Sprintf(
		"clients=%d published=%d (load %d + marker, ~%.0f msg/s in %.1fs) recv[%d..%d] "+
			"server_sent_delta=%.0f (want %.0f) drops_delta=%.0f conns_after=%.0f subs_after=%.0f "+
			"latency p50=%.0fms p95=%.0fms max=%.0fms goroutines %.0f->%.0f(transien)->%.0f(settle) heap %.2f->%.2f MB",
		plan.clients, plan.published(), plan.messages,
		float64(sent)/elapsed.Seconds(), elapsed.Seconds(),
		minRecv, maxRecv, sentDelta, expectedDeliveries, dropDelta, connsAfter, subsAfter,
		percentile(latency, 50), percentile(latency, 95), percentile(latency, 100),
		gBefore, gTransient, gSettled, mBefore/1048576, mAfter/1048576)

	switch {
	case publishErr != nil:
		return fail(name, detail+" | publisher stopped early", publishErr)
	case sent != plan.published():
		return fail(name, fmt.Sprintf("%s | publisher sent %d/%d", detail, sent, plan.published()), nil)
	case short > 0:
		return fail(name, fmt.Sprintf("%s | %d subscriber(s) missed frames", detail, short), nil)
	case dropDelta != 0:
		return fail(name, detail+" | subscriber queues dropped messages", nil)
	case connsAfter != 0 || subsAfter != 0:
		// Direct cleanup evidence: the hub must release every connection and
		// subscription, otherwise FR-5.4's drop-oldest backpressure would keep
		// feeding queues nobody reads.
		return fail(name, detail+" | connections/subscriptions not released", nil)
	case sentDelta != expectedDeliveries:
		return fail(name, detail+" | server-side sent count mismatch", nil)
	case percentile(latency, 95) >= 1000:
		return fail(name, detail+" | p95 latency >= 1 s", nil)
	case mAfter > mBefore*4+64*1048576:
		return fail(name, detail+" | heap did not plateau", nil)
	case gSettled > 0 && gSettled > gBefore+15:
		return fail(name, detail+" | goroutine growth suggests a leak", nil)
	}
	return pass(name, detail)
}

// drainUpdates counts VEHICLE_UPDATE frames until want arrive or ctx ends.
func drainUpdates(conn *websocket.Conn, want int, received *int, lastAt *time.Time, ctx context.Context) error {
	for *received < want {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context ended after %d/%d frames: %w", *received, want, err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return err
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read after %d/%d frames: %w", *received, want, err)
		}
		var envelope wsEnvelope
		if uerr := json.Unmarshal(raw, &envelope); uerr != nil {
			continue // not an envelope (should not happen) — keep counting
		}
		if !strings.EqualFold(envelope.Event, "VEHICLE_UPDATE") {
			continue // heartbeats / acks may interleave
		}
		*received++
		*lastAt = time.Now()
	}
	return nil
}

// publishFrames pushes the load frames at the target rate, then the marker.
// The returned duration covers the whole publish phase (effective rate).
func publishFrames(ctx context.Context, opt options, plan wsLoadPlan) (int, error, time.Time, time.Duration) {
	start := time.Now()

	// One device socket for the whole phase (a real tracker streams on one
	// connection; re-logging in per frame is what capped the rate before).
	device, derr := dialDevice(opt.tcpAddr, opt.timeout)
	if derr != nil {
		return 0, derr, time.Time{}, time.Since(start)
	}
	defer device.close()
	if lerr := device.login(opt.imei, opt.timeout); lerr != nil {
		return 0, lerr, time.Time{}, time.Since(start)
	}

	ticker := time.NewTicker(time.Second / time.Duration(plan.rate))
	defer ticker.Stop()

	sent := 0
	for i := 0; i < plan.messages; i++ {
		select {
		case <-ctx.Done():
			return sent, ctx.Err(), time.Time{}, time.Since(start)
		case <-ticker.C:
		}
		// lat/lon move along a small span so every frame is a real position
		// update (the pipeline must not dedupe identical frames).
		lat := -6.2000 + float64(i%5000)/100000
		if err := device.send(time.Now().UTC(), lat, 106.8000, 44.4, true, opt.timeout); err != nil {
			return sent, fmt.Errorf("frame %d: %w", i, err), time.Time{}, time.Since(start)
		}
		sent++
	}

	// The marker is published alone (after a quiet period) so each subscriber can
	// attribute its own end-to-end latency to exactly one frame.
	time.Sleep(1500 * time.Millisecond)
	markerAt := time.Now()
	if err := device.send(time.Now().UTC(), -6.2500, 106.8500, 66.6, true, opt.timeout); err != nil {
		return sent, fmt.Errorf("marker frame: %w", err), time.Time{}, time.Since(start)
	}
	sent++
	return sent, nil, markerAt, time.Since(start)
}

// percentile returns the p-th percentile of a latency slice in milliseconds
// (p=100 returns the maximum).
func percentile(values []float64, p int) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	idx := (len(sorted)*p + 99) / 100
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

// allReady reports whether every subscriber connected and subscribed.
func allReady(subs []*wsSubscriber) bool {
	for _, s := range subs {
		if !s.connected || !s.subscribed {
			return false
		}
	}
	return true
}

// readyCount counts the subscribers that connected and subscribed.
func readyCount(subs []*wsSubscriber) int {
	n := 0
	for _, s := range subs {
		if s.connected && s.subscribed {
			n++
		}
	}
	return n
}

// firstErr returns the first subscriber error (used for the failure detail).
func firstErr(subs []*wsSubscriber) error {
	for _, s := range subs {
		if s.err != nil {
			return s.err
		}
	}
	return nil
}

// deviceConn is one long-lived GT06 device connection. Real trackers stream
// continuously over a single socket; reconnecting per frame pays a login
// round-trip every time, which capped the publish rate well below the profile
// target (~20 frames/s instead of the requested 50).
type deviceConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

// dialDevice opens the device socket with Nagle disabled (frames are tiny; a
// delayed ACK would add ~40 ms per frame).
func dialDevice(addr string, timeout time.Duration) (*deviceConn, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial ingestion-tcp %s: %w", addr, err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	return &deviceConn{conn: conn, reader: bufio.NewReader(conn)}, nil
}

// close releases the device socket.
func (d *deviceConn) close() { _ = d.conn.Close() }

// login performs the GT06 login handshake and validates the reply.
func (d *deviceConn) login(imei string, timeout time.Duration) error {
	if _, err := d.conn.Write(loginFrame(imei)); err != nil {
		return fmt.Errorf("write login frame: %w", err)
	}
	if err := d.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	reply, err := readFrame(d.reader)
	if err != nil {
		return fmt.Errorf("read login reply: %w", err)
	}
	if len(reply) < 6 || reply[3] != 0x01 || reply[4] != 0x00 {
		return fmt.Errorf("unexpected login reply % x", reply)
	}
	return nil
}

// send writes one position frame and drains its ACK so the socket never backs
// up. A missing ACK is tolerated (it is not what this profile measures).
func (d *deviceConn) send(t time.Time, lat, lon, speed float64, acc bool, timeout time.Duration) error {
	if _, err := d.conn.Write(positionFrame(t, lat, lon, speed, acc)); err != nil {
		return fmt.Errorf("write position frame: %w", err)
	}
	if err := d.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	_, _ = readFrame(d.reader)
	return nil
}

// settleGoroutines samples adatrack_goroutines over a fixed window and returns
// the LOWEST value seen. Closing N connections leaves N read/write pumps plus
// the harness' idle HTTP keep-alive handlers alive for a while, so the sample
// right after a load run is expected to be high; a leak shows up as a minimum
// that never comes back down (FR-4.4).
func settleGoroutines(base string, window, scrape time.Duration) float64 {
	deadline := time.Now().Add(window)
	best := 0.0
	for time.Now().Before(deadline) {
		samples, err := scrapeMetrics(base, scrape)
		if err == nil {
			if v, ok := gaugeValue(samples, "adatrack_goroutines"); ok {
				if best == 0 || v < best {
					best = v
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return best
}
