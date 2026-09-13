# 5. Implementation Guide

## Protocol Implementation Checklist

Saat mengimplementasikan protokol baru, gunakan checklist ini:

```
[ ] 1. Pahami frame structure (start byte, length, checksum, stop byte)
[ ] 2. Identifikasi login/IMEI packet type
[ ] 3. Identifikasi command types yang didukung
[ ] 4. Parse position payload (lat, lon, speed, course, timestamp)
[ ] 5. Handle heartbeat (usually just ACK back)
[ ] 6. Handle alarm (if applicable)
[ ] 7. Build ACK/response packet
[ ] 8. Write FrameDecoder (delimit dari TCP stream)
[ ] 9. Write ProtocolDecoder (parse ke TelemetryMessage)
[ ] 10. Write connection handler (orchestrate read-parse-publish)
[ ] 11. Add metrics (packets_total, parsed_total, rejected_total)
[ ] 12. Write unit test dengan sample frame dari dokumentasi
[ ] 13. Integrasi test dengan device/simulator
```

## Go FrameDecoder Pattern

```go
// Pattern untuk binary protocol
func readFrame(r *bufio.Reader) ([]byte, error) {
    // 1. Read start bytes
    b0, _ := r.ReadByte()
    b1, _ := r.ReadByte()
    
    // 2. Validate start marker
    if b0 != 0x24 || b1 != 0x24 {  // contoh: Meiligao
        return nil, fmt.Errorf("bad start")
    }
    
    // 3. Read length
    lenBuf := make([]byte, 2)
    io.ReadFull(r, lenBuf)
    length := binary.BigEndian.Uint16(lenBuf)
    
    // 4. Read remaining frame
    frame := make([]byte, length)
    io.ReadFull(r, frame)
    
    // 5. Validate checksum
    if !validateChecksum(frame) {
        return nil, fmt.Errorf("checksum mismatch")
    }
    
    return frame, nil
}
```

## Go ProtocolDecoder Pattern

```go
// Pattern untuk text protocol (NMEA-based)
func parseXexun(sentence string) (models.TelemetryMessage, bool) {
    // 1. Cari GPRMC/GNRMC
    idx := strings.Index(sentence, "GPRMC")
    if idx == -1 {
        idx = strings.Index(sentence, "GNRMC")
    }
    if idx == -1 {
        return models.TelemetryMessage{}, false
    }
    
    // 2. Parse NMEA fields
    fields := strings.Split(sentence[idx:], ",")
    if len(fields) < 12 {
        return models.TelemetryMessage{}, false
    }
    
    // 3. Build TelemetryMessage
    var t models.TelemetryMessage
    t.Timestamp = parseTime(fields[0])
    t.Lat = parseLat(fields[2], fields[3])
    t.Lon = parseLon(fields[4], fields[5])
    t.Speed = parseSpeed(fields[6])
    t.Heading = parseCourse(fields[7])
    
    // 4. Extract IMEI
    imeiIdx := strings.Index(sentence, "imei:")
    if imeiIdx != -1 {
        t.IMEI = sentence[imeiIdx+5 : imeiIdx+20]
    }
    
    return t, true
}
```

## Connection Handler Pattern

```go
// Pattern untuk handle device connection
func handleMeiligao(c net.Conn) {
    defer connClose(c, "meiligao")
    r := bufio.NewReader(c)
    var imei string
    
    for {
        _ = c.SetReadDeadline(time.Now().Add(models.IdleTimeout))
        
        // 1. Read frame
        frame, err := readMeiligaoFrame(r)
        if err != nil {
            return
        }
        
        // 2. Parse command
        cmd := binary.BigEndian.Uint16(frame[0:2])
        payload := frame[2:]
        
        switch cmd {
        case 0x5001: // Login
            imei = string(payload[:7])
            // Resolve device, send ACK
            dev, err := tenantMgr.ResolveDeviceByIMEI(ctx, imei)
            if err != nil {
                rejectedTotal.WithLabelValues("unauthorised").Inc()
                return
            }
            writeMeiligaoAck(c, 0x9999, frame)
            
        case 0x5002: // Position
            if imei == "" {
                continue
            }
            tele, ok := parseMeiligaoPosition(payload)
            if !ok {
                continue
            }
            tele.IMEI = imei
            publishTelemetry(tele)
            
        case 0x5003: // Heartbeat
            writeMeiligaoAck(c, 0x9999, frame)
        }
    }
}
```

## Sample Frame Repository

Untuk testing, simpan sample frame dari setiap protokol di:
```
backend/services/ingestion-tcp/controllers/testdata/<protocol>/
    login.bin
    position.bin
    heartbeat.bin
    alarm.bin
```

## Testing Pattern

```go
func TestParseMeiligaoPosition(t *testing.T) {
    // Sample frame dari dokumentasi vendor
    frame := []byte{
        0x24, 0x24, // start
        0x00, 0x20, // length
        0x50, 0x02, // command (position)
        // ... payload ...
        0x0D, 0x0A, // stop
    }
    
    tele, ok := parseMeiligaoPosition(frame[4 : len(frame)-4])
    if !ok {
        t.Fatal("failed to parse position")
    }
    
    if tele.Lat == 0 && tele.Lon == 0 {
        t.Error("lat/lon should not be zero")
    }
}
```