// Package main — GT06 frame encoder + device driver for the B2 harness.
//
// The encoder is intentionally INDEPENDENT of the ingestion service
// implementation (it computes CRC-ITU bitwise, the service uses a precomputed
// table), so a bug in one path cannot mask a bug in the other.
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

// crcITU computes the GT06 CRC-ITU (CRC-16/X-25: init 0xFFFF, reflected poly
// 0x8408, final XOR 0xFFFF) bitwise.
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

// buildFrame assembles a GT06 terminal frame: start || length || proto ||
// content || crc(2) || stop.
func buildFrame(proto byte, content []byte) []byte {
	body := make([]byte, 0, 2+len(content))
	body = append(body, proto)
	body = append(body, content...)

	frame := make([]byte, 0, len(body)+8)
	frame = append(frame, 0x78, 0x78, byte(1+len(content)))
	frame = append(frame, body...)
	sum := crcITU(frame[2:])
	frame = append(frame, byte(sum>>8), byte(sum), 0x0D, 0x0A)
	return frame
}

// loginFrame builds a GT06 login packet for a 15-digit IMEI.
func loginFrame(imei string) []byte {
	content := make([]byte, 15, 17)
	copy(content, []byte(imei))
	content = append(content, 0x00, 0x01) // information serial number
	return buildFrame(0x01, content)
}

// positionFrame builds a 0x22 position packet with a valid GPS fix.
func positionFrame(t time.Time, lat, lon, speedKmh float64, acc bool) []byte {
	content := make([]byte, 0, 33)
	content = append(content,
		byte(t.Year()-2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
	content = append(content, 0x0A) // 10 satellites

	content = appendUint32(content, uint32(math.Abs(lat)*1800000))
	content = appendUint32(content, uint32(math.Abs(lon)*1800000))
	content = append(content, byte(speedKmh/1.852)) // speed is transmitted in knots

	status := uint16(0x1000) // GPS positioned
	if lat >= 0 {
		status |= 0x0400 // North
	}
	if lon < 0 {
		status |= 0x0800 // West
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

// appendUint32 appends a big-endian uint32.
func appendUint32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}

// readFrame reads one framed reply from the device socket.
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

// drive writes one device frame set (login + position) through ingestion-tcp,
// i.e. the real "from ingest" path the WS latency criterion refers to.
//
// lat/lon/speed differ per call so the harness never asserts on a stale state.
func drive(addr, imei string, lat, lon, speed float64, acc bool, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("dial ingestion-tcp %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write(loginFrame(imei)); err != nil {
		return fmt.Errorf("write login frame: %w", err)
	}
	reader := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	reply, err := readFrame(reader)
	if err != nil {
		return fmt.Errorf("read login reply: %w", err)
	}
	if len(reply) < 6 || reply[3] != 0x01 || reply[4] != 0x00 {
		return fmt.Errorf("unexpected login reply % x", reply)
	}

	if _, err := conn.Write(positionFrame(time.Now().UTC(), lat, lon, speed, acc)); err != nil {
		return fmt.Errorf("write position frame: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	_, _ = readFrame(reader) // drain the ACK
	return nil
}
