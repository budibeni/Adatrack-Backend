package controllers

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"time"
)

// bytesBuffer is a tiny buffered writer used by the framing tests.
type bytesBuffer struct{ buf bytes.Buffer }

// Write implements io.Writer.
func (b *bytesBuffer) Write(p []byte) (int, error) { return b.buf.Write(p) }

// bytes returns the accumulated bytes.
func (b *bytesBuffer) bytes() []byte { return b.buf.Bytes() }

// bufReader wraps a byte slice in the bufio.Reader ReadPacket expects.
func bufReader(data []byte) *bufio.Reader { return bufio.NewReader(bytes.NewReader(data)) }

// buildGPSBlock synthesizes the 18-byte GPS information block used by position
// and alarm packets (plain-hex date encoding, as the default configuration).
func buildGPSBlock(t time.Time, satellites byte, lat, lon, speedKnots float64, north, east bool) []byte {
	out := make([]byte, 0, 18)
	out = append(out,
		byte(t.Year()-2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
	out = append(out, satellites&0x0f)
	out = appendUint32BE(out, uint32(absFloat(lat)*1800000))
	out = appendUint32BE(out, uint32(absFloat(lon)*1800000))
	out = append(out, byte(speedKnots))
	status := uint16(0x1000) // positioned
	if north {
		status |= 0x0400
	}
	if !east {
		status |= 0x0800
	}
	out = append(out, byte(status>>8), byte(status))
	return out
}

// appendUint32BE appends a big-endian uint32.
func appendUint32BE(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

// absFloat returns the absolute value of a float64.
func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
