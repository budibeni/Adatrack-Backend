// =====================================================================
// tel8e — sender/simulator Teltonika **Codec 8 Extended (0x8E)** untuk
// smoke E2E pipeline:
//
//	device → ingestion-tcp (TCP :TELTONIKA_TCP_PORT) → NATS
//	         → (worker-live → Redis) + (worker-persistence → fuel_logs/telemetry_logs)
//
// Struktur frame 1:1 dengan parser controllers/codec8e.go (parseCodec8ERecord)
// mengikuti wiki Teltonika "Codec":
//   - login IMEI : 2-byte BE length + IMEI ASCII → ACK 1 byte 0x01
//   - AVL packet : preamble 4×0x00 + data length 4-byte BE + payload
//     payload    : 0x8E | Number of Data(1) | record | CRC-16/IBM(2 LE) | Number of Data(1)
//     record     : base 28 byte (timestamp8+priority1+GPS15+EventIO2+Ntotal2)
//                  + N1(2)[id2|v1] + N2(2)[id2|v2] + N4(2) + N8(2) + NX(2)[id2|len2|value]
//   - ACK AVL    : 4-byte BE jumlah record diterima
//
// IO: battery id 72 (1B), ACC id 67 (2B), fuel level id 86 (NX, 2B) — fuel
// menurun per frame (default -40) sehingga worker-alert dapat mendeteksi
// FUEL_DROP bila ikut dijalankan dengan konfigurasi aktif.
//
// Cara pakai:
//	go build -o tel8e-sender ./cmd/tel8e
//	./tel8e-sender -host 127.0.0.1:9011 -frames 10 -rate 2
// =====================================================================
package main

