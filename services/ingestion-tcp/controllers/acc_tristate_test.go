package controllers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

// TestACCIsTriState pins the B6 audit fix at the decoder level: ACC must only be
// claimed when the frame really carried it.
//
//	position 0x22 tail byte 0x01 → ACC = true  (device value)
//	position 0x22 tail byte 0x00 → ACC = false (device value, NOT inferred)
//	fuel sentence 0x94/0x0D      → ACC = nil   (packet cannot report ACC)
//	LBS alarm 0x19               → ACC = nil   (no terminal information byte)
func TestACCIsTriState(t *testing.T) {
	when := time.Date(2026, 9, 24, 4, 15, 0, 0, time.UTC)
	block := buildGPSBlock(when, 0x0A, -6.2088, 106.8456, 23, false, true)

	withTail := func(acc byte) []byte {
		payload := append([]byte{}, block...)
		payload = append(payload, 0x01, 0xF4, 0x01, 0x00, 0x01, 0x00, 0x01, 0x23) // LBS tail
		payload = append(payload, acc)                                            // ACC byte
		payload = append(payload, 0x00, 0x00, 0x00, 0x00, 0x00, 0x64)
		return payload
	}

	on, ok := ParsePosition(withTail(0x01))
	if !ok {
		t.Fatal("ParsePosition rejected the ACC-on payload")
	}
	if on.ACC == nil || !*on.ACC {
		t.Fatalf("ACC = %v, want the literal device value true", on.ACC)
	}

	off, ok := ParsePosition(withTail(0x00))
	if !ok {
		t.Fatal("ParsePosition rejected the ACC-off payload")
	}
	if off.ACC == nil {
		t.Fatal("ACC = nil, want a literal false when the device reported 0")
	}
	if *off.ACC {
		t.Fatalf("ACC = true, want false")
	}

	fuel, ok := ParseInfoTransmit(fuelPayload(t, when, "025.900", "025.400"))
	if !ok {
		t.Fatal("ParseInfoTransmit rejected the reference sentence")
	}
	if fuel.ACC != nil {
		t.Fatalf("fuel sentence ACC = %v, want nil (the packet carries no ACC)", *fuel.ACC)
	}

	if lbs := ParseLBSAlarm(nil, "864201040512345", "DEV001", 1); lbs.ACC != nil {
		t.Fatalf("LBS alarm ACC = %v, want nil", *lbs.ACC)
	}

	// Wire contract: an unknown ACC must disappear from the JSON payload (so
	// worker-live/worker-persistence store NULL instead of a fabricated false).
	unknown, err := json.Marshal(models.TelemetryMessage{IMEI: "1", Timestamp: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(unknown), "\"acc\"") {
		t.Fatalf("payload = %s, want no acc key when the device did not report ACC", unknown)
	}
	known, err := json.Marshal(models.TelemetryMessage{IMEI: "1", Timestamp: 1, ACC: models.BoolPtr(false)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(known), `"acc":false`) {
		t.Fatalf("payload = %s, want an explicit acc:false for a device reading", known)
	}
}
