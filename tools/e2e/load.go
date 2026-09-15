package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// runLoad drives `rate` messages/second across `devices` connections and then
// verifies the acceptance criterion "no data loss": the number of persisted rows
// equals the number of frames the devices successfully wrote.
//
// Only registered IMEIs are accepted by the ingestion allowlist, so the load
// uses the dev fixtures (3 IMEIs) round-robin across the device connections —
// which also exercises shared-device concurrency.
func runLoad(ctx context.Context, opt options, nc *nats.Conn, db *sql.DB, rdb *redis.Client) {
	imeis := loadIMEIs(opt)
	start := time.Now().UTC().Add(-2 * time.Second)

	fmt.Printf("load: rate=%d msg/s devices=%d duration=%s imeis=%v\n",
		opt.rate, opt.devices, opt.duration, imeis)

	perDevice := opt.rate / maxInt(opt.devices, 1)
	if perDevice < 1 {
		perDevice = 1
	}
	interval := time.Second / time.Duration(perDevice)

	var (
		wg       sync.WaitGroup
		writeErr int64
	)
	deadline := time.Now().Add(opt.duration)

	for d := 0; d < opt.devices; d++ {
		wg.Add(1)
		go func(deviceIndex int) {
			defer wg.Done()
			imei := imeis[deviceIndex%len(imeis)]

			conn, err := net.DialTimeout("tcp", opt.tcpAddr, 5*time.Second)
			if err != nil {
				log.Printf("load: device %d dial failed: %v", deviceIndex, err)
				writeErr++
				return
			}
			defer func() { _ = conn.Close() }()

			if _, err := conn.Write(loginFrame(imei)); err != nil {
				writeErr++
				return
			}
			reader := bufio.NewReader(conn)
			_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := readFrame(reader); err != nil {
				log.Printf("load: device %d login failed: %v", deviceIndex, err)
				writeErr++
				return
			}
			// Keep draining ACKs in the background so the socket never blocks.
			go drain(conn, reader)

			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for now := time.Now(); now.Before(deadline); now = time.Now() {
				<-ticker.C
				frame := positionFrame(time.Now().UTC(), -6.2088, 106.8456, 40+float64(deviceIndex%20), true)
				if _, err := conn.Write(frame); err != nil {
					writeErr++
					return
				}
				sentCount.Add(1)
			}
		}(d)
	}

	wg.Wait()
	sent := sentCount.Load()
	fmt.Printf("load: sent=%d frames (write errors=%d); waiting for persistence to settle...\n", sent, writeErr)

	// Allow the persistence batch timeout plus a margin for the final flush.
	settle := opt.timeout
	if settle < 15*time.Second {
		settle = 15 * time.Second
	}
	time.Sleep(settle)

	ctxCount, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	persisted, err := countTelemetrySinceAll(ctxCount, db, imeis, start)
	if err != nil {
		log.Fatalf("load: counting persisted rows failed: %v", err)
	}

	elapsed := opt.duration.Seconds()
	results := []checkResult{
		{
			Name:   "load.throughput",
			Detail: fmt.Sprintf("sent=%d in %.1fs (~%.0f msg/s), write_errors=%d", sent, elapsed, float64(sent)/elapsed, writeErr),
		},
		{
			Name:   "load.no_data_loss",
			Detail: fmt.Sprintf("sent=%d persisted=%d", sent, persisted),
			Err:    noLossError(sent, persisted),
		},
	}

	// Live state must exist for every IMEI that was used.
	for _, imei := range imeis {
		key := liveStateKey(opt.keyPrefix, opt.company, imei)
		_, lerr := waitForLiveState(ctx, rdb, key, 5*time.Second)
		results = append(results, checkResult{
			Name:   "load.live_state",
			Detail: key,
			Err:    lerr,
		})
	}

	report(results)
}

// noLossError compares sent vs persisted rows (the acceptance criterion).
func noLossError(sent, persisted int64) error {
	if persisted >= sent {
		return nil
	}
	return fmt.Errorf("data loss detected: %d of %d frames were not persisted", sent-persisted, sent)
}

// loadIMEIs returns the registered dev IMEIs (env E2E_IMEIS can override).
func loadIMEIs(opt options) []string {
	if raw := envOr("E2E_IMEIS", ""); raw != "" {
		out := []string{}
		for _, part := range splitComma(raw) {
			if part != "" {
				out = append(out, part)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"864201040512345", "864201040512346", "864201040512347"}
}

// splitComma splits a comma separated list (whitespace trimmed).
func splitComma(s string) []string {
	out := []string{}
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, trimSpace(cur))
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, trimSpace(cur))
	return out
}

// trimSpace trims ASCII whitespace.
func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 {
		last := s[len(s)-1]
		if last != ' ' && last != '\t' {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}

// maxInt returns the larger of two ints.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// drain consumes server ACKs so the device socket never stalls.
func drain(conn net.Conn, r *bufio.Reader) {
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		if _, err := readFrame(r); err != nil {
			return
		}
	}
}

// countTelemetrySinceAll counts persisted rows for several IMEIs at/after `since`.
func countTelemetrySinceAll(ctx context.Context, db *sql.DB, imeis []string, since time.Time) (int64, error) {
	var total int64
	for _, imei := range imeis {
		n, err := countTelemetrySince(ctx, db, imei, since)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}
