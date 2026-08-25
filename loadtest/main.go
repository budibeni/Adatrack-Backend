// =====================================================================
// loadtest — load test pipeline B1 (PRD GAP #6):
//
//	device → ingestion-tcp (TCP GT06) → NATS → (worker-live → Redis)
//	                                             + (worker-persistence → MySQL)
//
// Frame GT06 yang dihasilkan VALID terhadap parser di backend/services/
// ingestion-tcp/controllers (bukan sekadar menyerupai):
//   - start 0x78 0x78, length 1-byte, protocol+content, CRC-ITU 2-byte
//     big-endian (tabel+algoritma sama dgn controllers/crc16.go), stop 0d 0a
//   - date-time plain-heks (nilai byte == digit desimal → GT06_DATE_BCD=false)
//   - lat/lon integer 4-byte big-endian = (deg*60+min) * 30000
//     (decoder parser: raw / 1_800_000)
//   - serial 2-byte = 2 byte TERAKHIR content (untuk ACK parser)
//
// IMEI default = 3 IMEI terdaftar di master.vehicle_imei_map (seed DEV001);
// bisa di-override dengan -imeis (comma-separated). IMEI yang tidak terdaftar
// memang ditolak oleh anti-spoofing ingestion (path reject ikut teruji).
//
// Cara pakai:
//
//	go build -o loadtest .
//	./loadtest -devices 50 -rate 20            # 1000 msg/s
//	./loadtest -devices 200 -rate 10 -duration 60s -imeis "I1,I2,I3"
//
// =====================================================================
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	target   = flag.String("host", "127.0.0.1:9000", "ingestion-tcp GT06 address")
	devices  = flag.Int("devices", 100, "jumlah koneksi perangkat GPS simulasi")
	msgRate  = flag.Int("rate", 20, "msg/sec per device")
	duration = flag.Duration("duration", 30*time.Second, "durasi test")
	imeisArg = flag.String("imeis", "", "daftar IMEI terdaftar dipisah koma (default: seed DEV001)")

	msgs     int64
	writeErr int64
	started  time.Time
)

// Seeded & terdaftar di master.vehicle_imei_map (company DEV001).
var defaultIMEIs = []string{
	"864201040512345",
	"864201040512346",
	"864201040512347",
}

func main() {
	flag.Parse()
	imeis := parseIMEIs(*imeisArg)
	if *msgRate < 1 {
		*msgRate = 1
	}
	log.Printf("Load test: %d device x %d msg/s = %d msg/s target, durasi %v, imeis=%d",
		*devices, *msgRate, *devices**msgRate, *duration, len(imeis))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	stop := make(chan struct{})

	var wg sync.WaitGroup
	started = time.Now()
	for d := 0; d < *devices; d++ {
		wg.Add(1)
		go func(devID int) {
			defer wg.Done()
			simulateDevice(devID, imeis, stop)
		}(d)
	}

	// Progress ticker 5 detik.
	go func() {
		tk := time.NewTicker(5 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-tk.C:
				el := time.Since(started).Seconds()
				m := atomic.LoadInt64(&msgs)
				fmt.Printf("  [progress] %d msgs, %.0f msg/s (writeErr=%d)\n", m, float64(m)/el, atomic.LoadInt64(&writeErr))
			case <-stop:
				return
			}
		}
	}()

	// Berhenti otomatis setelah -duration ATAU menerima sinyal (SIGINT/SIGTERM).
	// Tanpa menunggu sinyal: -duration harus memutuskan akhir test sendiri.
	var stopOnce sync.Once
	expired := make(chan struct{})
	go func() {
		select {
		case <-time.After(*duration):
			stopOnce.Do(func() { close(stop) })
			close(expired)
		case <-sig:
			// Sinyal diproses di bawah; jangan close(expired) dari sini.
		}
	}()
	select {
	case <-sig:
		stopOnce.Do(func() { close(stop) })
	case <-expired:
	}
	wg.Wait()

	el := time.Since(started).Seconds()
	total := atomic.LoadInt64(&msgs)
	errCnt := atomic.LoadInt64(&writeErr)
	fmt.Printf("\n===== Ringkasan =====\n")
	fmt.Printf("Durasi: %.2f s\n", el)
	fmt.Printf("Total frame terkirim: %d\n", total)
	fmt.Printf("Throughput: %.2f msg/s\n", float64(total)/el)
	fmt.Printf("Write errors: %d\n", errCnt)
	if errCnt > 0 {
		fmt.Println("WARNING: ada write error! Periksa ingestion-tcp / konfigurasi.")
		os.Exit(1)
	}
}

