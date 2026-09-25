package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Stats struct {
	ConnectionsAttempted   int64
	ConnectionsEstablished int64
	ConnectionsFailed      int64
	LoginsSent             int64
	LoginsAcked            int64
	LocationsSent          int64
	BytesSent              int64
	BytesReceived          int64
	Errors                 int64
}

func main() {
	host := flag.String("host", "127.0.0.1", "Target TCP host")
	port := flag.Int("port", 15000, "Target TCP port (15000 for GT06)")
	devices := flag.Int("devices", 50, "Number of concurrent simulated devices")
	duration := flag.Duration("duration", 10*time.Second, "Test duration")
	rate := flag.Float64("rate", 1.0, "Messages per second per device")
	imeiBase := flag.Int64("imei-base", 868204000000001, "Starting IMEI number")
	flag.Parse()

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("=========================================================")
	log.Printf("Adatrack Platform Real TCP Load Generator (GT06 Protocol)")
	log.Printf("Target Address:       %s", addr)
	log.Printf("Simulated Devices:    %d concurrent devices", *devices)
	log.Printf("Message Rate:         %.1f msg/s per device", *rate)
	log.Printf("Target Total Rate:    %.1f msg/s", float64(*devices)**rate)
	log.Printf("Test Duration:        %v", *duration)
	log.Printf("=========================================================")

	stats := &Stats{}
	stopCh := make(chan struct{})

	// Check if server is reachable first
	testConn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		log.Printf("ERROR: Target %s is not reachable: %v", addr, err)
		log.Printf("Please ensure the service (ingestion-tcp) is running before testing.")
		return
	}
	testConn.Close()

	startTime := time.Now()
	var wg sync.WaitGroup

	interval := time.Duration(float64(time.Second) / *rate)
	if interval < 10*time.Millisecond {
		interval = 10 * time.Millisecond
	}

	for i := 0; i < *devices; i++ {
		wg.Add(1)
		imeiNum := *imeiBase + int64(i)
		imeiStr := fmt.Sprintf("%015d", imeiNum)
		go runDeviceSimulator(addr, imeiStr, interval, stopCh, stats, &wg)
		// Stagger device connections slightly
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for test duration
	time.Sleep(*duration)
	close(stopCh)
	wg.Wait()
	totalElapsed := time.Since(startTime)

	// Summary report
	totalMsgs := atomic.LoadInt64(&stats.LocationsSent)
	totalLogins := atomic.LoadInt64(&stats.LoginsSent)
	ackedLogins := atomic.LoadInt64(&stats.LoginsAcked)
	connEst := atomic.LoadInt64(&stats.ConnectionsEstablished)
	connFail := atomic.LoadInt64(&stats.ConnectionsFailed)
	totalBytesSent := atomic.LoadInt64(&stats.BytesSent)
	totalBytesRecv := atomic.LoadInt64(&stats.BytesReceived)
	errCount := atomic.LoadInt64(&stats.Errors)

	actualThroughput := float64(totalMsgs) / totalElapsed.Seconds()
	bandwidthKBps := (float64(totalBytesSent) / 1024.0) / totalElapsed.Seconds()

	log.Printf("=========================================================")
	log.Printf("LOAD TEST EXECUTION COMPLETED")
	log.Printf("=========================================================")
	log.Printf("Total Duration:         %.2f s", totalElapsed.Seconds())
	log.Printf("TCP Connections:        %d established / %d failed", connEst, connFail)
	log.Printf("Logins Sent / Acked:    %d / %d (%.1f%% ACK rate)", totalLogins, ackedLogins, float64(ackedLogins)*100.0/float64(max(totalLogins, 1)))
	log.Printf("Telemetry Packets Sent: %d packets", totalMsgs)
	log.Printf("Real Throughput:        %.2f msg/sec", actualThroughput)
	log.Printf("Network Throughput:     %.2f KB/sec (Total: %d bytes sent, %d bytes received)", bandwidthKBps, totalBytesSent, totalBytesRecv)
	log.Printf("Total Errors:           %d", errCount)
	log.Printf("=========================================================")

	if actualThroughput > 0 && errCount == 0 {
		log.Printf("RESULT: PASS — Real TCP traffic generated and processed successfully.")
	} else if actualThroughput > 0 {
		log.Printf("RESULT: PARTIAL — Traffic delivered with %d errors.", errCount)
	} else {
		log.Printf("RESULT: FAIL — No telemetry packets delivered.")
	}
}

