package controllers

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"adatrack_gps/ingestion-tcp/models"
)

// Packet is a decoded GT06 frame: Protocol is the protocol number and Data the
// raw Information Content (after the protocol byte, serial included).
type Packet struct {
	Protocol byte
	Data     []byte
}

// ReadPacket reads one GT06 frame. Both framings are supported:
//
//	0x78 0x78 → 1-byte Packet Length (v1.8.1 / base v3.1)
//	0x79 0x79 → 2-byte Packet Length (v3.1 §8.2.1 large content)
//
// The CRC-ITU is validated over length||protocol||content||serial; a corrupted
// frame is rejected with an error (the caller counts tcp_parse_errors_total and
// closes the connection — random bytes never cause a panic, PRD §9.6).
func ReadPacket(r *bufio.Reader) (Packet, error) {
	var p Packet

	b0, err := r.ReadByte()
	if err != nil {
		return p, err
	}
	b1, err := r.ReadByte()
	if err != nil {
		return p, err
	}

	lengthBytes := 1
	switch {
	case b0 == models.FrameStartShort && b1 == models.FrameStartShort:
	case b0 == models.FrameStartLong && b1 == models.FrameStartLong:
		lengthBytes = 2
	default:
		return p, fmt.Errorf("bad start bytes 0x%02x 0x%02x", b0, b1)
	}

	hdr := make([]byte, lengthBytes)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return p, err
	}
	length := int(hdr[0])
	if lengthBytes == 2 {
		length = int(binary.BigEndian.Uint16(hdr))
	}
	if length <= 0 || length > 4096 {
		return p, fmt.Errorf("invalid packet length %d", length)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return p, err
	}
	cks := make([]byte, 2)
	if _, err := io.ReadFull(r, cks); err != nil {
		return p, err
	}

	crcData := append(append([]byte{}, hdr...), payload...)
	if have, want := binary.BigEndian.Uint16(cks), crc16(crcData); have != want {
		return p, fmt.Errorf("crc mismatch: have 0x%04x want 0x%04x", have, want)
	}

	// Consume the stop bits (0x0D 0x0A).
	if _, err := r.ReadByte(); err != nil {
		return p, err
	}
	if _, err := r.ReadByte(); err != nil {
		return p, err
	}

	if len(payload) == 0 {
		return p, errors.New("empty payload")
	}
	p.Protocol = payload[0]
	p.Data = payload[1:]
	return p, nil
}

// WriteAck writes a GT06 server → terminal frame (1-byte length framing):
// start(0x78,0x78) || length || proto || data || crc(2) || stop(0x0D,0x0A).
func WriteAck(w io.Writer, proto byte, data []byte) error {
	frame := make([]byte, 0, 4+len(data)+4)
	frame = append(frame, models.FrameStartShort, models.FrameStartShort, byte(1+len(data)), proto)
	frame = append(frame, data...)
	// CRC covers length || proto || content (GT06 "Error Check").
	sum := crc16(frame[2:])
	frame = append(frame, byte(sum>>8), byte(sum), models.FrameStop0, models.FrameStop1)
	_, err := w.Write(frame)
	return err
}

// ---------------------------------------------------------------------------
// Date/time helpers
// ---------------------------------------------------------------------------

// dateBCD toggles the 6-byte Date Time encoding. Vendor examples decode as plain
// hex (0x17 = 23); some firmwares pack BCD instead (GT06_DATE_BCD=true).
var dateBCD bool

// SetDateEncoding selects BCD vs plain-hex date decoding (GT06_DATE_BCD env).
func SetDateEncoding(bcd bool) { dateBCD = bcd }

// DateEncodingBCD reports the active date encoding (tests/diagnostics).
func DateEncodingBCD() bool { return dateBCD }

// bcdToInt converts a BCD byte to its decimal value.
func bcdToInt(b byte) int { return int(b>>4)*10 + int(b&0x0f) }

// intToBCD encodes 0..99 as one BCD byte.
func intToBCD(v int) byte { return byte((v/10)<<4 | (v % 10)) }

// decodeDateField decodes one date/time byte using the active encoding.
func decodeDateField(b byte) int {
	if dateBCD {
		return bcdToInt(b)
	}
	return int(b)
}

// EncodeTime6 encodes a time into the 6-byte Date Time block (GT06 0x8A reply).
func EncodeTime6(t time.Time) []byte {
	if dateBCD {
		return []byte{
			intToBCD(t.Year() - 2000), intToBCD(int(t.Month())), intToBCD(t.Day()),
			intToBCD(t.Hour()), intToBCD(t.Minute()), intToBCD(t.Second()),
		}
	}
	return []byte{
		byte(t.Year() - 2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()),
	}
}

// ParseTime decodes the 6-byte Date Time block (v1.8.1 §5.2.1.4); the year is a
// 2-digit offset from 2000. Returns UTC.
func ParseTime(d []byte) (time.Time, bool) {
	if len(d) < 6 {
		return time.Time{}, false
	}
	year := 2000 + decodeDateField(d[0])
	month := time.Month(decodeDateField(d[1]))
	day := decodeDateField(d[2])
	hour := decodeDateField(d[3])
	minute := decodeDateField(d[4])
	second := decodeDateField(d[5])
	if month < 1 || month > 12 || day < 1 || day > 31 ||
		hour > 23 || minute > 59 || second > 59 {
		return time.Time{}, false
	}
	return time.Date(year, month, day, hour, minute, second, 0, time.UTC), true
}

// ParseLoginIMEI extracts the 15-character ASCII IMEI from a login payload
// (v1.8.1 §5.1.1.4 / v3.1 §1.1; trailing model/timezone bytes are ignored).
func ParseLoginIMEI(data []byte) string {
	if len(data) > 15 {
		data = data[:15]
	}
	return string(data)
}