func parseIMEIs(arg string) []string {
	if strings.TrimSpace(arg) == "" {
		return defaultIMEIs
	}
	parts := strings.Split(arg, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); len(s) >= 15 {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		log.Fatal("imeis: tidak ada IMEI valid (min 15 digit)")
	}
	return out
}

func simulateDevice(devID int, imeis []string, stop chan struct{}) {
	conn, err := net.DialTimeout("tcp", *target, 5*time.Second)
	if err != nil {
		log.Printf("device %d: dial failed: %v", devID, err)
		return
	}
	defer conn.Close()

	imei := imeis[devID%len(imeis)]
	if _, err := conn.Write(buildLogin(imei)); err != nil {
		log.Printf("device %d: login write failed: %v", devID, err)
		return
	}

	interval := time.Second / time.Duration(*msgRate)
	if interval < 1*time.Millisecond {
		interval = 1 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	seq := 0
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			seq++
			frame := buildPosition(time.Now(), seq)
			if _, err := conn.Write(frame); err != nil {
				atomic.AddInt64(&writeErr, 1)
				continue
			}
			atomic.AddInt64(&msgs, 1)
		}
	}
}

// =====================================================================
// GT06 wire builders — harus 1:1 dengan controllers/parser.go + crc16.go
// =====================================================================

// buildLogin: proto 0x01 + 15-byte IMEI + terminal type (2) + timezone (4).
func buildLogin(imei string) []byte {
	payload := append([]byte{0x01}, []byte(imei)...)
	payload = append(payload, 0x81, 0x60)             // terminal type
	payload = append(payload, 0x00, 0x00, 0x01, 0x00) // timezone
	return buildFrame(payload)
}

// buildPosition: proto 0x22 (full location).
// content: date(6) sats(1) lat(4) lon(4) speed(1) course(2) LBS(8) acc(1)
//
//	serial(2)  = 29 byte.
func buildPosition(t time.Time, seq int) []byte {
	rawLat := uint32(math.Round(6.2088 * 1_800_000)) // ~6.2088°N (bit north set)
	rawLon := uint32(math.Round(106.8456 * 1_800_000))
	speed := byte(30 + seq%70)
	course := uint16(seq % 360)

	var content []byte
	// Tanggal plain-hex: nilai byte == digit desimal (matching parser default).
	content = append(content, byte(t.Year()-2000), byte(int(t.Month())), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()))
	content = append(content, 11<<4|11) // hi-nibble info len + 11 satelit (low nibble)
	var latB, lonB [4]byte
	binary.BigEndian.PutUint32(latB[:], rawLat)
	binary.BigEndian.PutUint32(lonB[:], rawLon)
	content = append(content, latB[:]...)
	content = append(content, lonB[:]...)
	content = append(content, speed)

	// Course & status: 0x1000 positioned, 0x0400 north, low 10 bit = course,
	// bit east-lon 0x0800 cleared (= East).
	var cs uint16 = 0x1000 | 0x0400 | (course & 0x03FF)
	content = append(content, byte(cs>>8), byte(cs))

	// Network tail LBS: MCC(2) MNC(1) LAC(2) CellID(3) = 8 byte.
	content = append(content, 0x01, 0x00, 0x00, 0x28, 0x7D, 0x00, 0x1F, 0xB8)
	content = append(content, 0x01) // ACC high

	// 2-byte serial di AKHIR content (untuk parsing ACK).
	content = append(content, byte(seq>>8), byte(seq))

	return buildFrame(append([]byte{0x22}, content...))
}

// buildFrame: 0x78 0x78 | len | payload(protocol+content) | crc(2 BE) | 0d 0a.
func buildFrame(payload []byte) []byte {
	frame := []byte{0x78, 0x78, byte(len(payload))}
	frame = append(frame, payload...)
	sum := crc16(frame[2:]) // CRC atas [len, protocol, content]
	frame = append(frame, byte(sum>>8), byte(sum), 0x0d, 0x0a)
	return frame
}

// =====================================================================
// CRC-ITU — tabel & algoritma identik dengan controllers/crc16.go (docs v3.1)
// =====================================================================

