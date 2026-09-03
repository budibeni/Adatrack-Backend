package controllers

import (
	"encoding/binary"
	"errors"

	"ajb_gps/ingestion-tcp/models"
)

// applyTeltonikaIO memetakan satu IO element Teltonika ke field TelemetryMessage.
// Dipakai bersama oleh Codec 8 (IO ID 1 byte, di-cast ke uint16 oleh pemanggil)
// dan Codec 8 Extended (IO ID 2 byte) agar pemetaan tidak duplikat/drift.
func applyTeltonikaIO(t *models.TelemetryMessage, id uint16, val uint64) {
	switch id {
	case 72: // battery voltage (V*100)
		t.Battery = byte(val)
	case 66, 67: // movement / ignition 0-1
		t.ACC = val == 1
	default:
		// B5a: fuel sensor IO elements (configurable via env).
		switch id {
		case uint16(ioFuelLevel):
			f := float64(val)
			t.FuelLevel = &f
		case uint16(ioFuelUsed):
			f := float64(val)
			t.FuelVolume = &f
		case uint16(ioFuelTemp):
			f := float64(int16(val)) // signed temperature
			t.FuelTempC = &f
		}
	}
}

// parseCodec8ERecord mendekode satu AVL record Codec 8 Extended (0x8E, family
// FMB/FMM) mengikuti struktur resmi Teltonika (wiki "Codec", tabel perbedaan
// Codec 8 vs Codec 8 Extended — AVL IO ID dan semua count 2 byte):
//
//	Base 28 byte : timestamp(8) + priority(1) + GPS element(15) +
//	               Event IO ID(2) + N of Total IO(2)
//	Grup IO      : N1(2) elemen 1-byte · N2(2) 2-byte · N4(2) 4-byte ·
//	               N8(2) 8-byte · NX(2) variabel
//	  elemen grup tetap   : IO ID(2) + value(elemLen)
//	  elemen grup variabel: IO ID(2) + length(2) + value(length)
//
// Count & IO ID big-endian (konsisten field Teltonika lainnya). Offset wajib
// diverifikasi ulang terhadap capture perangkat 0x8E nyata sebelum produksi
// (GAP PRD Module 1a). Record ter-truncasi mengembalikan error (bukan panic);
// CRC akhir paket menolak paket rusak.
func parseCodec8ERecord(b []byte) (models.TelemetryMessage, int, error) {
	var t models.TelemetryMessage
	if len(b) < 28 {
		return t, 0, errors.New("short codec8e record")
	}
	ms := int64(binary.BigEndian.Uint64(b[0:8]))
	t.Lon = float64(int32(binary.BigEndian.Uint32(b[9:13]))) / 1e7
	t.Lat = float64(int32(binary.BigEndian.Uint32(b[13:17]))) / 1e7
	t.Timestamp = ms / 1000
	t.Heading = int16(binary.BigEndian.Uint16(b[19:21]))
	t.Satellites = b[21]
	t.Speed = float64(binary.BigEndian.Uint16(b[22:24])) / 10.0 // 10*km/h
	// Event IO ID (b[24:26]) + N of Total IO (b[26:28]) di-skip: jumlah total
	// hanya informasi; struktur dibaca per-grup agar tidak misalign.

	off := 28
	for _, elemLen := range []int{1, 2, 4, 8} {
		if off+2 > len(b) {
			return t, 0, errors.New("short codec8e io group count")
		}
		n := int(binary.BigEndian.Uint16(b[off : off+2]))
		off = parseCodec8EFixedGroup(b, off+2, &t, n, elemLen)
	}
	if off+2 > len(b) {
		return t, 0, errors.New("short codec8e var group count")
	}
	n := int(binary.BigEndian.Uint16(b[off : off+2]))
	off = parseCodec8EVarGroup(b, off+2, &t, n)
	return t, off, nil
}

// parseCodec8EFixedGroup membaca `count` elemen IO berukuran tetap 1/2/4/8 byte
// (IO ID 2 byte + value) dan mengembalikan offset setelah grup. Elemen yang
// melebihi sisa payload menghentikan grup (CRC paket yang menolak kerusakan).
func parseCodec8EFixedGroup(b []byte, off int, t *models.TelemetryMessage, count int, elemLen int) int {
	for j := 0; j < count; j++ {
		if off+2+elemLen > len(b) {
			break
		}
		id := binary.BigEndian.Uint16(b[off : off+2])
		var val uint64
		for k := 0; k < elemLen; k++ {
			val = val<<8 | uint64(b[off+2+k])
		}
		off += 2 + elemLen
		applyTeltonikaIO(t, id, val)
	}
	return off
}

// parseCodec8EVarGroup membaca `count` elemen IO variabel (IO ID 2 byte +
// length 2 byte + value) dan mengembalikan offset setelah grup.
func parseCodec8EVarGroup(b []byte, off int, t *models.TelemetryMessage, count int) int {
	for j := 0; j < count; j++ {
		if off+4 > len(b) {
			break
		}
		id := binary.BigEndian.Uint16(b[off : off+2])
		l := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
		off += 4
		if off+l > len(b) {
			break
		}
		var val uint64
		for k := 0; k < l; k++ {
			val = val<<8 | uint64(b[off])
			off++
		}
		applyTeltonikaIO(t, id, val)
	}
	return off
}
