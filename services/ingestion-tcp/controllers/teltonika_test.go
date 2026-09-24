package controllers

import (
	"encoding/binary"
	"testing"
)

// finishTeltonikaPacket appends the CRC (over codec..records) + record count.
func finishTeltonikaPacket(codec byte, count byte, body []byte) []byte {
	packet := append([]byte{codec, count}, body...)
	crc := teltonikaCRC16(packet)
	var tail [5]byte
	binary.LittleEndian.PutUint32(tail[0:4], uint32(crc))
	tail[4] = count
	return append(packet, tail[:]...)
}

// TestTeltonikaIMEILogin verifies the 2-byte length + IMEI login parse.
func TestTeltonikaIMEILogin(t *testing.T) {
	imei := "864201040512345"
	login := make([]byte, 0, 2+len(imei))
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(imei)))
	login = append(login, lenBuf[:]...)
	login = append(login, imei...)

	got, err := readTeltonikaIMEI(bufReader(login))
	if err != nil {
		t.Fatalf("readTeltonikaIMEI: %v", err)
	}
	if got != imei {
		t.Errorf("imei = %q, want %q", got, imei)
	}

	if _, err := readTeltonikaIMEI(bufReader([]byte{0x00, 0x00})); err == nil {
		t.Error("expected an error for a zero-length IMEI packet")
	}
}

// TestParseCodec8Packet decodes a Codec 8 record with GPS + IO elements.
func TestParseCodec8Packet(t *testing.T) {
	rec := make([]byte, 0, 26+8)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(1_767_000_000_000))
	rec = append(rec, ts[:]...)                     // timestamp (ms)
	rec = append(rec, 0x00)                         // priority
	rec = appendUint32BE(rec, uint32(106_8456_000)) // lon = 106.8456 (×1e7)
	rec = appendUint32BE(rec, uint32(6_2088_000))   // lat = 6.2088
	rec = append(rec, 0x00, 0x64)                   // altitude = 100
	rec = append(rec, 0x00, 0x5A)                   // heading = 90
	rec = append(rec, 0x09)                         // satellites
	rec = append(rec, 0x01, 0x2C)                   // speed = 300 → 30.0 km/h
	rec = append(rec, 0x01)                         // event id
	rec = append(rec, 0x02)                         // io count
	rec = append(rec, 72, 1, 0x0D)                  // IO 72 (battery) = 13
	rec = append(rec, 1, 1, 0x01)                   // IO 1 (ignition) = 1

	packet := finishTeltonikaPacket(teltonikaCodec8, 1, rec)

	msgs, err := parseTeltonikaAVL(packet)
	if err != nil {
		t.Fatalf("parseTeltonikaAVL(codec8): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("decoded %d records, want 1", len(msgs))
	}
	got := msgs[0]
	if diff := absFloat(got.Lon - 106.8456); diff > 0.0001 {
		t.Errorf("lon = %.6f, want 106.8456", got.Lon)
	}
	if diff := absFloat(got.Lat - 6.2088); diff > 0.0001 {
		t.Errorf("lat = %.6f, want 6.2088", got.Lat)
	}
	if diff := absFloat(got.Speed - 30.0); diff > 0.001 {
		t.Errorf("speed = %.3f km/h, want 30.0 (10×km/h encoding)", got.Speed)
	}
	if got.Altitude != 100 || got.Heading != 90 || got.Satellites != 9 {
		t.Errorf("alt/heading/sats = %d/%d/%d, want 100/90/9",
			got.Altitude, got.Heading, got.Satellites)
	}
	if got.Battery != 13 {
		t.Errorf("battery = %d, want 13 (IO 72)", got.Battery)
	}
	if got.ACC == nil || !*got.ACC {
		t.Error("expected ACC=true from IO 1 (ignition)")
	}
	if got.Timestamp != 1_767_000_000 {
		t.Errorf("timestamp = %d, want 1767000000 (ms → s)", got.Timestamp)
	}
}

// TestParseTeltonikaRejectsBadCRC ensures a corrupted packet is rejected whole
// (no partial ingest).
func TestParseTeltonikaRejectsBadCRC(t *testing.T) {
	rec := make([]byte, 0, 26)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(1_767_000_000_000))
	rec = append(rec, ts[:]...)
	rec = append(rec, 0x00)
	rec = append(rec, make([]byte, 18)...) // priority tail: lon/lat/alt/heading/sats/speed/event/ioCount

	packet := finishTeltonikaPacket(teltonikaCodec8, 1, rec)
	packet[len(packet)-5] ^= 0xFF // corrupt the CRC

	if _, err := parseTeltonikaAVL(packet); err == nil {
		t.Fatal("expected a CRC mismatch error, got nil")
	}
}

// TestParseTeltonikaUnsupportedCodec rejects unknown codec IDs.
func TestParseTeltonikaUnsupportedCodec(t *testing.T) {
	packet := finishTeltonikaPacket(0x42, 1, make([]byte, 26))
	if _, err := parseTeltonikaAVL(packet); err == nil {
		t.Fatal("expected an unsupported codec error, got nil")
	}
}

// TestParseTeltonikaShortPayload rejects packets that are too short to hold a
// record (defensive parsing, no panic).
func TestParseTeltonikaShortPayload(t *testing.T) {
	if _, err := parseTeltonikaAVL([]byte{0x08, 0x01}); err == nil {
		t.Fatal("expected a short-payload error, got nil")
	}
}
