package controllers

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// buildCodec8Packet constructs a Teltonika codec-8 AVL payload (after the
// 4-byte length prefix) with a single record and correct LE CRC-16.
func buildCodec8Packet(ts time.Time, lat, lon float64, speed float64) []byte {
	rec := make([]byte, 26, 28)
	binary.BigEndian.PutUint64(rec[0:8], uint64(ts.UnixMilli()))
	rec[8] = 0 // priority
	binary.BigEndian.PutUint32(rec[9:13], uint32(int32(lon*1e7)))
	binary.BigEndian.PutUint32(rec[13:17], uint32(int32(lat*1e7)))
	binary.BigEndian.PutUint16(rec[17:19], 125) // altitude 125 m
	binary.BigEndian.PutUint16(rec[19:21], 180) // angle 180
	rec[21] = 10                                // satellites
	binary.BigEndian.PutUint16(rec[22:24], uint16(speed*10))
	rec[24] = 0 // event id
	rec[25] = 1 // 1 IO element
	// IO: id=72 (battery V*100), len=2, value=1250 (12.50 V)
	rec = append(rec, 72, 2, 0x04, 0xD2)

	payload := []byte{0x08, 0x01, 0x00} // codec, count, placeholder
	payload = append(payload, rec...)
	payload[2] = 0
	// Recompute: payload = codec(1) count(1) + rec
	body := append([]byte{0x08, 0x01}, rec...)
	crc := teltonikaCRC16(body)
	p := append([]byte{0x08, 0x01}, rec...)
	p = append(p, byte(crc), byte(crc>>8), 0x01) // LE crc + repeated count
	return p
}

func TestTeltonikaCRC16(t *testing.T) {
	// CRC-16 (poly 0x1021 init 0) "123456789" -> 0x31C3 (XMODEM gives 0x31C3).
	if got := teltonikaCRC16([]byte("123456789")); got != 0x31C3 {
		t.Errorf("teltonikaCRC16 = 0x%04x, want 0x31C3", got)
	}
}

func TestParseTeltonikaCodec8(t *testing.T) {
	ts := time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC)
	pkt := buildCodec8Packet(ts, -6.2088, 106.8456, 45.5)
	msgs, err := parseTeltonikaAVL(pkt)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 record, got %d", len(msgs))
	}
	m := msgs[0]
	if math.Abs(m.Lat-(-6.2088)) > 1e-6 {
		t.Errorf("lat = %v, want ~-6.2088", m.Lat)
	}
	if math.Abs(m.Lon-106.8456) > 1e-6 {
		t.Errorf("lon = %v, want ~106.8456", m.Lon)
	}
	if math.Abs(m.Speed-45.5) > 1e-6 {
		t.Errorf("speed = %v, want 45.5", m.Speed)
	}
	if m.Timestamp != ts.Unix() {
		t.Errorf("ts = %d, want %d", m.Timestamp, ts.Unix())
	}
	if m.Satellites != 10 {
		t.Errorf("satellites = %d, want 10", m.Satellites)
	}
	if m.Battery != 0xD2 { // low byte of 0x04D2 (12.50 V)
		t.Errorf("battery = %d, want 0xD2", m.Battery)
	}
}

func TestParseTeltonikaCRCmismatch(t *testing.T) {
	pkt := buildCodec8Packet(time.Now(), 1, 2, 3)
	// corrupt a body byte (recompute nothing so crc no longer matches)
	pkt[4] ^= 0xFF
	if _, err := parseTeltonikaAVL(pkt); err == nil {
		t.Error("expected crc mismatch error")
	}
}

func TestReadTeltonikaIME(t *testing.T) {
	// IMEI as ASCII digits (15 bytes), length prefix 0x0F.
	imei := "359070061389042"
	body := append([]byte{0, 0, 0, 15}, []byte(imei)...)
	got, err := readTeltonikaIME(bufio.NewReader(bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("read ime error: %v", err)
	}
	if got != imei {
		t.Errorf("imei = %q, want %q", got, imei)
	}
}
