package controllers

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"time"

	"ajb_gps/ingestion-tcp/models"
)

// ============================================================================
// Teltonika / FM family adapter (Codec 8 & 0x8E, plus Codec 7).
//
// NOTE (honest): repo ini tidak memuat dokument Teltonika resmi; implementasi
// ini didasarkan pada protokol AVL Teltonika yang dipublikasikan secara umum
// (Codec 8/8E "FMB/FMM" family). Setiap angka byte-offset wajib diverifikasi
// ulang terhadap dokument vendor + capture device nyata sebelum produksi —
// dicatat sebagai GAP di PRD (Module 1a).
// ============================================================================

const (
	teltonikaCodec8  = 0x08 // standard AVL (FMB): priority + event
	teltonikaCodec8E = 0x8E // asset-tracker long codec
	teltonikaCodec7  = 0x07 // short codec (tanpa priority/event)
	teltonikaCodec6  = 0x06 // extended codec dengan priority
)

// teltonikaCRC16 is CRC-16 (poly 0x1021, init 0x0000) used by Teltonika for
// the AVL data integrity check; transmitted as two bytes little-endian.
func teltonikaCRC16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// readTeltonikaIME reads the device login packet:
//
//	4 bytes length (big-endian, usually 0x0F) + N bytes ASCII IMEI
//
// Returns the IMEI string (caller replies 0x01).
func readTeltonikaIME(r *bufio.Reader) (string, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return "", err
	}
	n := int(binary.BigEndian.Uint32(lenBuf[:]))
	if n <= 0 || n > 64 {
		return "", fmt.Errorf("invalid teltonika ime length %d", n)
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return "", err
	}
	imei := string(raw)
	// Some firmwares send the IMEI as ASCII-hex (30 hex chars = 15 bytes);
	// others send raw ASCII digits (15 bytes). Normalise the hex form.
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

// parseTeltonikaAVL decodes one AVL data packet (payload AFTER the 4-byte
// length prefix). Layout:
//
//	1 byte CodecID
//	1 byte Number of Data
//	   N AVL records (codec-specific)
//	2 bytes CRC-16 (little-endian) over [CodecID .. last record]
//	1 byte Number of Data (repeat)
//
// Codec 8 record base (26 bytes + IO):
//
//	timestamp(8 ms) priority(1) lon(4) lat(4) alt(2) angle(2) sats(1)
//	speed(2, = 10*km/h) eventID(1) ioCount(1)
func parseTeltonikaAVL(payload []byte) ([]models.TelemetryMessage, error) {
	if len(payload) < 4 {
		return nil, errors.New("teltonika: payload too short")
	}
	codec := payload[0]
	count := int(payload[1])
	off := 2
	out := make([]models.TelemetryMessage, 0, count)

	switch codec {
	case teltonikaCodec8, teltonikaCodec8E:
		for i := 0; i < count; i++ {
			t, n, err := parseCodec8Record(payload[off:])
			if err != nil {
				return nil, fmt.Errorf("teltonika codec 8 record %d: %w", i, err)
			}
			off += n
			out = append(out, t)
		}
	case teltonikaCodec7:
		for i := 0; i < count; i++ {
			t, n, err := parseCodec7Record(payload[off:])
			if err != nil {
				return nil, fmt.Errorf("teltonika codec 7 record %d: %w", i, err)
			}
			off += n
			out = append(out, t)
		}
	default:
		return nil, fmt.Errorf("teltonika: unsupported codec 0x%02X", codec)
	}

	// Trailing CRC (2 bytes LE) + repeated count (1 byte).
	if off+3 > len(payload) {
		return nil, errors.New("teltonika: missing trailing crc/count")
	}
	wantCRC := binary.LittleEndian.Uint16(payload[off : off+2])
	if gotCRC := teltonikaCRC16(payload[:off]); wantCRC != 0 && wantCRC != gotCRC {
		return nil, fmt.Errorf("teltonika: crc mismatch have 0x%04x want 0x%04x", wantCRC, gotCRC)
	}
	return out, nil
}

// Teltonika fuel IO IDs (B5a) — configurable via env with safe defaults.
// Common FMB IO IDs: 86 = Fuel Level, 87 = Fuel Used, 84 = Fuel Temperature.
var (
	ioFuelLevel = envIOID("TELTONIKA_IO_FUEL_LEVEL", 86)
	ioFuelUsed  = envIOID("TELTONIKA_IO_FUEL_USED", 87)
	ioFuelTemp  = envIOID("TELTONIKA_IO_FUEL_TEMP", 84)
)

func envIOID(key string, def byte) byte {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 255 {
		return def
	}
	return byte(n)
}

