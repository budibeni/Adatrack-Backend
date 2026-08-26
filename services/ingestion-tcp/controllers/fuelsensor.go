package controllers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"ajb_gps/ingestion-tcp/models"
)

// ProtoFuelSensor is the Information Type byte within the Information
// Transmission Packet (0x94) that carries fuel-sensor data (v3.1 §10.1, "0D").
const ProtoFuelSensor = 0x0D

// ParseInfoTransmit dispatches an Information Transmission Packet (0x94)
// payload to the sub-protocol handler keyed by its Information Type byte.
// data is packet.Data (already after the protocol number 0x94).
func ParseInfoTransmit(data []byte) (models.TelemetryMessage, bool) {
	if len(data) == 0 {
		return models.TelemetryMessage{}, false
	}
	switch data[0] {
	case ProtoFuelSensor:
		return ParseFuelSensor(data)
	default:
		return models.TelemetryMessage{}, false
	}
}

// ParseFuelSensor parses a GT06 fuel-sensor data block (Information Type 0x0D
// inside the 0x94 Information Transmission Packet, v3.1 §10.1 / "0D Fuel sensor
// data").
//
// Layout:
//
//	0x0D                      // Information Type (1 byte)
//	6-byte Date Time block     // year..second per GT06 date encoding
//	ASCII sensor string        // starts with "!AIOIL,"
//	(serial number, 2 bytes)   // present in the full frame; stripped here
//
// ASCII sensor string format (vendor sample):
//
//	!AIOIL,02,025.900,025.400,519J,0200,027.140,0,00,9F
//
// Fields (0-indexed):
//
//	0: !AIOIL (header)
//	1: device address (02)
//	2: liquid level output value (cm)
//	3: temperature
//	4: version info (e.g. 519J)
//	5: hardware version (e.g. 0200)
//	6: liquid level measurement value (cm) — actual fuel level
//	7: motion status
//	8: excitation waveform
//	9: check code
func ParseFuelSensor(data []byte) (models.TelemetryMessage, bool) {
	if len(data) < 8 { // type(1) + time(6) + at least 1 sensor byte
		return models.TelemetryMessage{}, false
	}
	if data[0] != ProtoFuelSensor {
		return models.TelemetryMessage{}, false
	}

	var t models.TelemetryMessage

	// Date Time block: 6 bytes starting at data[1].
	if ts, ok := ParseTime(data[1:7]); ok {
		t.Timestamp = ts.Unix()
	}

	// Sensor ASCII data: starts at data[7]. If there is a trailing 2-byte
	// serial number (present in the full frame), exclude it so the ASCII
	// parser does not choke on non-printable bytes.
	sensorStart := 7
	sensorEnd := len(data)
	if sensorEnd-sensorStart > 2 {
		sensorEnd -= 2
	}
	ascii := string(data[sensorStart:sensorEnd])

	fuelLevel, fuelTemp := parseAIOIL(ascii)
	if fuelLevel != nil {
		t.FuelLevel = fuelLevel
	}
	if fuelTemp != nil {
		t.FuelTempC = fuelTemp
	}

	return t, fuelLevel != nil || fuelTemp != nil
}

// parseAIOIL parses the "!AIOIL,..." ASCII sensor string and extracts:
//   - liquid level measurement value (field index 6, cm)
//   - temperature (field index 3)
func parseAIOIL(ascii string) (fuelLevel *float64, fuelTemp *float64) {
	fields := strings.Split(ascii, ",")
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "!AIOIL") {
		return nil, nil
	}

	// Temperature — field index 3.
	if len(fields) > 3 {
		if v, err := strconv.ParseFloat(fields[3], 64); err == nil {
			fuelTemp = &v
		}
	}

	// Liquid level measurement value — field index 6.
	if len(fields) > 6 {
		if v, err := strconv.ParseFloat(fields[6], 64); err == nil {
			fuelLevel = &v
		}
	}

	return fuelLevel, fuelTemp
}

// ---------------------------------------------------------------------------
// Metrics & telemetry helpers
// ---------------------------------------------------------------------------

var fuelReadingsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "fuel_readings_total",
	Help: "Fuel sensor readings parsed per protocol (B5a)",
}, []string{"protocol"})

// publishFuelTelemetry marshals and publishes a fuel telemetry message,
// recording publish latency / errors (reuses the telemetry path).
func publishFuelTelemetry(t models.TelemetryMessage) error {
	data, err := json.Marshal(t)
	if err != nil {
		return err
	}
	company := t.CompanyCode
	if company == "" {
		company = "default"
	}
	subject := natsCli.Subject("raw", t.IMEI)
	start := time.Now()
	err = natsCli.Publish(subject, data)
	natsPublishDuration.WithLabelValues(company, natsCli.Subject("raw")).
		Observe(float64(time.Since(start).Microseconds()) / 1000.0)
	if err != nil {
		natsPublishErrors.WithLabelValues(company).Inc()
		return err
	}
	return nil
}

// fuelSensorError increments and logs a fuel-sensor parse error.
func fuelSensorError(imei, reason string) {
	rejectedTotal.WithLabelValues("fuel_parse").Inc()
	slog.Warn("fuel sensor parse error", "imei", imei, "reason", reason)
}

var _ = fmt.Sprintf // keep fmt import for future diagnostic messages