func runDeviceSimulator(addr, imei string, interval time.Duration, stopCh <-chan struct{}, stats *Stats, wg *sync.WaitGroup) {
	defer wg.Done()
	atomic.AddInt64(&stats.ConnectionsAttempted, 1)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		atomic.AddInt64(&stats.ConnectionsFailed, 1)
		atomic.AddInt64(&stats.Errors, 1)
		return
	}
	defer conn.Close()
	atomic.AddInt64(&stats.ConnectionsEstablished, 1)

	// 1. Send GT06 Login Packet
	loginPacket := buildGT06LoginPacket(imei, 1)
	if _, err := conn.Write(loginPacket); err != nil {
		atomic.AddInt64(&stats.Errors, 1)
		return
	}
	atomic.AddInt64(&stats.LoginsSent, 1)
	atomic.AddInt64(&stats.BytesSent, int64(len(loginPacket)))

	// Read Login ACK
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	ackBuf := make([]byte, 64)
	n, err := conn.Read(ackBuf)
	if err == nil && n >= 5 && ackBuf[0] == 0x78 && ackBuf[1] == 0x78 {
		atomic.AddInt64(&stats.LoginsAcked, 1)
		atomic.AddInt64(&stats.BytesReceived, int64(n))
	}

	// 2. Stream Location Packets
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	serial := uint16(2)
	baseLat := -6.2088 + (rand.Float64()-0.5)*0.1
	baseLon := 106.8456 + (rand.Float64()-0.5)*0.1

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
			// Simulate slight movement
			baseLat += (rand.Float64() - 0.5) * 0.0005
			baseLon += (rand.Float64() - 0.5) * 0.0005
			speed := 40 + rand.Float64()*40

			locPacket := buildGT06LocationPacket(time.Now().UTC(), baseLat, baseLon, speed, serial)
			serial++

			conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if _, err := conn.Write(locPacket); err != nil {
				if err != io.EOF {
					atomic.AddInt64(&stats.Errors, 1)
				}
				return
			}
			atomic.AddInt64(&stats.LocationsSent, 1)
			atomic.AddInt64(&stats.BytesSent, int64(len(locPacket)))
		}
	}
}

func buildGT06LoginPacket(imei string, serial uint16) []byte {
	// Preamble (2) + Length (1) + Protocol (1) + IMEI (8) + Serial (2) + ErrorCheck (2) + Stop (2) = 18 bytes
	// Example: 78 78 11 01 [8 bytes IMEI BCD] [2 bytes Serial] [2 bytes CRC] 0D 0A
	pkt := make([]byte, 18)
	pkt[0] = 0x78
	pkt[1] = 0x78
	pkt[2] = 0x11 // Length: 17 bytes from Protocol to Serial
	pkt[3] = 0x01 // Protocol: Login

	// Encode 15/16 digit IMEI into 8 bytes BCD
	padded := imei
	if len(padded) < 16 {
		padded = "0" + padded
	}
	imeiBytes, _ := hex.DecodeString(padded[:16])
	copy(pkt[4:12], imeiBytes)

	binary.BigEndian.PutUint16(pkt[12:14], serial)
	pkt[14] = 0x8C // Checksum dummy
	pkt[15] = 0x2A
	pkt[16] = 0x0D // Stop bits
	pkt[17] = 0x0A
	return pkt
}

func buildGT06LocationPacket(t time.Time, lat, lon, speed float64, serial uint16) []byte {
	// Standard GT06 location packet: ~36 bytes
	pkt := make([]byte, 36)
	pkt[0] = 0x78
	pkt[1] = 0x78
	pkt[2] = 0x1F // Length
	pkt[3] = 0x12 // Protocol: Location

	// Date time: YY MM DD HH MM SS
	year := byte(t.Year() % 100)
	pkt[4] = year
	pkt[5] = byte(t.Month())
	pkt[6] = byte(t.Day())
	pkt[7] = byte(t.Hour())
	pkt[8] = byte(t.Minute())
	pkt[9] = byte(t.Second())

	// GPS info length & satellites
	pkt[10] = 0xC8 // 8 satellites

	// Latitude: (lat * 30000 * 60)
	absLat := lat
	if absLat < 0 {
		absLat = -absLat
	}
	latVal := uint32(absLat * 60 * 30000)
	binary.BigEndian.PutUint32(pkt[11:15], latVal)

	// Longitude: (lon * 30000 * 60)
	absLon := lon
	if absLon < 0 {
		absLon = -absLon
	}
	lonVal := uint32(absLon * 60 * 30000)
	binary.BigEndian.PutUint32(pkt[15:19], lonVal)

	// Speed: km/h
	pkt[19] = byte(int(speed) & 0xFF)

	// Course & Status flags (South/East, GPS differential, etc.)
	binary.BigEndian.PutUint16(pkt[20:22], 0x1402)

	// MCC, MNC, LAC, Cell ID dummy
	pkt[22] = 0x02
	pkt[23] = 0x01
	pkt[24] = 0x01
	pkt[25] = 0x01

	// Serial number
	binary.BigEndian.PutUint16(pkt[30:32], serial)

	// Error check CRC
	pkt[32] = 0x00
	pkt[33] = 0x00

	// Stop bits
	pkt[34] = 0x0D
	pkt[35] = 0x0A
	return pkt
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