import (
	"encoding/binary"
	"flag"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

var (
	target   = flag.String("host", "127.0.0.1:9011", "ingestion-tcp Teltonika address")
	imeisArg = flag.String("imeis", "864201040512345,864201040512346,864201040512347", "IMEI terdaftar (koma)")
	frames   = flag.Int("frames", 10, "jumlah AVL packet (1 record/packet) per IMEI")
	rate     = flag.Int("rate", 2, "packet/sec per IMEI")
	lat0     = flag.Float64("lat", -6.2088, "latitude awal")
	lon0     = flag.Float64("lon", 106.8456, "longitude awal")
	speed0   = flag.Float64("speed", 45, "kecepatan km/h")
	fuel0    = flag.Float64("fuel", 500, "fuel level awal (literal IO 86)")
	fuelStep = flag.Float64("fuel-step", -40, "perubahan fuel per frame (negatif = drop)")
)

func main() {
	flag.Parse()
	imeis := strings.Split(*imeisArg, ",")
	log.Printf("tel8e: host=%s imeis=%d frames/imei=%d rate=%d/s fuel0=%.0f step=%.0f",
		*target, len(imeis), *frames, *rate, *fuel0, *fuelStep)

	var totSent, totAcc, totErr int64
	for _, raw := range imeis {
		imei := strings.TrimSpace(raw)
		if imei == "" {
			continue
		}
		s, a, e := sendSession(imei)
		totSent += s
		totAcc += a
		totErr += e
	}
	log.Printf("tel8e SELESAI: sent=%d accepted=%d writeErr=%d", totSent, totAcc, totErr)
}

// sendSession membuka satu koneksi per IMEI: login + kirim N AVL packet 0x8E.
func sendSession(imei string) (sent, accepted, errs int64) {
	conn, err := net.DialTimeout("tcp", *target, 5*time.Second)
	if err != nil {
		log.Printf("[%s] dial error: %v", imei, err)
		return 0, 0, 1
	}
	defer conn.Close()

	// Login IMEI: 2-byte BE length + ASCII, server membalas 1 byte 0x01.
	login := make([]byte, 2, 2+len(imei))
	binary.BigEndian.PutUint16(login[:2], uint16(len(imei)))
	login = append(login, imei...)
	if _, err := conn.Write(login); err != nil {
		log.Printf("[%s] write login error: %v", imei, err)
		return 0, 0, 1
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var ack [1]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		// Koneksi ditutup tanpa ACK = IMEI ditolak anti-spoofing (tidak terdaftar).
		log.Printf("[%s] login ACK tidak diterima (%v) — IMEI terdaftar?", imei, err)
		return 0, 0, 1
	}
	if ack[0] != 0x01 {
		log.Printf("[%s] login ACK tidak dikenal: 0x%02X", imei, ack[0])
		return 0, 0, 1
	}
	log.Printf("[%s] login ACK 0x01 OK", imei)

	fuel := *fuel0
	for i := 0; i < *frames; i++ {
		pkt := build8EPacket(time.Now().UTC(),
			*lat0+float64(i)*1e-4, *lon0+float64(i)*1e-4, *speed0, fuel)
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := conn.Write(pkt); err != nil {
			log.Printf("[%s] write frame %d error: %v", imei, i, err)
			errs++
			break
		}
		sent++

		// ACK server: 4-byte BE jumlah record diterima.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		var ackN [4]byte
		if _, err := io.ReadFull(conn, ackN[:]); err != nil {
			log.Printf("[%s] read ACK frame %d error: %v", imei, i, err)
			errs++
			break
		}
		accepted += int64(binary.BigEndian.Uint32(ackN[:]))

		fuel += *fuelStep
		if fuel < 0 {
			fuel = 0
		}
		if *rate > 0 {
			time.Sleep(time.Second / time.Duration(*rate))
		}
	}
	log.Printf("[%s] selesai: sent=%d accepted=%d fuel_akhir=%.0f", imei, sent, accepted, fuel)
	return sent, accepted, errs
}

// build8EPacket: preamble 4×0 + data length 4 BE + payload 0x8E (1 record).
func build8EPacket(ts time.Time, lat, lon, speed, fuel float64) []byte {
	rec := make([]byte, 28, 64)
	binary.BigEndian.PutUint64(rec[0:8], uint64(ts.UnixMilli()))
	rec[8] = 0 // priority: low
	binary.BigEndian.PutUint32(rec[9:13], uint32(int32(lon*1e7)))
	binary.BigEndian.PutUint32(rec[13:17], uint32(int32(lat*1e7)))
	binary.BigEndian.PutUint16(rec[17:19], 50) // altitude
	binary.BigEndian.PutUint16(rec[19:21], 90) // angle
	rec[21] = 12                               // satellites
	binary.BigEndian.PutUint16(rec[22:24], uint16(speed*10))
	binary.BigEndian.PutUint16(rec[24:26], 0) // Event IO ID: none
	binary.BigEndian.PutUint16(rec[26:28], 3) // N of Total IO = N1+N2+NX

	// N1 (1-byte IO): 1 elemen — id 72 battery (level 0-100).
	rec = append(rec, 0, 1)
	rec = append(rec, 0, 72, 96)
	// N2 (2-byte IO): 1 elemen — id 67 ACC ON.
	rec = append(rec, 0, 1)
	rec = append(rec, 0, 67, 0, 1)
	// N4 / N8: kosong.
	rec = append(rec, 0, 0)
	rec = append(rec, 0, 0)
	// NX (variabel): 1 elemen — id 86 fuel level, length 2 byte.
	f := uint16(fuel)
	rec = append(rec, 0, 1)
	rec = append(rec, 0, 86, 0, 2, byte(f>>8), byte(f))

	// Payload: codec 0x8E + jumlah data + record + CRC-16/IBM (2 LE) + jumlah data.
	p := append([]byte{0x8E, 0x01}, rec...)
	crc := crc16IBM(p)
	p = append(p, byte(crc), byte(crc>>8), 0x01)

	// Packet: preamble 4×0x00 + data length 4-byte BE (codec s/d Number of Data 2).
	pkt := make([]byte, 8, 8+len(p))
	binary.BigEndian.PutUint32(pkt[0:4], 0)
	binary.BigEndian.PutUint32(pkt[4:8], uint32(len(p)))
	return append(pkt, p...)
}

// crc16IBM — CRC-16/IBM (poly 0xA001 reflected, init 0xFFFF) identik dengan
// teltonikaCRC16 di controllers (spesifikasi Teltonika Codec 8/8E).
func crc16IBM(data []byte) uint16 {
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

