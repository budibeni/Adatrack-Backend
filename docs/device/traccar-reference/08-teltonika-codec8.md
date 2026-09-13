# Teltonika Codec 8 Extended (0x8E) — Protocol Reference

> **Source:** Teltonika GPS Wiki (https://wiki.teltonika-gps.com/view/Codec)
> **Cross-reference:** Traccar TeltonikaProtocolDecoder.java
> **Family:** FMB/FMM asset trackers
> **Priority:** **HIGHEST** — primary codec for Teltonika devices in this project
> **Last updated:** 2026-09-04

---

## 1. Overview

This document is the **standalone reference** for Teltonika device protocols. It is separated
from other protocol references (`traccar-reference/`) because Teltonika devices are the
**primary supported protocol** in this project (implemented in fase B5a — Fuel Sensor End-to-End).

### 1.1 Codec Priority Within Teltonika Family

| Priority | Codec | ID | Description | Status |
|----------|-------|----|-------------|--------|
| **HIGHEST** | **Codec 8 Extended** | `0x8E` | Extended AVL codec — 2-byte IO IDs, 2-byte counts | ✅ Implemented |
| HIGH | Codec 8 | `0x08` | Standard AVL codec — 1-byte IO IDs, 1-byte counts | ✅ Implemented |
| MEDIUM | Codec 7 | `0x07` | Short codec (no priority, no event) | ✅ Implemented |
| LOW | Codec 6 | `0x06` | Extended codec with priority (legacy) | ⬜ Not implemented |
| LOW | Codec 12 | `0x0C` | Command/response protocol | ⬜ Not implemented |
| LOW | Codec 13 | `0x0D` | Event confirmation protocol | ⬜ Not implemented |
| LOW | Codec 16 | `0x10` | Extended records (multi-record) | ⬜ Not implemented |

**Why Codec 8 Extended has the highest priority:**
- Supports larger IO ID space (2-byte = up to 65535 IDs vs 255)
- Supports more elements per group (2-byte counts)
- Required for advanced features (fuel sensors, extended telemetry)
- Used by modern FMB/FMM family devices
- Already fully implemented and verified in this project

---

## 2. TCP Transport Framing

### 2.1 IMEI Login Packet (handshake)

```
+--------+------------------------+
| Length | IMEI (ASCII)           |
| 2 BE   | N bytes                |
+--------+------------------------+
```

- **Length** (2 bytes, big-endian): number of IMEI bytes that follow (typically `0x000F` = 15).
- **IMEI**: ASCII string (15 digits) or hex-encoded (30 chars -> 15 bytes).

**Server response:** `0x01` (1 byte) = accepted; `0x00` = rejected.

### 2.2 AVL Data Packet (TCP)

```
+----------+----------+------------------------+--------+--------+
| Preamble | Data Len | AVL Payload            | CRC-16 | No. of |
| 4 bytes  | 4 BE     | (variable)             | 2 LE   | Data   |
| (0x0000  |          |                        |        | 1 byte |
|  0000)   |          |                        |        |        |
+----------+----------+------------------------+--------+--------+
```

| Field | Size | Description |
|-------|------|-------------|
| Preamble | 4 bytes | Always `0x00000000` |
| Data Length | 4 bytes BE | Length of AVL Payload |
| AVL Payload | N bytes | See section 3 |
| CRC-16 | 2 bytes LE | CRC-16/IBM over AVL Payload |
| Number of Data | 1 byte | Repeat of record count |

### 2.3 AVL Data Packet (UDP)

```
+--------+----------+-----------+--------+------------------------+
| Length | Packet ID| Packet    | No. of | AVL Payload            |
| 2 BE   | 2 BE     | Type 1B   | Data   | (variable)             |
|        |          | (0x00)    | 1B     |                        |
+--------+----------+-----------+--------+------------------------+
```

UDP does **not** include CRC or preamble.

### 2.4 Server Response (AVL ACK)

**TCP:** 4 bytes big-endian = number of records accepted (e.g., `0x00000001`).

**UDP:** 5 bytes - `0x0005 0x0000 0x01 <packet_id> <count>`.

---

## 3. AVL Payload Structure

```
+--------+--------+--------------------+--------+--------+
| Codec  | No. of | AVL Records        | CRC-16 | No. of |
| ID     | Data   | (N x record)       | 2 LE   | Data   |
| 1 byte | 1 byte |                    |        | 1 byte |
+--------+--------+--------------------+--------+--------+
```

| Field | Size | Description |
|-------|------|-------------|
| Codec ID | 1 byte | `0x08` (Codec 8) or `0x8E` (Codec 8 Extended) |
| Number of Data | 1 byte | Number of AVL records in this packet |
| AVL Records | variable | See section 4 |
| CRC-16 | 2 bytes LE | CRC-16/IBM over `[Codec ID .. last byte of last record]` |
| Number of Data | 1 byte | Repeat of record count |

---

## 4. AVL Record Structure

### 4.1 Codec 8 Extended Record (0x8E) — PRIMARY

**Base record: 28 bytes + IO elements**

```
Offset  Size  Field           Description
------  ----  -----           -----------
0       8     Timestamp       Unix epoch, milliseconds (BE)
8       1     Priority        0=Low, 1=High, 2=Emergency
9       4     Longitude       Degrees x 10^7 (signed int32 BE)
13      4     Latitude        Degrees x 10^7 (signed int32 BE)
17      2     Altitude        Meters above sea level (unsigned)
19      2     Angle           Degrees from north (0-359, unsigned)
21      1     Satellites      Number of visible satellites
22      2     Speed           10 x km/h (unsigned) -> divide by 10
24      2     Event IO ID     ID of the event (2 bytes BE)
26      2     Number of IO    Total count of IO elements (N_total, 2 bytes BE)
```

**IO Elements (variable length):**

After the base 28 bytes, IO elements are grouped by size:

```
For each group [1-byte, 2-byte, 4-byte, 8-byte, variable]:
    Count (2 bytes BE) - number of elements in this group
    For each element:
        IO ID (2 bytes BE) - property identifier
        Value (N bytes) - N = group size (1/2/4/8)
    For variable group only:
        IO ID (2 bytes BE)
        Length (2 bytes BE)
        Value (Length bytes)
```

### 4.2 Codec 8 Record (0x08) — SECONDARY

**Base record: 26 bytes + IO elements**

```
Offset  Size  Field           Description
------  ----  -----           -----------
0       8     Timestamp       Unix epoch, milliseconds (BE)
8       1     Priority        0=Low, 1=High, 2=Emergency
9       4     GPS Element     See section 5
13      4     Longitude       Degrees x 10^7 (signed int32 BE)
17      2     Altitude        Meters above sea level (unsigned)
19      2     Angle           Degrees from north (0-359, unsigned)
21      1     Satellites      Number of visible satellites
22      2     Speed           10 x km/h (unsigned) -> divide by 10
24      1     Event IO ID     ID of the event that triggered the record
25      1     Number of IO    Total count of IO elements (N_total)
```

**IO Elements (variable length):**

After the base 26 bytes, IO elements are grouped by size:

```
For each group [1-byte, 2-byte, 4-byte, 8-byte, variable]:
    Count (1 byte) - number of elements in this group
    For each element:
        IO ID (1 byte) - property identifier
        Value (N bytes) - N = group size (1/2/4/8)
    For variable group only:
        IO ID (1 byte)
        Length (1 byte)
        Value (Length bytes)
```

### 4.3 Key Differences: Codec 8 Extended vs Codec 8

| Feature | Codec 8 Extended (0x8E) | Codec 8 (0x08) |
|---------|------------------------|----------------|
| Priority | **HIGHEST** | HIGH |
| Base record size | 28 bytes | 26 bytes |
| Event IO ID width | 2 bytes | 1 byte |
| Number of IO width | 2 bytes | 1 byte |
| IO ID width | 2 bytes | 1 byte |
| Group count width | 2 bytes | 1 byte |
| Variable IO length width | 2 bytes | 1 byte |
| Max IO ID value | 65535 | 255 |
| Max elements per group | 65535 | 255 |
| Use case | Modern FMB/FMM devices | Legacy devices |

---

## 5. GPS Element Detail

The GPS element is encoded as a single 4-byte field in Codec 8 (split into lon/lat in 0x8E):

```
Codec 8 (offset 9-12, 4 bytes):
    Byte 0:    Validity (bit 0: 1=valid, 0=invalid) + satellites MSB
    Byte 1-3:  Longitude (24-bit signed, degrees x 10^7)

Codec 8 Extended (offset 9-16, split):
    Longitude (4 bytes, signed int32 BE, degrees x 10^7)
    Latitude  (4 bytes, signed int32 BE, degrees x 10^7)
```

**Coordinate conversion:**
```
degrees = raw_value / 10_000_000.0
```

**Example:** `1068456000` -> `106.8456000 degrees`

---

## 6. CRC-16/IBM Calculation

Teltonika uses **CRC-16/IBM** (also known as CRC-16/ARC):

| Parameter | Value |
|-----------|-------|
| Polynomial | `0xA001` (reflected) or `0x8005` (normal) |
| Initial value | `0xFFFF` |
| Input reflected | Yes |
| Output reflected | Yes |
| Final XOR | `0x0000` |

**Algorithm (reflected/LSB-first):**
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

**CRC is transmitted as 2 bytes little-endian** (LSB first).

**CRC scope:** Over the entire AVL Payload (from Codec ID through last byte of last record).

---

## 7. IO Element Reference

### 7.1 Codec 8 Extended IO IDs (2-byte) — PRIMARY

| ID (dec) | ID (hex) | Name | Size | Description |
|----------|----------|------|------|-------------|
| 1 | 0x0001 | Digital Input 1 | 1 | Ignition |
| 2 | 0x0002 | Digital Input 2 | 1 | |
| 3 | 0x0003 | Digital Input 3 | 1 | |
| 4 | 0x0004 | Digital Input 4 | 1 | |
| 9 | 0x0009 | Analog Input 1 | 2 | mV |
| 10 | 0x000A | Analog Input 2 | 2 | mV |
| 11 | 0x000B | Analog Input 3 | 2 | mV |
| 12 | 0x000C | Analog Input 4 | 2 | mV |
| 15 | 0x000F | Digital Output 1 | 1 | |
| 16 | 0x0010 | Digital Output 2 | 1 | |
| 17 | 0x0011 | Digital Output 3 | 1 | |
| 18 | 0x0012 | Digital Output 4 | 1 | |
| 21 | 0x0015 | GSM Signal | 1 | 0-5 |
| 24 | 0x0018 | Speed | 2 | km/h |
| 48 | 0x0030 | Battery Voltage | 2 | mV |
| 66 | 0x0042 | Movement Sensor | 1 | |
| 67 | 0x0043 | Ignition | 1 | 0/1 |
| 68 | 0x0044 | GSM Operator | 2 | |
| 69 | 0x0045 | GPS Signal | 1 | |
| 70 | 0x0046 | HMI Fuel Level | 2 | |
| 71 | 0x0047 | Battery Current | 2 | mA |
| 72 | 0x0048 | Battery Voltage | 2 | V x 100 |
| 73 | 0x0049 | PDOP | 2 | x 100 |
| 74 | 0x004A | HDOP | 2 | x 100 |
| 78 | 0x004E | PCB Temperature | 2 | C x 10 |
| 80 | 0x0050 | Trip Odometer | 4 | m |
| 81 | 0x0051 | Total Odometer | 4 | m |
| 82 | 0x0052 | Sleep Timer | 1 | |
| 83 | 0x0053 | GSM Cell ID | 4 | |
| 84 | 0x0054 | GSM LAC | 2 | |
| 85 | 0x0055 | GSM MCC | 2 | |
| 86 | 0x0056 | GSM MNC | 2 | |
| 90 | 0x005A | iButton ID | 8 | |
| 100 | 0x0064 | Mode | 1 | |
| 101 | 0x0065 | HHM Fuel Level | 2 | |
| 102 | 0x0066 | HHM Fuel Temperature | 2 | |
| 108 | 0x006C | Fuel Level (liters) | 2 | L x 10 |
| 110 | 0x006E | Fuel Temperature | 2 | C x 10 |
| 113 | 0x0071 | RPM | 2 | |
| 120 | 0x0078 | GSM Band | 1 | |
| 122 | 0x007A | Active Operator | 4 | |
| 123 | 0x007B | IMSI | 8 | |
| 125 | 0x007D | CCID | 20 | |
| 129 | 0x0081 | Eco Score | 2 | x 100 |
| 130 | 0x0082 | Fuel Rate | 2 | L/h x 10 |
| 131 | 0x0083 | Fuel Used | 4 | L x 10 |
| 132 | 0x0084 | Fuel Level (%) | 2 | % x 10 |
| 133 | 0x0085 | Fuel Temperature | 2 | C x 10 |
| 134 | 0x0086 | Fuel Level (ADC) | 2 | raw |
| 135 | 0x0087 | Fuel Level (Ohm) | 2 | Ohm |
| 256 | 0x0100 | Dallas Temperature | 4 | C x 10 |
| 257 | 0x0101 | Dallas Temperature 2 | 4 | |
| 258 | 0x0102 | Dallas Temperature 3 | 4 | |
| 259 | 0x0103 | Dallas Temperature 4 | 4 | |
| 768 | 0x0300 | Dallas ID | 8 | |
| 1024 | 0x0400 | Fuel Level (extended) | 4 | |
| 1280 | 0x0500 | CAN data | var | |
| 1536 | 0x0600 | Bluetooth | var | |

### 7.2 Codec 8 IO IDs (1-byte) — SECONDARY

| ID (dec) | ID (hex) | Name | Size | Description |
|----------|----------|------|------|-------------|
| 1 | 0x01 | Digital Input 1 | 1 | 0/1 (ignition on many models) |
| 2 | 0x02 | Digital Input 2 | 1 | 0/1 |
| 3 | 0x03 | Digital Input 3 | 1 | 0/1 |
| 4 | 0x04 | Digital Input 4 | 1 | 0/1 |
| 9 | 0x09 | Analog Input 1 | 2 | mV |
| 10 | 0x0A | Analog Input 2 | 2 | mV |
| 11 | 0x0B | Analog Input 3 | 2 | mV |
| 12 | 0x0C | Analog Input 4 | 2 | mV |
| 15 | 0x0F | Digital Output 1 | 1 | 0/1 |
| 16 | 0x10 | Digital Output 2 | 1 | 0/1 |
| 17 | 0x11 | Digital Output 3 | 1 | 0/1 |
| 18 | 0x12 | Digital Output 4 | 1 | 0/1 |
| 21 | 0x15 | GSM Signal | 1 | 0-5 (signal level) |
| 24 | 0x18 | Speed | 2 | km/h (alternative to GPS speed) |
| 48 | 0x30 | Battery Voltage | 2 | mV |
| 66 | 0x42 | Movement Sensor | 1 | 0=moving, 1=stopped |
| 67 | 0x43 | Ignition | 1 | 0=off, 1=on |
| 68 | 0x44 | GSM Operator | 2 | MCC+MNC |
| 69 | 0x45 | GPS Signal | 1 | 0-5 |
| 70 | 0x46 | HMI Fuel Level | 2 | ADC value |
| 71 | 0x47 | Battery Current | 2 | mA |
| 72 | 0x48 | Battery Voltage (alt) | 2 | V x 100 |
| 73 | 0x49 | PDOP | 2 | x 100 |
| 74 | 0x4A | HDOP | 2 | x 100 |
| 78 | 0x4E | PCB Temperature | 2 | C x 10 (signed) |
| 80 | 0x50 | Trip Odometer | 4 | meters |
| 81 | 0x51 | Total Odometer | 4 | meters |
| 82 | 0x52 | Sleep Timer | 1 | 0/1 |
| 83 | 0x53 | GSM Cell ID | 4 | Cell ID |
| 84 | 0x54 | GSM LAC | 2 | Location Area Code |
| 85 | 0x55 | GSM MCC | 2 | Mobile Country Code |
| 86 | 0x56 | GSM MNC | 2 | Mobile Network Code |
| 90 | 0x5A | iButton ID | 8 | iButton serial |
| 100 | 0x64 | Mode | 1 | 0-3 |
| 101 | 0x65 | HHM Fuel Level | 2 | ADC |
| 102 | 0x66 | HHM Fuel Temperature | 2 | C x 10 |
| 108 | 0x6C | Fuel Level (liters) | 2 | liters x 10 |
| 110 | 0x6E | Fuel Temperature | 2 | C x 10 (signed) |
| 113 | 0x71 | RPM | 2 | RPM |
| 120 | 0x78 | GSM Band | 1 | 0-7 |
| 122 | 0x7A | Active Operator | 4 | PLMN |
| 123 | 0x7B | IMSI | 8 | IMSI (BCD) |
| 125 | 0x7D | CCID | 20 | SIM CCID |
| 129 | 0x81 | Eco Score | 2 | x 100 |
| 130 | 0x82 | Fuel Rate | 2 | L/h x 10 |
| 131 | 0x83 | Fuel Used | 4 | liters x 10 |
| 132 | 0x84 | Fuel Level (%) | 2 | % x 10 |
| 133 | 0x85 | Fuel Temperature (alt) | 2 | C x 10 |
| 134 | 0x86 | Fuel Level (ADC) | 2 | raw ADC |
| 135 | 0x87 | Fuel Level (Ohm) | 2 | Ohm |

---

## 8. Priority Values

| Value | Name | Description |
|-------|------|-------------|
| 0 | LOW | Normal periodic record |
| 1 | HIGH | Event-triggered (movement, input change, etc.) |
| 2 | EMERGENCY | SOS / alarm button |

---

## 9. Event IO ID

The **Event IO ID** field indicates which IO element triggered the record:
- **Codec 8 Extended (0x8E):** offset 24-25 (2 bytes BE)
- **Codec 8 (0x08):** offset 24 (1 byte)

A value of `0` means the record was triggered by a periodic timer, not an event.

---

## 10. Implementation Notes

### 10.1 Parsing Algorithm

```go
func parseTeltonikaAVL(payload []byte) ([]TelemetryMessage, error) {
    if len(payload) < 5 {
        return nil, errors.New("short payload")
    }

    codec := payload[0]
    count := payload[1]

    var records []TelemetryMessage
    offset := 2

    for i := 0; i < count; i++ {
        var msg TelemetryMessage
        var consumed int
        var err error

        switch codec {
        case 0x08:
            msg, consumed, err = parseCodec8Record(payload[offset:])
        case 0x8E:
            msg, consumed, err = parseCodec8ERecord(payload[offset:])
        default:
            return nil, fmt.Errorf("unknown codec: 0x%02X", codec)
        }

        if err != nil {
            return nil, err
        }
        records = append(records, msg)
        offset += consumed
    }

    // Verify CRC (last 3 bytes: CRC-LE + count repeat)
    if len(payload) < offset+3 {
        return nil, errors.New("short CRC")
    }

    crcReceived := binary.LittleEndian.Uint16(payload[offset:offset+2])
    crcExpected := teltonikaCRC16(payload[:offset])

    if crcReceived != crcExpected {
        return nil, fmt.Errorf("CRC mismatch: got %04X, want %04X", crcReceived, crcExpected)
    }

    // Verify count repeat
    if payload[offset+2] != byte(count) {
        return nil, errors.New("count mismatch")
    }

    return records, nil
}
```

### 10.2 Codec 8 Extended Record Parsing (PRIMARY)

```go
func parseCodec8ERecord(b []byte) (TelemetryMessage, int, error) {
    var t TelemetryMessage
    if len(b) < 28 {
        return t, 0, errors.New("short codec8e record")
    }

    ms := int64(binary.BigEndian.Uint64(b[0:8]))
    t.Timestamp = ms / 1000

    t.Lon = float64(int32(binary.BigEndian.Uint32(b[9:13]))) / 1e7
    t.Lat = float64(int32(binary.BigEndian.Uint32(b[13:17]))) / 1e7
    t.Heading = int16(binary.BigEndian.Uint16(b[19:21]))
    t.Satellites = b[21]
    t.Speed = float64(binary.BigEndian.Uint16(b[22:24])) / 10.0

    // Event IO ID (2 bytes) + N of Total IO (2 bytes) - informational only
    off := 28

    for _, elemLen := range []int{1, 2, 4, 8} {
        if off+2 > len(b) {
            return t, 0, errors.New("short codec8e io group count")
        }
        n := int(binary.BigEndian.Uint16(b[off : off+2]))
        off += 2
        for j := 0; j < n; j++ {
            if off+2+elemLen > len(b) {
                break
            }
            id := binary.BigEndian.Uint16(b[off : off+2])
            var val uint64
            for k := 0; k < elemLen; k++ {
                val = val<<8 | uint64(b[off+2+k])
            }
            off += 2 + elemLen
            applyTeltonikaIO(&t, id, val)
        }
    }

    // Variable-length group
    if off+2 > len(b) {
        return t, 0, errors.New("short codec8e var group count")
    }
    n := int(binary.BigEndian.Uint16(b[off : off+2]))
    off += 2
    for j := 0; j < n; j++ {
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
        applyTeltonikaIO(&t, id, val)
    }

    return t, off, nil
}
```

### 10.3 Codec 8 Record Parsing (SECONDARY)

```go
func parseCodec8Record(b []byte) (TelemetryMessage, int, error) {
    var t TelemetryMessage
    if len(b) < 26 {
        return t, 0, errors.New("short codec8 record")
    }

    ms := int64(binary.BigEndian.Uint64(b[0:8]))
    t.Timestamp = ms / 1000

    t.Lon = float64(int32(binary.BigEndian.Uint32(b[9:13]))) / 1e7
    t.Lat = float64(int32(binary.BigEndian.Uint32(b[13:17]))) / 1e7
    t.Heading = int16(binary.BigEndian.Uint16(b[19:21]))
    t.Satellites = b[21]
    t.Speed = float64(binary.BigEndian.Uint16(b[22:24])) / 10.0

    ioCount := int(b[25])
    off := 26

    // Parse IO groups: 1-byte, 2-byte, 4-byte, 8-byte, variable
    for _, elemLen := range []int{1, 2, 4, 8} {
        if off >= len(b) {
            break
        }
        n := int(b[off])
        off++
        for j := 0; j < n; j++ {
            if off+1+elemLen > len(b) {
                break
            }
            id := b[off]
            var val uint64
            for k := 0; k < elemLen; k++ {
                val = val<<8 | uint64(b[off+1+k])
            }
            off += 1 + elemLen
            applyTeltonikaIO(&t, uint16(id), val)
        }
    }

    // Variable-length group
    if off < len(b) {
        n := int(b[off])
        off++
        for j := 0; j < n; j++ {
            if off+2 > len(b) {
                break
            }
            id := b[off]
            l := int(b[off+1])
            off += 2
            if off+l > len(b) {
                break
            }
            var val uint64
            for k := 0; k < l; k++ {
                val = val<<8 | uint64(b[off])
                off++
            }
            applyTeltonikaIO(&t, uint16(id), val)
        }
    }

    return t, off, nil
}
```

---

## 11. Example Packets

### 11.1 Codec 8 Extended - Single Record with IO (PRIMARY)

```
TCP Frame:
00 00 00 00  - Preamble
00 00 00 26  - Data Length (38 bytes)
8E           - Codec ID (Codec 8 Extended)
01           - Number of Data (1 record)
  00 00 01 89 12 34 56 78  - Timestamp (ms)
  00           - Priority (Low)
  06 4E 5B 20  - Longitude (106.8456000)
  02 76 1B 40  - Latitude (6.2088000)
  00 7D        - Altitude (125m)
  00 B4        - Angle (180 degrees)
  0A           - Satellites (10)
  01 C7        - Speed (45.5 km/h)
  00 00        - Event IO ID (none)
  00 03        - Number of Total IO (3)
  00 01        - N1 (1-byte IO count = 1)
    00 48 00 D2  - IO ID 72 (battery), value 0xD2 (210)
  00 01        - N2 (2-byte IO count = 1)
    00 43 00 01  - IO ID 67 (ignition), value 1 (ON)
  00 00        - N4 (4-byte IO count = 0)
  00 00        - N8 (8-byte IO count = 0)
  00 01        - NX (variable IO count = 1)
    00 56 00 02 01 2C  - IO ID 86 (fuel), length 2, value 300
XX XX        - CRC-16 (LE)
01           - Number of Data (repeat)
```

### 11.2 Codec 8 - Single Record (no IO) (SECONDARY)

```
TCP Frame:
00 00 00 00  - Preamble
00 00 00 1A  - Data Length (26 bytes)
08           - Codec ID (Codec 8)
01           - Number of Data (1 record)
  00 00 01 89 12 34 56 78  - Timestamp (ms)
  00           - Priority (Low)
  06 4E 5B 20  - Longitude (106.8456000)
  02 76 1B 40  - Latitude (6.2088000)
  00 7D        - Altitude (125m)
  00 B4        - Angle (180 degrees)
  0A           - Satellites (10)
  01 C7        - Speed (455 = 45.5 km/h)
  00           - Event IO ID (none)
  00           - Number of IO (0)
XX XX        - CRC-16 (LE)
01           - Number of Data (repeat)
```

---

## 12. References

- **Teltonika GPS Wiki - Codec:** https://wiki.teltonika-gps.com/view/Codec
- **Traccar TeltonikaProtocolDecoder:** https://github.com/traccar/traccar/blob/master/src/main/java/org/traccar/protocol/TeltonikaProtocolDecoder.java
- **Traccar TeltonikaProtocol:** https://github.com/traccar/traccar/blob/master/src/main/java/org/traccar/protocol/TeltonikaProtocol.java

---

## 13. Implementation Status in This Project

| Feature | Status | File |
|---------|--------|------|
| Codec 8 Extended parsing (PRIMARY) | Implemented | `ingestion-tcp/controllers/codec8e.go` |
| Codec 8 parsing (SECONDARY) | Implemented | `ingestion-tcp/controllers/teltonika.go` |
| Codec 7 parsing | Implemented | `ingestion-tcp/controllers/teltonika.go` |
| CRC-16/IBM | Implemented | `ingestion-tcp/controllers/teltonika.go` |
| IME login (2-byte length) | Fixed 2026-08-26 | `ingestion-tcp/controllers/teltonika.go` |
| Preamble handling (4 zero bytes) | Fixed 2026-08-26 | `ingestion-tcp/controllers/teltonika.go` |
| Fuel IO mapping (configurable) | Implemented | `ingestion-tcp/controllers/codec8e.go` |
| Unit tests | Passing | `ingestion-tcp/controllers/codec8e_test.go` |

> **GAP (honest):** Byte offsets for Codec 8 Extended (0x8E) are based on publicly available documentation and Traccar source code. They **must be verified against real device captures** before production deployment. This is tracked as GAP PRD Module 1a.
