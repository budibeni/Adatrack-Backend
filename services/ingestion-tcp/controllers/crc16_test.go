package controllers

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

// crc16 of the exact byte sequences from the GT06 spec examples (validated
// against docs/docs-device GPS_Tracker_communication_protocol_v3.1.md §1.2,
// §2.2 and the v1.8.1 §5.1.3 / §5.1.2 examples).
func TestCRC16KnownVectors(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want uint16
	}{
		{"login-reply", []byte{0x05, 0x01, 0x00, 0x01}, 0xD9DC},
		{"terminal-login", []byte{0x0D, 0x01, 0x01, 0x23, 0x45, 0x67, 0x89, 0x01, 0x23, 0x45, 0x00, 0x01}, 0x8CDD},
		{"heartbeat-reply", []byte{0x05, 0x13, 0x01, 0x00}, 0xE1A0},
		{"login-reject-v31", []byte{0x05, 0x01, 0x00, 0x05}, 0x9FF8},
	}
	for _, tc := range cases {
		if got := crc16(tc.data); got != tc.want {
			t.Errorf("%s: crc16 = 0x%04X, want 0x%04X", tc.name, got, tc.want)
		}
	}
}

// buildFrame assembles a valid GT06 frame (short/long) with proper CRC-16.
func buildFrame(start0, start1 byte, lengthBytes int, payload []byte) []byte {
	var frame []byte
	frame = append(frame, start0, start1)
	var hdr []byte
	if lengthBytes == 1 {
		hdr = []byte{byte(len(payload))}
	} else {
		hdr = []byte{byte(len(payload) >> 8), byte(len(payload))}
	}
	frame = append(frame, hdr...)
	frame = append(frame, payload...)
	crcData := append(append([]byte{}, hdr...), payload...)
	sum := crc16(crcData)
	frame = append(frame, byte(sum>>8), byte(sum), 0x0d, 0x0a)
	return frame
}

func TestReadPacketShortFrame(t *testing.T) {
	// Login frame: protocol 0x01 + 15-byte ASCII IMEI.
	imei := []byte("864201040512345")
	payload := append([]byte{0x01}, imei...)
	frame := buildFrame(0x78, 0x78, 1, payload)

	p, err := ReadPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("ReadPacket error: %v", err)
	}
	if p.Protocol != 0x01 {
		t.Errorf("protocol = 0x%02x, want 0x01", p.Protocol)
	}
	if string(p.Data) != "864201040512345" {
		t.Errorf("data = %q", string(p.Data))
	}
}

func TestReadPacketLongFrame(t *testing.T) {
	// Large 2-byte-length framings (v3.1 §8.2.1) use 0x79 0x79.
	payload := []byte{0x21, 0x00, 0x00, 0x00, 0x01, 'A', 'B', 'C'}
	frame := buildFrame(0x79, 0x79, 2, payload)

	p, err := ReadPacket(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("ReadPacket long frame error: %v", err)
	}
	if p.Protocol != 0x21 {
		t.Errorf("protocol = 0x%02x, want 0x21", p.Protocol)
	}
	if string(p.Data) != string(payload[1:]) {
		t.Errorf("data = %q, want %q", string(p.Data), string(payload[1:]))
	}
}

func TestReadPacketBadStartBytes(t *testing.T) {
	r := bufio.NewReader(bytes.NewReader([]byte{0x99, 0x78, 0x01}))
	if _, err := ReadPacket(r); err == nil {
		t.Error("expected error for mixed/bad start bytes")
	}
	// 0x78 followed by 0x79 is not a valid pair.
	r2 := bufio.NewReader(bytes.NewReader([]byte{0x78, 0x79, 0x01}))
	if _, err := ReadPacket(r2); err == nil {
		t.Error("expected error for mismatched start bytes")
	}
}

func TestReadPacketCRCmismatch(t *testing.T) {
	payload := []byte{0x01, 0x00, 0x00, 0x03}
	frame := buildFrame(0x78, 0x78, 1, payload)
	frame[len(frame)-4] ^= 0xFF // corrupt one CRC byte
	if _, err := ReadPacket(bufio.NewReader(bytes.NewReader(frame))); err == nil {
		t.Error("expected CRC mismatch error")
	}
}

func TestWriteAckFormatAndCRC(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := client.Read(buf)
		done <- buf[:n]
	}()

	if err := WriteAck(server, 0x01, []byte{0x00, 0x01}); err != nil {
		t.Fatalf("WriteAck error: %v", err)
	}
	frame := <-done
	if len(frame) < 10 {
		t.Fatalf("frame too short: %d bytes", len(frame))
	}
	if frame[0] != 0x78 || frame[1] != 0x78 {
		t.Errorf("frame should start with 0x78 0x78, got 0x%02x 0x%02x", frame[0], frame[1])
	}
	// Length byte = protocol + content.
	if int(frame[2]) != 1+2 {
		t.Errorf("length = %d, want 3", frame[2])
	}
	if frame[3] != 0x01 {
		t.Errorf("protocol = 0x%02x, want 0x01", frame[3])
	}
	// Validate the CRC we wrote.
	crcData := frame[2 : len(frame)-4]
	if have, want := binary.BigEndian.Uint16(frame[len(frame)-4:len(frame)-2]), crc16(crcData); have != want {
		t.Errorf("ack CRC = 0x%04X, want 0x%04X", have, want)
	}
	if frame[len(frame)-2] != 0x0d || frame[len(frame)-1] != 0x0a {
		t.Error("frame should end with 0x0d 0x0a")
	}
}
