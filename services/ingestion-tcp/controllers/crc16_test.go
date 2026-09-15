package controllers

import "testing"

// TestCRC16DocumentedVectors validates CRC-ITU against every frame documented in
// docs/docs-device/GPS_Tracker_communication_protocol_v3.1.md (Appendix 1).
func TestCRC16DocumentedVectors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want uint16
	}{
		{"login reply 78 78 05 01 00 01 D9 DC", []byte{0x05, 0x01, 0x00, 0x01}, 0xD9DC},
		{"login reject 78 78 05 01 00 05 9F F8", []byte{0x05, 0x01, 0x00, 0x05}, 0x9FF8},
		{"heartbeat reply 78 78 05 13 01 00 E1 A0", []byte{0x05, 0x13, 0x01, 0x00}, 0xE1A0},
	}
	for _, tc := range cases {
		if got := crc16(tc.data); got != tc.want {
			t.Errorf("%s: crc16 = 0x%04X, want 0x%04X", tc.name, got, tc.want)
		}
	}
}

// TestCRC16NotTeltonika documents that the GT06 CRC and the Teltonika CRC are
// different algorithms (a regression here would break one of the protocols).
func TestCRC16NotTeltonika(t *testing.T) {
	data := []byte{0x05, 0x01, 0x00, 0x01}
	if crc16(data) == teltonikaCRC16(data) {
		t.Fatalf("GT06 CRC (poly 0x8408) must differ from the Teltonika CRC (poly 0xA001)")
	}
}

// TestWriteAckAndReadPacketRoundTrip builds a server frame and parses it back,
// proving the framing (start/length/crc/stop) is symmetric.
func TestWriteAckAndReadPacketRoundTrip(t *testing.T) {
	var buf bytesBuffer
	if err := WriteAck(&buf, 0x05, []byte{0x01, 0x00}); err != nil {
		t.Fatalf("WriteAck: %v", err)
	}

	packet, err := ReadPacket(bufReader(buf.bytes()))
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}
	if packet.Protocol != 0x05 {
		t.Errorf("protocol = 0x%02X, want 0x05", packet.Protocol)
	}
	if len(packet.Data) != 2 || packet.Data[0] != 0x01 || packet.Data[1] != 0x00 {
		t.Errorf("data = % x, want 01 00", packet.Data)
	}
}

// TestReadPacketRejectsCorruption ensures CRC failures are surfaced (never a
// silent accept) and random bytes do not panic the parser.
func TestReadPacketRejectsCorruption(t *testing.T) {
	// Valid frame with a corrupted CRC.
	frame := []byte{0x78, 0x78, 0x05, 0x01, 0x00, 0x01, 0xDE, 0xAD, 0x0D, 0x0A}
	if _, err := ReadPacket(bufReader(frame)); err == nil {
		t.Fatal("expected a CRC mismatch error, got nil")
	}

	// Bad start bytes.
	if _, err := ReadPacket(bufReader([]byte{0x12, 0x34, 0x00})); err == nil {
		t.Fatal("expected a bad start byte error, got nil")
	}

	// Truncated frame (must return an error, not panic).
	if _, err := ReadPacket(bufReader([]byte{0x78, 0x78, 0x40, 0x01})); err == nil {
		t.Fatal("expected a short-read error, got nil")
	}
}

// TestReadPacketTwoByteLength accepts the 0x79 0x79 long framing (v3.1 §8.2.1).
func TestReadPacketTwoByteLength(t *testing.T) {
	body := []byte{0x01, 0x00, 0x01} // proto + content
	hdr := []byte{0x00, byte(len(body))}
	crcData := append(append([]byte{}, hdr...), body...)
	sum := crc16(crcData)

	frame := []byte{0x79, 0x79}
	frame = append(frame, hdr...)
	frame = append(frame, body...)
	frame = append(frame, byte(sum>>8), byte(sum), 0x0D, 0x0A)

	packet, err := ReadPacket(bufReader(frame))
	if err != nil {
		t.Fatalf("ReadPacket(long frame): %v", err)
	}
	if packet.Protocol != 0x01 {
		t.Errorf("protocol = 0x%02X, want 0x01", packet.Protocol)
	}
}
