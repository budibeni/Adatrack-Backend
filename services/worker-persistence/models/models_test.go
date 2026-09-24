package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestACCTriStateStorage pins the B6 audit fix at the persistence boundary: the
// `acc_status` parameter is 1/0 for a real device reading and nil (SQL NULL) when
// the device never reported ACC — never a fabricated 0.
func TestACCTriStateStorage(t *testing.T) {
	on := ToRow(TelemetryMessage{IMEI: "86001", ACC: BoolPtr(true), Timestamp: 1})
	if got := on.Values()[8]; got != 1 {
		t.Errorf("ACC on stored as %v, want 1", got)
	}

	off := ToRow(TelemetryMessage{IMEI: "86001", ACC: BoolPtr(false), Timestamp: 1})
	if got := off.Values()[8]; got != 0 {
		t.Errorf("ACC off stored as %v, want 0 (a literal device reading)", got)
	}

	unknown := ToRow(TelemetryMessage{IMEI: "86001", Timestamp: 1})
	if got := unknown.Values()[8]; got != nil {
		t.Errorf("missing ACC stored as %v, want nil (NULL)", got)
	}
	if unknown.ACC != nil {
		t.Errorf("missing ACC became %v, want nil", *unknown.ACC)
	}

	fuel := ToFuelRow(TelemetryMessage{IMEI: "86001", ACC: BoolPtr(false), Timestamp: 1})
	if got := fuel.Values()[8]; got != 0 {
		t.Errorf("fuel row ACC off stored as %v, want 0", got)
	}
}

// TestTelemetryACCJSONContract asserts the wire contract shared with ingestion:
// `acc` disappears when the frame carried no ACC information.
func TestTelemetryACCJSONContract(t *testing.T) {
	unknown, err := json.Marshal(TelemetryMessage{IMEI: "86001", Timestamp: 1})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(unknown), `"acc"`) {
		t.Fatalf("payload = %s, want no acc key for an unreported ACC", unknown)
	}

	off, err := json.Marshal(TelemetryMessage{IMEI: "86001", Timestamp: 1, ACC: BoolPtr(false)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(off), `"acc":false`) {
		t.Fatalf("payload = %s, want an explicit acc:false", off)
	}
}
