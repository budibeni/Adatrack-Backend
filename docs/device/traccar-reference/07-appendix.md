# 7. Appendix

## Common CRC Algorithms

| Algorithm | Poly | Init | RefIn | RefOut | XorOut | Used By |
|-----------|------|------|-------|--------|--------|---------|
| CRC-16/CCITT-FALSE | 0x1021 | 0xFFFF | false | false | 0x0000 | Teltonika (old), Navigil |
| CRC-16/IBM (ARC) | 0xA001 | 0xFFFF | true | true | 0x0000 | Teltonika (correct) |
| CRC-16/XMODEM | 0x1021 | 0x0000 | false | false | 0x0000 | Some Chinese |
| CRC-16/MODBUS | 0xA001 | 0xFFFF | true | true | 0x0000 | Modbus devices |
| CRC-16/KERMIT | 0x1021 | 0x0000 | true | true | 0x0000 | Some trackers |
| CRC-32 | 0x04C11DB7 | 0xFFFFFFFF | true | true | 0xFFFFFFFF | Ruptela, some binary |

### XOR Checksum (simple)

```go
func xorChecksum(data []byte) byte {
    var sum byte
    for _, b := range data {
        sum ^= b
    }
    return sum
}
```

### CRC-16/IBM Implementation (Go)

```go
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
```

## NMEA Coordinate Parsing

Banyak protokol (Xexun, Totem, Gl200) menggunakan format NMEA untuk koordinat.

### Format NMEA

```
Latitude:  DDMM.mmmm  (degrees + minutes)
Longitude: DDDMM.mmmm (degrees + minutes)
```

### Parse ke Decimal Degrees

```go
func parseNMEACoordinate(value string, direction string) float64 {
    // value: "4807.038" means 48 degrees, 7.038 minutes
    // direction: "N", "S", "E", "W"
    
    dotIdx := strings.Index(value, ".")
    if dotIdx < 0 {
        return 0
    }
    
    var degrees float64
    var minutes float64
    
    if dotIdx == 4 {  // DDMM.mmmm
        degrees, _ = strconv.ParseFloat(value[:2], 64)
        minutes, _ = strconv.ParseFloat(value[2:], 64)
    } else if dotIdx == 5 {  // DDDMM.mmmm
        degrees, _ = strconv.ParseFloat(value[:3], 64)
        minutes, _ = strconv.ParseFloat(value[3:], 64)
    }
    
    result := degrees + minutes/60.0
    
    if direction == "S" || direction == "W" {
        result = -result
    }
    
    return result
}
```

### Parse NMEA Timestamp

```go
func parseNMEATime(timeStr string, dateStr string) time.Time {
    // timeStr: "123519.000" -> 12:35:19 UTC
    // dateStr: "030926" -> 2026-09-03
    
    h, _ := strconv.Atoi(timeStr[0:2])
    m, _ := strconv.Atoi(timeStr[2:4])
    s, _ := strconv.Atoi(timeStr[4:6])
    
    d, _ := strconv.Atoi(dateStr[0:2])
    mo, _ := strconv.Atoi(dateStr[2:4])
    y, _ := strconv.Atoi(dateStr[4:6])
    
    return time.Date(2000+y, time.Month(mo), d, h, m, s, 0, time.UTC)
}
```

## BCD Encoding/Decoding

Beberapa protokol (GT06, H02, Meiligao) menggunakan BCD (Binary-Coded Decimal).

### BCD Decode

```go
func bcdToInt(b byte) int {
    return int(b>>4)*10 + int(b&0x0f)
}

func bcdBytesToInt(data []byte) int {
    result := 0
    for _, b := range data {
        result = result*100 + bcdToInt(b)
    }
    return result
}
```

### BCD Encode

```go
func intToBcd(v int) byte {
    h := v / 10
    l := v % 10
    return byte(h<<4 | l)
}
```

## Port Configuration

### Default Ports (Traccar convention)

Saat mengimplementasikan protokol baru, gunakan port convention Traccar sebagai default, tapi selalu overridable via env:

| Protocol | Default Port | Env Var |
|----------|-------------|---------|
| GT06 | 5001 | TCP_PORT (9000) |
| Teltonika | 5027 | TELTONIKA_TCP_PORT (9011) |
| TK103 | 5013 | TK103_TCP_PORT (9002) |
| Meiligao | 5002 | MEILIGAO_TCP_PORT |
| Xexun | 5003 | XEXUN_TCP_PORT |
| Suntech | 5017 | SUNTECH_TCP_PORT |
| H02 | 5010 | H02_TCP_PORT |
| Totem | 5005 | TOTEM_TCP_PORT |
| GT02 | 5006 | GT02_TCP_PORT |
| Navigil | 5012 | NAVIGIL_TCP_PORT |
| Castel | 5019 | CASTEL_TCP_PORT |

### Port Allocation Strategy

```
5000-5099: Protocol TCP listeners (one per protocol)
5100-5199: UDP listeners (if needed)
5200-5299: HTTP-based protocols (if any)
```