package controllers

import (
	"bufio"
	"bytes"
	"testing"
)

// buildTK103Frame assembles a provisional TK-103 GT-clone frame with XOR checksum.
func buildTK103Frame(start byte, cmd byte, data []byte) []byte {
	payload := append([]byte{cmd}, data...)
	frame := []byte{0x78, start, byte(len(payload))}
	frame = append(frame, payload...)
	var x byte
	for _, b := range frame {
		x ^= b
	}
	frame = append(frame, x, 0x0d, 0x0a)
	return frame
}

func TestParseTK103LoginFrame(t *testing.T) {
	frame := buildTK103Frame(0x78, 0x12, []byte("359070061389042"))
	p, err := parseTK103Frame(bufio.NewReader(bytes.NewReader(frame)))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if p.Protocol != 0x12 {
		t.Errorf("protocol = 0x%02x, want 0x12", p.Protocol)
	}
	if string(p.Data) != "359070061389042" {
		t.Errorf("data = %q", string(p.Data))
	}
}

func TestParseTK103ChecksumMismatch(t *testing.T) {
	frame := buildTK103Frame(0x78, 0x12, []byte("359070061389042"))
	frame[len(frame)-3] ^= 0xFF // corrupt checksum
	if _, err := parseTK103Frame(bufio.NewReader(bytes.NewReader(frame))); err == nil {
		t.Error("expected checksum mismatch error")
	}
}

func TestParseTK103BadStart(t *testing.T) {
	if _, err := parseTK103Frame(bufio.NewReader(bytes.NewReader([]byte{0x99, 0x78, 0x01}))); err == nil {
		t.Error("expected error for bad start byte")
	}
}