func parseCodec8Record(b []byte) (models.TelemetryMessage, int, error) {
	var t models.TelemetryMessage
	if len(b) < 26 {
		return t, 0, errors.New("short record")
	}
	ms := int64(binary.BigEndian.Uint64(b[0:8]))
	t.Lon = float64(int32(binary.BigEndian.Uint32(b[9:13]))) / 1e7
	t.Lat = float64(int32(binary.BigEndian.Uint32(b[13:17]))) / 1e7
	t.Timestamp = ms / 1000
	t.Heading = int16(binary.BigEndian.Uint16(b[19:21]))
	t.Satellites = b[21]
	t.Speed = float64(binary.BigEndian.Uint16(b[22:24])) / 10.0 // 10*km/h
	ioCount := int(b[25])
	off := 26
	for j := 0; j < ioCount && off+2 <= len(b); j++ {
		id := b[off]
		l := int(b[off+1])
		off += 2
		var val uint64
		for k := 0; k < l && off < len(b); k++ {
			val = val<<8 | uint64(b[off])
			off++
		}
		switch id {
		case 72: // battery voltage (V*100)
			t.Battery = byte(val)
		case 66, 67: // movement / ignition 0-1
			t.ACC = val == 1
		default:
			// B5a: fuel sensor IO elements (configurable via env).
			switch id {
			case ioFuelLevel:
				f := float64(val)
				t.FuelLevel = &f
			case ioFuelUsed:
				f := float64(val)
				t.FuelVolume = &f
			case ioFuelTemp:
				f := float64(int16(val)) // signed temperature
				t.FuelTempC = &f
			}
		}
	}
	return t, off, nil
}

func parseCodec7Record(b []byte) (models.TelemetryMessage, int, error) {
	var t models.TelemetryMessage
	if len(b) < 24 {
		return t, 0, errors.New("short codec7 record")
	}
	ms := int64(binary.BigEndian.Uint64(b[0:8]))
	t.Lon = float64(int32(binary.BigEndian.Uint32(b[8:12]))) / 1e7
	t.Lat = float64(int32(binary.BigEndian.Uint32(b[12:16]))) / 1e7
	t.Timestamp = ms / 1000
	t.Heading = int16(binary.BigEndian.Uint16(b[18:20]))
	t.Satellites = b[20]
	t.Speed = float64(binary.BigEndian.Uint16(b[21:23])) / 10.0
	ioCount := int(b[23])
	off := 24
	for j := 0; j < ioCount && off+2 <= len(b); j++ {
		l := int(b[off+1])
		off += 2 + l
	}
	return t, off, nil
}

// handleTeltonika services a Teltonika device connection: IME login + AVL.
func handleTeltonika(c net.Conn) {
	protoName := models.ProtoTeltonika.String()
	defer connClose(c, protoName)

	r := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(models.IdleTimeout))
	imei, err := readTeltonikaIME(r)
	if err != nil {
		slog.Debug("teltonika: no ime / login timeout", "remote", c.RemoteAddr(), "error", err)
		return
	}
	// Tenant resolution / anti-spoofing (same allowlist path as GT06).
	dev, resolveErr := tenantMgr.ResolveDeviceByIMEI(ctx, imei)
	if resolveErr != nil {
		slog.Warn("teltonika: unauthorised IMEI rejected", "imei", imei, "reason", resolveErr)
		rejectedTotal.WithLabelValues("unauthorised").Inc()
		return
	}
	company, vehicleID := dev.CompanyCode, dev.VehicleID
	// Reply 0x01 to acknowledge the IME packet.
	if _, err := c.Write([]byte{0x01}); err != nil {
		return
	}
	slog.Info("teltonika authenticated", "imei", imei, "company", company, "vehicle_id", vehicleID, "remote", c.RemoteAddr())

	for {
		_ = c.SetReadDeadline(time.Now().Add(models.IdleTimeout))
		var lenBuf [4]byte
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			if err != io.EOF {
				slog.Debug("teltonika: read len error", "imei", imei, "error", err)
			}
			return
		}
		pktLen := int(binary.BigEndian.Uint32(lenBuf[:]))
		if pktLen <= 0 || pktLen > 8192 {
			slog.Warn("teltonika: invalid packet length", "imei", imei, "len", pktLen)
			return
		}
		payload := make([]byte, pktLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			slog.Debug("teltonika: read payload error", "imei", imei, "error", err)
			return
		}
		packetsTotal.WithLabelValues(protoName).Inc()
		msgs, perr := parseTeltonikaAVL(payload)
		if perr != nil {
			slog.Warn("teltonika: parse error", "imei", imei, "error", perr)
			rejectedTotal.WithLabelValues("parse").Inc()
			continue
		}
		for i := range msgs {
			msgs[i].IMEI = imei
			msgs[i].CompanyCode = company
			msgs[i].VehicleID = vehicleID
			if publishTelemetry(msgs[i]) == nil {
				parsedTotal.WithLabelValues(protoName).Inc()
			}
		}
		// Reply: 4-byte length + number of AVL records (codec 8 convention).
		var reply [5]byte
		binary.BigEndian.PutUint32(reply[0:4], 1)
		reply[4] = byte(len(msgs))
		if _, err := c.Write(reply[:]); err != nil {
			return
		}
	}
}
