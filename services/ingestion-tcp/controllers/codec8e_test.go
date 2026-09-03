package controllers

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// buildCodec8EPacket membangun payload AVL Codec 8 Extended (0x8E) satu record
// dengan CRC-16 LE yang benar — struktur 1:1 dengan parseCodec8ERecord mengikuti
// wiki Teltonika "Codec" (Codec 8 Extended): base 28 byte + 5 count grup 2 byte.
func buildCodec8EPacket(ts time.Time, lat, lon, speed float64) []byte {
	rec := make([]byte, 28, 64)
	binary.BigEndian.PutUint64(rec[0:8], uint64(ts.UnixMilli()))
	rec[8] = 0 // priority: low
	binary.BigEndian.PutUint32(rec[9:13], uint32(int32(lon*1e7)))
	binary.BigEndian.PutUint32(rec[13:17], uint32(int32(lat*1e7)))
	binary.BigEndian.PutUint16(rec[17:19], 125)              // altitude
	binary.BigEndian.PutUint16(rec[19:21], 180)              // angle
	rec[21] = 10                                             // satellites
	binary.BigEndian.PutUint16(rec[22:24], uint16(speed*10)) // speed 10*km/h
	binary.BigEndian.PutUint16(rec[24:26], 0)                // Event IO ID: none
	binary.BigEndian.PutUint16(rec[26:28], 3)                // N of Total IO = N1+N2+NX

	// N1 (1-byte IO): 1 elemen — id 72 battery 0xD2.
	rec = append(rec, 0, 1)
	rec = append(rec, 0, 72, 0xD2)
	// N2 (2-byte IO): 1 elemen — id 67 ignition ON.
	rec = append(rec, 0, 1)
	rec = append(rec, 0, 67, 0, 1)
	// N4: 0 elemen.
	rec = append(rec, 0, 0)
	// N8: 0 elemen.
	rec = append(rec, 0, 0)
	// NX (variabel): 1 elemen — id 86 fuel level, length 2, nilai 300 (0x012C).
	rec = append(rec, 0, 1)
	rec = append(rec, 0, 86, 0, 2, 0x01, 0x2C)

	// Payload AVL: codec 0x8E + jumlah data 1 + record + CRC(2 LE) + jumlah data (ulang).
	p := append([]byte{teltonikaCodec8E, 0x01}, rec...)
	crc := teltonikaCRC16(p)
	p = append(p, byte(crc), byte(crc>>8), 0x01)
	return p
}

func TestParseTeltonikaCodec8E(t *testing.T) {
	ts := time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)
	pkt := buildCodec8EPacket(ts, -6.2088, 106.8456, 45.5)
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
	if m.Heading != 180 {
		t.Errorf("heading = %d, want 180", m.Heading)
	}
	if m.Battery != 0xD2 {
		t.Errorf("battery = %d, want 0xD2", m.Battery)
	}
	if !m.ACC {
		t.Error("acc expected on")
	}
	if m.FuelLevel == nil {
		t.Fatal("fuel_level expected present")
	}
	if math.Abs(*m.FuelLevel-300) > 1e-6 {
		t.Errorf("fuel_level = %v, want 300", *m.FuelLevel)
	}
}

func TestParseTeltonikaCodec8EZeroIO(t *testing.T) {
	ts := time.Date(2026, 8, 1, 2, 3, 0, 0, time.UTC)
	rec := make([]byte, 38) // base 28 + 5 count grup (2 byte each), semua nol
	binary.BigEndian.PutUint64(rec[0:8], uint64(ts.UnixMilli()))
	p := append([]byte{teltonikaCodec8E, 0x01}, rec...)
	crc := teltonikaCRC16(p)
	p = append(p, byte(crc), byte(crc>>8), 0x01)
	msgs, err := parseTeltonikaAVL(p)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 record, got %d", len(msgs))
	}
	if msgs[0].Battery != 0 || msgs[0].FuelLevel != nil || msgs[0].ACC {
		t.Errorf("expected no IO fields, got %+v", msgs[0])
	}
}

func TestParseTeltonikaCodec8ECrcMismatch(t *testing.T) {
	pkt := buildCodec8EPacket(time.Now(), 1, 2, 3)
	pkt[4] ^= 0xFF
	if _, err := parseTeltonikaAVL(pkt); err == nil {
		t.Error("expected crc mismatch error")
	}
}

func TestParseTeltonikaCodec8ETruncatedNoPanic(t *testing.T) {
	pkt := buildCodec8EPacket(time.Now(), 1, 2, 3)
	// Record terpotong di tengah base → error, bukan panic.
	if _, err := parseTeltonikaAVL(pkt[:10]); err == nil {
		t.Error("expected error for truncated base record")
	}
	// Record berhenti tepat setelah base 28 byte (count grup hilang) →
	// error, bukan panic (regresi slice out-of-range pada count grup).
	if _, err := parseTeltonikaAVL(pkt[:2+28]); err == nil {
		t.Error("expected error for truncated io group counts")
	}
}
