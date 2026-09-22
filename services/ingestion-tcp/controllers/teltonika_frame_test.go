package controllers

// teltonika_frame_test.go — framing + ACK Teltonika dan encoder tanggal GT06.
//
// Pelengkap teltonika_test.go / teltonika8e_test.go: keduanya menguji *isi* paket
// (parseTeltonikaAVL), sedangkan file ini menguji sisi yang belum tersentuh —
// pembacaan framing TCP (readTeltonikaAVLPacket), balasan record-count
// (writeTeltonikaAck) dan encoder tanggal BCD server→perangkat (intToBCD +
// SetDateEncoding). Semua hermetik: tanpa DB, NATS, atau socket nyata.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// TestReadTeltonikaAVLPacket pins the TCP framing contract:
//
//	4-byte preamble (zeros) || 4-byte big-endian length || data field
//
// A wrong length parse would desynchronise the stream for every later record, so
// the bounds (<= 0 and > 8192) are asserted explicitly.
func TestReadTeltonikaAVLPacket(t *testing.T) {
	frame := func(preamble []byte, length uint32, payload []byte) *bufio.Reader {
		out := append([]byte{}, preamble...)
		out = appendUint32BE(out, length)
		out = append(out, payload...)
		return bufReader(out)
	}

	t.Run("valid packet returns exactly the payload", func(t *testing.T) {
		payload := []byte{0x08, 0x01, 0xAA, 0xBB}
		body, err := readTeltonikaAVLPacket(frame([]byte{0, 0, 0, 0}, uint32(len(payload)), payload))
		if err != nil {
			t.Fatalf("readTeltonikaAVLPacket: %v", err)
		}
		if !bytes.Equal(body, payload) {
			t.Fatalf("payload = % x, want % x", body, payload)
		}
	})

	t.Run("zero length is rejected", func(t *testing.T) {
		if _, err := readTeltonikaAVLPacket(frame([]byte{0, 0, 0, 0}, 0, nil)); err == nil {
			t.Fatal("panjang 0 diterima, want error")
		}
	})

	t.Run("length above the 8 KiB guard is rejected", func(t *testing.T) {
		if _, err := readTeltonikaAVLPacket(frame([]byte{0, 0, 0, 0}, 8193, nil)); err == nil {
			t.Fatal("panjang 8193 diterima, want error")
		}
	})

	t.Run("truncated payload is rejected", func(t *testing.T) {
		if _, err := readTeltonikaAVLPacket(frame([]byte{0, 0, 0, 0}, 16, []byte{1, 2, 3})); err == nil {
			t.Fatal("payload terpotong diterima, want error")
		}
	})

	t.Run("truncated header is rejected", func(t *testing.T) {
		if _, err := readTeltonikaAVLPacket(bufReader([]byte{0, 0})); err == nil {
			t.Fatal("header terpotong diterima, want error")
		}
	})
}

// TestWriteTeltonikaAck asserts the 4-byte big-endian record count Codec 8/8E
// requires, and that a broken socket is reported (not silently swallowed).
func TestWriteTeltonikaAck(t *testing.T) {
	srv, cli := net.Pipe()
	t.Cleanup(func() { _ = cli.Close() })

	done := make(chan bool, 1)
	go func() { done <- writeTeltonikaAck(srv, 7) }()

	buf := make([]byte, 4)
	_ = cli.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := cli.Read(buf); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if got := binary.BigEndian.Uint32(buf); got != 7 {
		t.Fatalf("ack count = %d, want 7", got)
	}
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("writeTeltonikaAck = false pada socket sehat")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writeTeltonikaAck menggantung")
	}
	_ = srv.Close()

	// Socket tertutup -> laporan kegagalan, bukan panic.
	if writeTeltonikaAck(srv, 1) {
		t.Fatal("writeTeltonikaAck = true pada socket tertutup")
	}
}

// TestGT06DateEncoding covers the server→device date encoder and the
// GT06_DATE_BCD toggle: sebagian besar model encode plain hex, sebagian
// firmware lain memakai BCD.
func TestGT06DateEncoding(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want byte
	}{
		{in: 0, want: 0x00},
		{in: 9, want: 0x09},
		{in: 10, want: 0x10},
		{in: 23, want: 0x23},
		{in: 59, want: 0x59},
		{in: 99, want: 0x99},
	} {
		if got := intToBCD(tc.in); got != tc.want {
			t.Fatalf("intToBCD(%d) = 0x%02x, want 0x%02x", tc.in, got, tc.want)
		}
	}

	// Default: plain hex (0x59 -> 89). Toggle BCD: 0x59 -> 59.
	t.Cleanup(func() { SetDateEncoding(false) })

	SetDateEncoding(false)
	if DateEncodingBCD() {
		t.Fatal("DateEncodingBCD() = true, want false (default plain hex)")
	}
	if got := decodeDateField(0x59); got != 89 {
		t.Fatalf("decodeDateField(0x59) plain = %d, want 89", got)
	}

	SetDateEncoding(true)
	if !DateEncodingBCD() {
		t.Fatal("DateEncodingBCD() = false setelah SetDateEncoding(true)")
	}
	if got := decodeDateField(0x59); got != 59 {
		t.Fatalf("decodeDateField(0x59) BCD = %d, want 59", got)
	}
}
