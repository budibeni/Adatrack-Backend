package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"time"
)

// crcITU computes the GT06 CRC-ITU (CRC-16/X-25) bitwise — deliberately
// independent of the ingestion implementation so a bug in one path cannot mask
// a bug in the other (same rationale as tools/e2e).
func crcITU(data []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// buildFrame assembles start || length || proto || content || crc(2) || stop.
func buildFrame(proto byte, content []byte) []byte {
	body := append([]byte{proto}, content...)
	frame := []byte{0x78, 0x78, byte(1 + len(content))}
	frame = append(frame, body...)
	sum := crcITU(frame[2:])
	return append(frame, byte(sum>>8), byte(sum), 0x0D, 0x0A)
}

// loginFrame builds the GT06 login packet for a 15-digit IMEI.
func loginFrame(imei string) []byte {
	content := make([]byte, 15, 17)
	copy(content, []byte(imei))
	content = append(content, 0x00, 0x01)
	return buildFrame(0x01, content)
}

// positionFrame builds a 0x22 position packet (needed so worker-live keeps the
// position while the fuel-only packets merge into the same live-state key).
func positionFrame(t time.Time, lat, lon, speedKmh float64, acc bool) []byte {
	content := []byte{
		byte(t.Year() - 2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()), 0x0A,
	}
	content = appendUint32(content, uint32(math.Abs(lat)*1800000))
	content = appendUint32(content, uint32(math.Abs(lon)*1800000))
	content = append(content, byte(speedKmh/1.852))

	status := uint16(0x1000)
	if lat >= 0 {
		status |= 0x0400
	}
	if lon < 0 {
		status |= 0x0800
	}
	content = append(content, byte(status>>8), byte(status))
	content = append(content, 0x01, 0xF4, 0x01, 0x00, 0x01, 0x00, 0x01, 0x23)
	if acc {
		content = append(content, 0x01)
	} else {
		content = append(content, 0x00)
	}
	content = append(content, 0x00, 0x00)
	content = appendUint32(content, 0)
	return buildFrame(0x22, content)
}

// fuelFrame builds the v3.1 fuel-sensor packet: 0x94 (information transmission)
// with subtype 0x0D, the 6-byte time block, the ASCII `!AIOIL` sentence and the
// 2-byte serial. The sentence carries centimetres, exactly like the device doc
// example (`!AIOIL,02,025.900,025.400,519J,0200,027.140,0,00,9F`).
func fuelFrame(t time.Time, heightCM, tempC float64) []byte {
	content := []byte{
		0x0D,
		byte(t.Year() - 2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()),
	}
	sentence := fmt.Sprintf("!AIOIL,02,%07.3f,%06.3f,519J,0200,027.140,0,00,9F", heightCM, tempC)
	content = append(content, sentence...)
	content = append(content, 0x00, 0x01) // information serial number
	return buildFrame(0x94, content)
}

// appendUint32 appends a big-endian uint32.
func appendUint32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}

// readFrame reads one framed reply (mainly to verify the server ACKs).
func readFrame(r *bufio.Reader) ([]byte, error) {
	start, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	second, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if start != 0x78 || second != 0x78 {
		return nil, fmt.Errorf("unexpected start bytes 0x%02x 0x%02x", start, second)
	}
	lengthByte, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	rest := make([]byte, int(lengthByte)+4)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, err
	}
	return append([]byte{start, second, lengthByte}, rest...), nil
}

// driveFuel runs the device sequence: login → position → fuel(high) → fuel(low).
// It returns once both fuel packets have been written (the pipeline assertions
// follow asynchronously).
func driveFuel(opt options) error {
	conn, err := net.DialTimeout("tcp", opt.tcpAddr, opt.timeout)
	if err != nil {
		return fmt.Errorf("dial ingestion-tcp %s: %w", opt.tcpAddr, err)
	}
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)

	if _, err := conn.Write(loginFrame(opt.imei)); err != nil {
		return fmt.Errorf("write login: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(opt.timeout))
	reply, err := readFrame(reader)
	if err != nil {
		return fmt.Errorf("read login reply: %w", err)
	}
	if len(reply) < 6 || reply[3] != 0x01 || reply[4] != 0x00 {
		return fmt.Errorf("login rejected: % x", reply)
	}

	if _, err := conn.Write(positionFrame(time.Now().UTC(), -6.2088, 106.8456, 20, true)); err != nil {
		return fmt.Errorf("write position: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = readFrame(reader)

	// Baseline reading, then the drop. The detector evaluates the delta over the
	// readings ALREADY in its sliding window (the current reading itself only
	// triggers the evaluation — see worker-alert `detFuel` + alert_fuel_test.go),
	// so the sequence is: high → low → settle(low). The settle frame is what
	// makes the alert deterministic on a cold service start.
	if _, err := conn.Write(fuelFrame(time.Now().UTC(), opt.high, 25.4)); err != nil {
		return fmt.Errorf("write fuel(high): %w", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := conn.Write(fuelFrame(time.Now().UTC(), opt.low, 25.6)); err != nil {
		return fmt.Errorf("write fuel(low): %w", err)
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := conn.Write(fuelFrame(time.Now().UTC(), opt.low, 25.6)); err != nil {
		return fmt.Errorf("write fuel(settle): %w", err)
	}
	// Give ingestion a moment to read the last frame before the socket closes
	// (an immediate close can race the final write on some stacks).
	time.Sleep(500 * time.Millisecond)
	return nil
}
