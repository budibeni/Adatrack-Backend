package controllers

import (
	"encoding/binary"
	"errors"

	"adatrack_gps/ingestion-tcp/models"
)

// Fuel sensor IO IDs (B5a, PRD FR-7.2) — the Teltonika AVL IO set is mapped
// through env so FLS/CAN-bus sensors work without a code change: 86 = fuel level
// (%), 87 = fuel used (L), 89 = fuel temperature (°C) by default.
var (
	fuelIOLevel = envInt("TELTONIKA_IO_FUEL_LEVEL", 86)
	fuelIOUsed  = envInt("TELTONIKA_IO_FUEL_USED", 87)
	fuelIOTemp  = envInt("TELTONIKA_IO_FUEL_TEMP", 89)
)

// applyTeltonikaIO maps one Teltonika IO element onto the canonical payload.
// Shared by Codec 8 (1-byte IDs) and Codec 8 Extended (2-byte IDs).
func applyTeltonikaIO(t *models.TelemetryMessage, id uint16, val uint64) {
	// Fuel IDs are resolved first: a deployment may remap them onto 66/67/239
	// for a CAN-bus gateway, so the generic ACC interpretation must not win.
	switch {
	case fuelIOLevel != 0 && id == uint16(fuelIOLevel):
		f := float64(val)
		t.FuelLevel = &f
		return
	case fuelIOUsed != 0 && id == uint16(fuelIOUsed):
		f := float64(val)
		t.FuelVolume = &f
		return
	case fuelIOTemp != 0 && id == uint16(fuelIOTemp):
		f := float64(int16(val))
		t.FuelTempC = &f
		return
	}

	switch id {
	case 72: // battery voltage (V × 100)
		t.Battery = uint8(val)
	case 66, 67: // external/internal power + ignition (0/1)
		// ACC is only claimed when the IO element is actually present in the
		// record (B6): a Teltonika device without an ignition input must leave
		// the field nil instead of publishing an inferred `false`.
		t.ACC = models.BoolPtr(val == 1)
	case 239, 1: // ignition (firmware dependent)
		t.ACC = models.BoolPtr(val == 1)
	case 24: // speed (km/h) fallback when the GPS element reports 0
		if t.Speed == 0 {
			t.Speed = float64(val)
		}
	}
}

// parseCodec8Record decodes one Codec 8 record:
//
//	timestamp(8) priority(1) lon(4) lat(4) alt(2) angle(2) sats(1) speed(2)
//	eventID(1) ioCount(1) [IO id(1) len(1) value(len)]*
func parseCodec8Record(b []byte, off int) (models.TelemetryMessage, int, error) {
	var t models.TelemetryMessage
	if off+26 > len(b) {
		return t, 0, errors.New("short codec8 record")
	}
	rec := b[off:]
	ms := int64(binary.BigEndian.Uint64(rec[0:8]))
	t.Lon = float64(int32(binary.BigEndian.Uint32(rec[9:13]))) / 1e7
	t.Lat = float64(int32(binary.BigEndian.Uint32(rec[13:17]))) / 1e7
	t.Altitude = int16(binary.BigEndian.Uint16(rec[17:19]))
	t.Timestamp = ms / 1000
	t.Heading = int16(binary.BigEndian.Uint16(rec[19:21]))
	t.Satellites = rec[21]
	t.Speed = float64(binary.BigEndian.Uint16(rec[22:24])) / 10.0 // 10 × km/h
	t.Fix = t.Satellites > 0

	ioCount := int(rec[25])
	pos := off + 26
	for j := 0; j < ioCount; j++ {
		if pos+2 > len(b) {
			return t, 0, errors.New("short codec8 io element")
		}
		id := uint16(b[pos])
		l := int(b[pos+1])
		pos += 2
		if pos+l > len(b) {
			return t, 0, errors.New("short codec8 io value")
		}
		var val uint64
		for k := 0; k < l; k++ {
			val = val<<8 | uint64(b[pos+k])
		}
		pos += l
		applyTeltonikaIO(&t, id, val)
	}
	return t, pos, nil
}

// parseCodec8ERecord decodes one Codec 8 Extended record (0x8E): base 28 bytes,
// 2-byte IO IDs and 2-byte group counts (1/2/4/8/variable-length groups).
func parseCodec8ERecord(b []byte, off int) (models.TelemetryMessage, int, error) {
	var t models.TelemetryMessage
	if off+28 > len(b) {
		return t, 0, errors.New("short codec8e record")
	}
	rec := b[off:]
	ms := int64(binary.BigEndian.Uint64(rec[0:8]))
	t.Lon = float64(int32(binary.BigEndian.Uint32(rec[9:13]))) / 1e7
	t.Lat = float64(int32(binary.BigEndian.Uint32(rec[13:17]))) / 1e7
	t.Altitude = int16(binary.BigEndian.Uint16(rec[17:19]))
	t.Timestamp = ms / 1000
	t.Heading = int16(binary.BigEndian.Uint16(rec[19:21]))
	t.Satellites = rec[21]
	t.Speed = float64(binary.BigEndian.Uint16(rec[22:24])) / 10.0
	t.Fix = t.Satellites > 0

	pos := off + 28
	for _, elemLen := range []int{1, 2, 4, 8} {
		if pos+2 > len(b) {
			return t, 0, errors.New("short codec8e io group count")
		}
		n := int(binary.BigEndian.Uint16(b[pos : pos+2]))
		pos += 2
		for j := 0; j < n; j++ {
			if pos+2+elemLen > len(b) {
				return t, 0, errors.New("short codec8e io element")
			}
			id := binary.BigEndian.Uint16(b[pos : pos+2])
			var val uint64
			for k := 0; k < elemLen; k++ {
				val = val<<8 | uint64(b[pos+2+k])
			}
			pos += 2 + elemLen
			applyTeltonikaIO(&t, id, val)
		}
	}

	// Variable-length group: count + [id(2) len(2) value(len)]*
	if pos+2 > len(b) {
		return t, 0, errors.New("short codec8e var group count")
	}
	n := int(binary.BigEndian.Uint16(b[pos : pos+2]))
	pos += 2
	for j := 0; j < n; j++ {
		if pos+4 > len(b) {
			return t, 0, errors.New("short codec8e var element")
		}
		id := binary.BigEndian.Uint16(b[pos : pos+2])
		l := int(binary.BigEndian.Uint16(b[pos+2 : pos+4]))
		pos += 4
		if pos+l > len(b) {
			return t, 0, errors.New("short codec8e var value")
		}
		var val uint64
		for k := 0; k < l; k++ {
			val = val<<8 | uint64(b[pos+k])
		}
		pos += l
		applyTeltonikaIO(&t, id, val)
	}
	return t, pos, nil
}