var crc16Table = [256]uint16{
	0x0000, 0x1189, 0x2312, 0x329B, 0x4624, 0x57AD, 0x6536, 0x74BF,
	0x8C48, 0x9DC1, 0xAF5A, 0xBED3, 0xCA6C, 0xDBE5, 0xE97E, 0xF8F7,
	0x1081, 0x0108, 0x3393, 0x221A, 0x56A5, 0x472C, 0x75B7, 0x643E,
	0x9CC9, 0x8D40, 0xBFDB, 0xAE52, 0xDAED, 0xCB64, 0xF9FF, 0xE876,
	0x2102, 0x308B, 0x0210, 0x1399, 0x6726, 0x76AF, 0x4434, 0x55BD,
	0xAD4A, 0xBCC3, 0x8E58, 0x9FD1, 0xEB6E, 0xFAE7, 0xC87C, 0xD9F5,
	0x3183, 0x200A, 0x1291, 0x0318, 0x77A7, 0x662E, 0x54B5, 0x453C,
	0xBDCB, 0xAC42, 0x9ED9, 0x8F50, 0xFBEF, 0xEA66, 0xD8FD, 0xC974,
	0x4204, 0x538D, 0x6116, 0x709F, 0x0420, 0x15A9, 0x2732, 0x36BB,
	0xCE4C, 0xDFC5, 0xED5E, 0xFCD7, 0x8868, 0x99E1, 0xAB7A, 0xBAF3,
	0x5285, 0x430C, 0x7197, 0x601E, 0x14A1, 0x0528, 0x37B3, 0x263A,
	0xDECD, 0xCF44, 0xFDDF, 0xEC56, 0x98E9, 0x8960, 0xBBFB, 0xAA72,
	0x6306, 0x728F, 0x4014, 0x519D, 0x2522, 0x34AB, 0x0630, 0x17B9,
	0xEF4E, 0xFEC7, 0xCC5C, 0xDDD5, 0xA96A, 0xB8E3, 0x8A78, 0x9BF1,
	0x7387, 0x620E, 0x5095, 0x411C, 0x35A3, 0x242A, 0x16B1, 0x0738,
	0xFFCF, 0xEE46, 0xDCDD, 0xCD54, 0xB9EB, 0xA862, 0x9AF9, 0x8B70,
	0x8408, 0x9581, 0xA71A, 0xB693, 0xC22C, 0xD3A5, 0xE13E, 0xF0B7,
	0x0840, 0x19C9, 0x2B52, 0x3ADB, 0x4E64, 0x5FED, 0x6D76, 0x7CFF,
	0x9489, 0x8500, 0xB79B, 0xA612, 0xD2AD, 0xC324, 0xF1BF, 0xE036,
	0x18C1, 0x0948, 0x3BD3, 0x2A5A, 0x5EE5, 0x4F6C, 0x7DF7, 0x6C7E,
	0xA50A, 0xB483, 0x8618, 0x9791, 0xE32E, 0xF2A7, 0xC03C, 0xD1B5,
	0x2942, 0x38CB, 0x0A50, 0x1BD9, 0x6F66, 0x7EEF, 0x4C74, 0x5DFD,
	0xB58B, 0xA402, 0x9699, 0x8710, 0xF3AF, 0xE226, 0xD0BD, 0xC134,
	0x39C3, 0x284A, 0x1AD1, 0x0B58, 0x7FE7, 0x6E6E, 0x5CF5, 0x4D7C,
	0xC60C, 0xD785, 0xE51E, 0xF497, 0x8028, 0x91A1, 0xA33A, 0xB2B3,
	0x4A44, 0x5BCD, 0x6956, 0x78DF, 0x0C60, 0x1DE9, 0x2F72, 0x3EFB,
	0xD68D, 0xC704, 0xF59F, 0xE416, 0x90A9, 0x8120, 0xB3BB, 0xA232,
	0x5AC5, 0x4B4C, 0x79D7, 0x685E, 0x1CE1, 0x0D68, 0x3FF3, 0x2E7A,
	0xE70E, 0xF687, 0xC41C, 0xD595, 0xA12A, 0xB0A3, 0x8238, 0x93B1,
	0x6B46, 0x7ACF, 0x4854, 0x59DD, 0x2D62, 0x3CEB, 0x0E70, 0x1FF9,
	0xF78F, 0xE606, 0xD49D, 0xC514, 0xB1AB, 0xA022, 0x92B9, 0x8330,
	0x7BC7, 0x6A4E, 0x58D5, 0x495C, 0x3DE3, 0x2C6A, 0x1EF1, 0x0F78,
}

func crc16(data []byte) uint16 {
	var fcs uint16 = 0xffff
	for _, b := range data {
		fcs = (fcs >> 8) ^ crc16Table[(fcs^uint16(b))&0xff]
	}
	return ^fcs
}
