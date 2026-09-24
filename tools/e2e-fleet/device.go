package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"time"
)

// crcITU computes the GT06 CRC-ITU (CRC-16/X-25) bitwise — deliberately
// independent of the ingestion implementation so a bug in one path cannot mask a
// bug in the other (same rationale as tools/e2e).
func crcITU(data []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}

// buildFrame assembles start || length || proto || content || crc(2) || stop.
func buildFrame(proto byte, content []byte) []byte {
	body := append([]byte{proto}, content...)
	frame := []byte{0x78, 0x78, byte(1 + len(content))}
	frame = append(frame, body...)
	sum := crcITU(frame[2:])
	return append(frame, byte(sum>>8), byte(sum), 0x0D, 0x0A)
}

// loginFrame builds the GT06 login packet for a 15-digit IMEI.
func loginFrame(imei string) []byte {
	content := make([]byte, 15, 17)
	copy(content, []byte(imei))
	content = append(content, 0x00, 0x01)
	return buildFrame(0x01, content)
}

// positionFrame builds a 0x22 position packet with a valid GPS fix, the device
// timestamp and the ACC byte the B6/B7 accumulators rely on.
func positionFrame(t time.Time, lat, lon, speedKmh float64, acc bool) []byte {
	content := []byte{
		byte(t.Year() - 2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()), 0x0A,
	}
	content = appendUint32(content, uint32(math.Abs(lat)*1800000))
	content = appendUint32(content, uint32(math.Abs(lon)*1800000))
	content = append(content, byte(speedKmh/1.852))

	status := uint16(0x1000)
	if lat >= 0 {
		status |= 0x0400
	}
	if lon < 0 {
		status |= 0x0800
	}
	content = append(content, byte(status>>8), byte(status))
	content = append(content, 0x01, 0xF4, 0x01, 0x00, 0x01, 0x00, 0x01, 0x23)
	if acc {
		content = append(content, 0x01)
	} else {
		content = append(content, 0x00)
	}
	content = append(content, 0x00, 0x00)
	content = appendUint32(content, 0)
	return buildFrame(0x22, content)
}

// fuelFrame builds the positionless fuel sentence (0x94/0x0D) used for the
// "positionless frames never move the odometer" assertion.
func fuelFrame(t time.Time, heightCM float64) []byte {
	content := []byte{
		0x0D,
		byte(t.Year() - 2000), byte(t.Month()), byte(t.Day()),
		byte(t.Hour()), byte(t.Minute()), byte(t.Second()),
	}
	sentence := fmt.Sprintf("!AIOIL,02,%07.3f,%06.3f,519J,0200,027.140,0,00,9F", heightCM, 25.4)
	content = append(content, sentence...)
	content = append(content, 0x00, 0x01)
	return buildFrame(0x94, content)
}

// appendUint32 appends a big-endian uint32.
func appendUint32(b []byte, v uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	return append(b, buf[:]...)
}

// readFrame reads one framed reply (mainly to verify the server ACKs).
func readFrame(r *bufio.Reader) ([]byte, error) {
	start, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	second, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if start != 0x78 || second != 0x78 {
		return nil, fmt.Errorf("unexpected start bytes 0x%02x 0x%02x", start, second)
	}
	lengthByte, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	rest := make([]byte, int(lengthByte)+4)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, err
	}
	return append([]byte{start, second, lengthByte}, rest...), nil
}

// routeFix is one planned position frame.
type routeFix struct {
	offset time.Duration
	lat    float64
	lon    float64
	speed  float64
}

// routePlan is the synthetic drive of the harness: one moving leg to a stop, a
// standstill longer than TRIP_MIN_STOP_SECONDS, then a short second leg so the
// trip closes deterministically (FR-2.6).
type routePlan struct {
	// points are the moving fixes with their device-time offsets.
	points []routeFix
	// stops are the stationary fixes (grace 30 s → min stop 60 s).
	stops []routeFix
	// resume is the final moving fix that closes the trip.
	resume routeFix
	// jump is the GPS-jump frame the FR-2.5 guard must discard.
	jump routeFix
}

