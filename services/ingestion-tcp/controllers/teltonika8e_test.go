package controllers

import (
	"encoding/binary"
	"testing"

	"ajb_gps/ingestion-tcp/models"
)

// TestParseCodec8ExtendedPacket decodes a Codec 8 Extended record (0x8E): base
// 28 bytes, 2-byte IO IDs and 2-byte group counts.
func TestParseCodec8ExtendedPacket(t *testing.T) {
	rec := make([]byte, 0, 28+32)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(1_767_000_000_000))
	rec = append(rec, ts[:]...)                     // timestamp (ms)
	rec = append(rec, 0x00)                         // priority
	rec = appendUint32BE(rec, uint32(106_8456_000)) // lon
	rec = appendUint32BE(rec, uint32(6_2088_000))   // lat
	rec = append(rec, 0x00, 0x64)                   // altitude
	rec = append(rec, 0x00, 0x5A)                   // heading
	rec = append(rec, 0x08)                         // satellites
	rec = append(rec, 0x00, 0xC8)                   // speed = 200 → 20.0 km/h
	rec = append(rec, 0x00, 0x01, 0x00, 0x02)       // event IO id + total IO count
	rec = append(rec, 0x00, 0x01)                   // group1: 1 element (1 byte)
	rec = append(rec, 0x00, 0x48, 0x0A)             // IO 72 (battery) = 10
	rec = append(rec, 0x00, 0x00)                   // group2: 0 elements
	rec = append(rec, 0x00, 0x00)                   // group4: 0 elements
	rec = append(rec, 0x00, 0x00)                   // group8: 0 elements
	rec = append(rec, 0x00, 0x00)                   // variable group: 0 elements

	packet := finishTeltonikaPacket(teltonikaCodec8E, 1, rec)

	msgs, err := parseTeltonikaAVL(packet)
	if err != nil {
		t.Fatalf("parseTeltonikaAVL(codec8e): %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("decoded %d records, want 1", len(msgs))
	}
	if diff := absFloat(msgs[0].Speed - 20.0); diff > 0.001 {
		t.Errorf("speed = %.3f, want 20.0", msgs[0].Speed)
	}
	if msgs[0].Battery != 10 {
		t.Errorf("battery = %d, want 10 (IO 72 in a 1-byte group)", msgs[0].Battery)
	}
	if diff := absFloat(msgs[0].Lon - 106.8456); diff > 0.0001 {
		t.Errorf("lon = %.6f, want 106.8456", msgs[0].Lon)
	}
}

// TestApplyTeltonikaIOMapping documents the IO mapping used by both codecs.
func TestApplyTeltonikaIOMapping(t *testing.T) {
	var msg models.TelemetryMessage
	applyTeltonikaIO(&msg, 72, 0x0D)
	if msg.Battery != 13 {
		t.Errorf("battery = %d, want 13", msg.Battery)
	}

	applyTeltonikaIO(&msg, 239, 1)
	if !msg.ACC {
		t.Error("expected ACC=true for IO 239 = 1")
	}

	// Speed fallback when the GPS element reported 0.
	applyTeltonikaIO(&msg, 24, 55)
	if msg.Speed != 55 {
		t.Errorf("speed = %.1f, want 55 (IO 24 fallback)", msg.Speed)
	}

	// An unknown IO id must be ignored without touching the payload.
	before := msg
	applyTeltonikaIO(&msg, 9999, 123)
	if msg != before {
		t.Error("an unknown IO id modified the telemetry payload")
	}
}
