// Package main — GT06 frame encoder used by the E2E harness.
//
// The encoder is intentionally INDEPENDENT of the ingestion service
// implementation: it computes CRC-ITU bitwise (the service uses a precomputed
// table), so a bug in one path cannot mask a bug in the other.
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"time"
)

// crcITU computes the GT06 CRC-ITU (CRC-16/X-25: init 0xFFFF, reflected poly
// 0x8408 — the reflection of 0x1021 — final XOR 0xFFFF) bitwise.
//
// Verified against the documented v3.1 vector: the service reply frame
// 78 78 05 01 00 01 D9 DC 0D 0A carries CRC 0xD9DC over [05 01 00 01].
// (Note: Teltonika uses a DIFFERENT CRC — reflected poly 0xA001 — so the two
// must never be unified.)
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

// buildFrame assembles a GT06 terminal frame: start || length || proto || content
// || crc(2) || stop.
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

// positionFrame builds a 0x22 position packet with a valid GPS fix:
//
//	date-time(6) satellites(1) lat(4) lon(4) speed(1) course/status(2) + tail
//
// Coordinates are transmitted as |deg| * 1_800_000 with the hemisphere carried
// in the course/status bits (bit2 = North, bit3 = West) — the raw field is
// UNSIGNED, so a negative value must never be cast into it.
func positionFrame(t time.Time, lat, lon, speedKmh float64, acc bool) []byte {
	content := make([]byte, 0, 33)
	// Date time (plain-hex encoding, matching the default GT06_DATE_BCD=false).
	content = append(content,
		byte(t.Year()-2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
	content = append(content, 0x0A) // 10 satellites (low nibble)

	content = appendUint32(content, uint32(math.Abs(lat)*1800000))
	content = appendUint32(content, uint32(math.Abs(lon)*1800000))

	// Speed is transmitted in knots.
	speedKnots := byte(speedKmh / 1.852)
	content = append(content, speedKnots)

	// Course 0° + status: positioned, plus hemisphere bits.
	status := uint16(0x1000) // GPS positioned
	if lat >= 0 {
		status |= 0x0400 // North
	}
	if lon < 0 {
		status |= 0x0800 // West (0 = East)
	}
	content = append(content, byte(status>>8), byte(status))

	// Tail: MCC(2) MNC(1) LAC(2) CellID(3) ACC(1) upload(1) gpsRealtime(1) mileage(4)
	content = append(content, 0x01, 0xF4, 0x01, 0x00, 0x01, 0x00, 0x01, 0x23)
	if acc {
		content = append(content, 0x01)
	} else {
		content = append(content, 0x00)
	}
	content = append(content, 0x00, 0x00) // upload mode + GPS realtime
	content = appendUint32(content, 0)    // mileage
	return buildFrame(0x22, content)
}

// heartbeatFrame builds a 0x13 heartbeat packet.
func heartbeatFrame() []byte {
	return buildFrame(0x13, []byte{0x00})
}

// appendUint32 appends a big-endian uint32.
func appendUint32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}

// readFrame reads one framed reply (start || length || proto || content || crc ||
// stop) and returns the raw bytes, mainly to verify the server ACKs.
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
	rest := make([]byte, int(lengthByte)+4) // proto+content + crc(2) + stop(2)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, err
	}
	return append([]byte{start, second, lengthByte}, rest...), nil
}

// selfTestCRC validates the encoder against the documented v3.1 vector:
// length(05) proto(01) content(00 01) must produce CRC 0xD9DC.
func selfTestCRC() error {
	got := crcITU([]byte{0x05, 0x01, 0x00, 0x01})
	if got != 0xD9DC {
		return fmt.Errorf("crc self-test failed: got 0x%04X want 0xD9DC", got)
	}
	return nil
}