// plannedRoute builds the drive used by the assertions: 0.001° of latitude is
// ≈110.5 m, so the moving legs total ≈0.33 km while the stationary fixes add ≈0.
func plannedRoute() routePlan {
	return routePlan{
		points: []routeFix{
			{offset: 0, lat: -6.2000, lon: 106.8000, speed: 40},
			{offset: 20 * time.Second, lat: -6.2010, lon: 106.8000, speed: 45},
			{offset: 40 * time.Second, lat: -6.2020, lon: 106.8000, speed: 30},
		},
		stops: []routeFix{
			{offset: 60 * time.Second, lat: -6.2020, lon: 106.8000, speed: 0},
			{offset: 190 * time.Second, lat: -6.2020, lon: 106.8000, speed: 0},
		},
		resume: routeFix{offset: 200 * time.Second, lat: -6.2030, lon: 106.8000, speed: 35},
		jump:   routeFix{offset: 220 * time.Second, lat: -6.3030, lon: 106.8000, speed: 30},
	}
}

// expectedDistanceKM is the Haversine length of the planned moving legs (the
// portion the odometer must accumulate).
func (p routePlan) expectedDistanceKM() float64 {
	pts := append([]routeFix{}, p.points...)
	pts = append(pts, p.resume)
	var total float64
	for i := 1; i < len(pts); i++ {
		total += haversineKM([2]float64{pts[i-1].lat, pts[i-1].lon},
			[2]float64{pts[i].lat, pts[i].lon})
	}
	return total
}

// stopDurationSeconds is the planned standstill length (stop end - stop start).
func (p routePlan) stopDurationSeconds() int {
	return int((p.stops[len(p.stops)-1].offset - p.stops[0].offset).Seconds())
}

// haversineKM mirrors PRD FR-2.5 (radius 6371 km) for the expectation.
func haversineKM(a, b [2]float64) float64 {
	const r = 6371.0
	const rad = math.Pi / 180
	dLat := (b[0] - a[0]) * rad
	dLon := (b[1] - a[1]) * rad
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a[0]*rad)*math.Cos(b[0]*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(h))
}

// driveRoute logs in and replays the planned drive on the GT06 listener. Frames
// carry DEVICE timestamps spread over the plan, so the trip machine observes the
// intended durations without the harness waiting minutes.
func driveRoute(opt options, base time.Time, plan routePlan) error {
	conn, err := net.DialTimeout("tcp", opt.tcpAddr, opt.timeout)
	if err != nil {
		return fmt.Errorf("dial ingestion-tcp %s: %w", opt.tcpAddr, err)
	}
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)

	if _, err := conn.Write(loginFrame(opt.imei)); err != nil {
		return fmt.Errorf("write login: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(opt.timeout))
	reply, err := readFrame(reader)
	if err != nil {
		return fmt.Errorf("read login reply: %w", err)
	}
	if len(reply) < 6 || reply[3] != 0x01 || reply[4] != 0x00 {
		return fmt.Errorf("login rejected: % x", reply)
	}

	writePosition := func(f routeFix, acc bool) error {
		if _, werr := conn.Write(positionFrame(base.Add(f.offset), f.lat, f.lon, f.speed, acc)); werr != nil {
			return fmt.Errorf("write position @%s: %w", f.offset, werr)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = readFrame(reader)
		return nil
	}

	for _, f := range plan.points {
		if err := writePosition(f, true); err != nil {
			return err
		}
	}
	// A positionless fuel sentence in between must not disturb the accumulators.
	if _, err := conn.Write(fuelFrame(base.Add(50*time.Second), 60)); err != nil {
		return fmt.Errorf("write fuel frame: %w", err)
	}
	time.Sleep(300 * time.Millisecond)
	for _, f := range plan.stops {
		if err := writePosition(f, true); err != nil {
			return err
		}
	}
	if err := writePosition(plan.resume, true); err != nil {
		return err
	}
	// GPS jump (≈11 km): FR-2.5 must discard it, so the odometer delta stays at
	// the planned route length instead of growing by 11 km.
	if err := writePosition(plan.jump, true); err != nil {
		return err
	}
	// Give ingestion a moment to read the last frame before the socket closes.
	time.Sleep(500 * time.Millisecond)
	return nil
}
