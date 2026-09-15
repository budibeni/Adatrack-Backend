package controllers

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"ajb_gps/ingestion-tcp/models"
)

// Teltonika Codec IDs (AVL family, PRD Module 1b — own protocol reference).
const (
	teltonikaCodec8  = 0x08
	teltonikaCodec8E = 0x8E
)

// teltonikaCRC16 is CRC-16/IBM (reflected poly 0xA001, init 0xFFFF) as used by
// Teltonika Codec 8/8E, transmitted at the end of the data field.
func teltonikaCRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// readTeltonikaIMEI reads the login packet: 2-byte big-endian length + ASCII IMEI.
// Some firmwares send the IMEI as 30 ASCII-hex characters (15 bytes); both forms
// are normalised to the 15-character ASCII IMEI used by the allowlist.
func readTeltonikaIMEI(r *bufio.Reader) (string, error) {
	var lenBuf [2]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", err
	}
	n := int(binary.BigEndian.Uint16(lenBuf[:]))
	if n <= 0 || n > 64 {
		return "", fmt.Errorf("invalid teltonika imei length %d", n)
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", err
	}
	imei := string(raw)
	if len(imei) == 30 {
		var hex [15]byte
		if _, err := fmt.Sscanf(imei, "%2x%2x%2x%2x%2x%2x%2x%2x%2x%2x%2x%2x%2x%2x%2x",
			&hex[0], &hex[1], &hex[2], &hex[3], &hex[4], &hex[5], &hex[6], &hex[7],
			&hex[8], &hex[9], &hex[10], &hex[11], &hex[12], &hex[13], &hex[14]); err == nil {
			imei = string(hex[:])
		}
	}
	return imei, nil
}

// readTeltonikaAVLPacket reads one AVL packet (TCP framing):
//
//	4-byte preamble (zeros) || 4-byte data length || data field
//
// The data field contains codec id, records, CRC and the repeated record count,
// so the framing only needs the length.
func readTeltonikaAVLPacket(r *bufio.Reader) ([]byte, error) {
	var preamble [4]byte
	if _, err := io.ReadFull(r, preamble[:]); err != nil {
		return nil, err
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint32(lenBuf[:]))
	if n <= 0 || n > 8192 {
		return nil, fmt.Errorf("invalid teltonika packet length %d", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// parseTeltonikaAVL decodes an AVL data field:
//
//	[0]      Codec ID (0x08 / 0x8E)
//	[1]      Number of data 1
//	[2..]    AVL records
//	[-5..-1] CRC-16 (4 bytes) over [0 .. len-5) + Number of data 2 (1 byte)
//
// The CRC is validated over the same range the device used (client → server),
// which is the standard Teltonika layout; a mismatch or a truncated record
// rejects the whole packet so corrupted data is never partially ingested.
func parseTeltonikaAVL(payload []byte) ([]models.TelemetryMessage, error) {
	if len(payload) < 3+5 {
		return nil, errors.New("teltonika payload too short")
	}
	codec := payload[0]
	numData := int(payload[1])
	if numData <= 0 || numData > 255 {
		return nil, fmt.Errorf("invalid teltonika record count %d", numData)
	}

	dataEnd := len(payload) - 5
	want := teltonikaCRC16(payload[:dataEnd])
	got := binary.LittleEndian.Uint32(payload[dataEnd : dataEnd+4])
	if uint32(want) != got {
		return nil, fmt.Errorf("teltonika crc mismatch: want 0x%04x got 0x%08x", want, got)
	}

	msgs := make([]models.TelemetryMessage, 0, numData)
	off := 2
	for i := 0; i < numData; i++ {
		var (
			msg  models.TelemetryMessage
			next int
			perr error
		)
		switch codec {
		case teltonikaCodec8:
			msg, next, perr = parseCodec8Record(payload[:dataEnd], off)
		case teltonikaCodec8E:
			msg, next, perr = parseCodec8ERecord(payload[:dataEnd], off)
		default:
			return nil, fmt.Errorf("unsupported teltonika codec 0x%02X", codec)
		}
		if perr != nil {
			return nil, fmt.Errorf("teltonika record %d: %w", i, perr)
		}
		msgs = append(msgs, msg)
		off = next
	}
	return msgs, nil
}
