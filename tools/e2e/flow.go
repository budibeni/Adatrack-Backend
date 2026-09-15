package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

// runSingle verifies the full pipeline for one device.
func runSingle(ctx context.Context, opt options, nc *nats.Conn, db *sql.DB, rdb *redis.Client) {
	start := time.Now().UTC().Add(-2 * time.Second)
	rawCh := make(chan []byte, 8)
	liveCh := make(chan []byte, 8)

	rawSub, err := nc.Subscribe(subject("raw", opt.imei), func(m *nats.Msg) { push(rawCh, m.Data) })
	if err != nil {
		log.Fatalf("subscribe raw failed: %v", err)
	}
	defer func() { _ = rawSub.Unsubscribe() }()

	liveSub, err := nc.Subscribe(subject("live", opt.imei), func(m *nats.Msg) { push(liveCh, m.Data) })
	if err != nil {
		log.Fatalf("subscribe live failed: %v", err)
	}
	defer func() { _ = liveSub.Unsubscribe() }()
	_ = nc.FlushTimeout(3 * time.Second)

	// Values of the test drive.
	lat, lon, speedKmh := -6.2088, 106.8456, 42.5
	acc := true
	// GT06 carries speed as ONE BYTE of knots, so the transmitted value is
	// quantized: assert the round-trip of that value, not the ideal float.
	speedOnWire := float64(byte(speedKmh/1.852)) * 1.852

	conn := dialDevice(opt.tcpAddr)
	defer func() { _ = conn.Close() }()

	if err := sendLogin(conn, opt.imei); err != nil {
		log.Fatalf("login failed: %v", err)
	}
	if err := sendPosition(conn, lat, lon, speedKmh, acc); err != nil {
		log.Fatalf("position failed: %v", err)
	}

	results := []checkResult{
		checkNATSRaw(ctx, rawCh, opt),
		checkNATSLive(ctx, liveCh, opt),
		checkPostgres(ctx, db, opt, lat, lon, start),
		checkRedis(ctx, rdb, opt, lat, lon, speedOnWire, acc),
		checkTenantIsolation(ctx, opt, opt.imei, start),
	}
	report(results)
}

// dialDevice opens a TCP connection to the GT06 listener.
func dialDevice(addr string) net.Conn {
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Fatalf("cannot dial ingestion-tcp at %s: %v", addr, err)
	}
	return conn
}

// sendLogin performs the GT06 handshake and verifies the accept reply.
func sendLogin(conn net.Conn, imei string) error {
	if _, err := conn.Write(loginFrame(imei)); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	reply, err := readFrame(bufio.NewReader(conn))
	if err != nil {
		return fmt.Errorf("read login reply: %w", err)
	}
	// Login accept: protocol 0x01 with status 0x00.
	if len(reply) < 6 || reply[3] != 0x01 || reply[4] != 0x00 {
		return fmt.Errorf("unexpected login reply % x (expected proto 0x01 status 0x00)", reply)
	}
	return nil
}

// sendPosition writes one 0x22 position frame and drains the ACK.
func sendPosition(conn net.Conn, lat, lon, speed float64, acc bool) error {
	frame := positionFrame(time.Now().UTC(), lat, lon, speed, acc)
	if _, err := conn.Write(frame); err != nil {
		return err
	}
	// The ACK content is not asserted here (the pipeline is), but the reader must
	// drain it so the socket buffer does not fill up.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = readFrame(bufio.NewReader(conn))
	return nil
}
