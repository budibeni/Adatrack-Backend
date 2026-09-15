# 3. Prioritas Sedang

## 3.1 Totem

> **Referensi Traccar:** `TotemProtocolDecoder.java` (600+ lines)

### Frame Structure

Protokol Totem menggunakan dua pattern: text-based ($) dan binary.

**Text mode Pattern 1 (GPRMC):**
```
$$<length>|<IMEI>|<alarm>$GPRMC,<time>,<A/V>,<lat>,<N/S>,<lon>,<E/W>,<speed>,<course>,<date>*<checksum>|<pdop>|<hdop>|<vdop>|<io>|<battery>|<power>|<adc>|<lac>|<cid>|<temp>|<odometer>|<serial><checksum>
```

**Text mode Pattern 2 (binary-like):**
```
$$<length>|<IMEI>|<alarm>|<date>|<time>|<A/V>|<lat>|<lon>|<speed>|<course>|<status>|<adc>|<power>|<temp>|<odometer>|<serial><checksum>
```

### Key Implementation Notes

1. Frame delimiter: $$ di awal
2. Checksum: XOR dari $$
3. Dua mode parsing: GPRMC-based dan pipe-delimited
4. ACK response: ACK OK untuk mode 1, $$0014AA + checksum untuk mode 4
5. Support E2/E5 messages untuk OBD data
6. Field io (input/output status) untuk berbagai sensor

---

## 3.2 GT02 / GT02A

> **Referensi Traccar:** `Gt02ProtocolDecoder.java` (compact, 100 lines)

### Frame Structure

```
Header(2B) | Size(1B) | Power(1B) | GSM(1B) | IMEI(8B) | Index(2B) | Type(1B) | Payload(N) | Checksum(2B) | Stop(2B)
0x68 0x68   | 1 byte   | 1 byte    | 1 byte  | 8 byte   | 2 byte   | 1 byte   | Variable  | XOR          | 0x0D 0x0A
```

### Command Types

| Type | Code | Deskripsi |
|------|------|-----------|
| MSG_DATA | 0x10 | GPS Position data |
| MSG_HEARTBEAT | 0x1A | Heartbeat |
| MSG_RESPONSE | 0x1C | Response to server command |

### Position Payload (MSG_DATA)

```
Date(3B: YY,MM,DD) | Time(3B: HH,MM,SS) | Lat(4B) | Lon(4B) | Speed(1B) | Course(2B) | Reserved(3B) | Flags(4B)
```

- Latitude: unsigned int / (60.0 * 30000.0)
- Longitude: unsigned int / (60.0 * 30000.0)
- Flags: bit 0=valid, bit 1=north, bit 2=east

### Key Implementation Notes

1. Frame delimiter: 0x68 0x68 header, 0x0D 0x0A footer
2. IMEI: 8 byte hex (ambil 15 digit setelah skip first)
3. Heartbeat response: 0x54 0x68 0x1A 0x0D 0x0A
4. Checksum: XOR dari semua byte
5. Sangat compact - mudah diimplementasikan
6. GT02A adalah versi lebih baru dengan tambahan field

---

## 3.3 Navigil

> **Referensi Traccar:** `NavigilProtocolDecoder.java` (300+ lines)

### Frame Structure

```
ProtocolVer(1B) | VersionID(1B) | SeqNum(2B LE) | MsgID(2B LE) | Length(2B LE) | Flags(2B LE) | Checksum(2B LE) | DeviceID(4B LE) | Timestamp(4B LE) | Payload(N)
```

### Message Types

| MsgID | Code | Deskripsi |
|-------|------|-----------|
| MSG_ERROR | 2 | Error report |
| MSG_INDICATION | 4 | Indication |
| MSG_CONN_OPEN | 5 | Connection open |
| MSG_CONN_CLOSE | 6 | Connection close |
| MSG_SYSTEM_REPORT | 7 | System status |
| MSG_UNIT_REPORT | 8 | Unit status report |
| MSG_GEOFENCE_ALARM | 10 | Geofence alarm |
| MSG_INPUT_ALARM | 11 | Input alarm |
| MSG_TG2_REPORT | 12 | TG2 report |
| MSG_POSITION_REPORT | 13 | Position report |
| MSG_POSITION_REPORT_2 | 15 | Position report ext |
| MSG_SNAPSHOT4 | 17 | Snapshot |
| MSG_TRACKING_DATA | 18 | Tracking data |
| MSG_MOTION_ALARM | 19 | Motion alarm |
| MSG_ACKNOWLEDGEMENT | 255 | ACK |

### Key Implementation Notes

1. Header tetap 20 byte (fixed)
2. Semua integer little-endian
3. CRC16 CCITT-FALSE untuk checksum
4. Sequence number untuk ACK
5. Timestamp: Unix epoch + 25 leap seconds delta
6. Support multiple position formats (UnitReport, TG2, PositionReport)
7. Device ID: 4 byte LE (bukan IMEI)

---

## 3.4 Castel (SC/CC/MPIP)

> **Referensi Traccar:** `CastelProtocolDecoder.java` (700+ lines)

### Frame Structure

Tiga versi protocol: SC (Smart Car), CC (Commercial Car), MPIP (Multi-Purpose IP)

**Common header:**
```
Header(2B LE) | Length(2B LE) | Version(1B) | ID(20B ASCII) | Type(2B) | Payload(N)
```

- SC header: 0x4040
- CC header: 0x4040 (same, tapi beda version)
- MPIP: tanpa version byte

### Command Types (SC/CC)

| Type | Code | Deskripsi |
|------|------|-----------|
| MSG_SC_LOGIN | 0x1001 | Login |
| MSG_SC_LOGOUT | 0x1002 | Logout |
| MSG_SC_HEARTBEAT | 0x1003 | Heartbeat |
| MSG_SC_GPS | 0x4001 | GPS Position |
| MSG_SC_PID_DATA | 0x4002 | PID data |
| MSG_SC_G_SENSOR | 0x4003 | G-sensor |
| MSG_SC_OBD_DATA | 0x4005 | OBD data |
| MSG_SC_DTCS | 0x4006/0x400B | DTC codes |
| MSG_SC_ALARM | 0x4007 | Alarm |
| MSG_SC_CELL | 0x4008 | Cell info |
| MSG_SC_GPS_SLEEP | 0x4009 | GPS sleep |
| MSG_SC_FUEL | 0x400E | Fuel data |
| MSG_SC_COMPREHENSIVE | 0x401F | Comprehensive data |

### Key Implementation Notes

1. ID: 20 byte ASCII (padded), bukan IMEI
2. GPS payload: timestamp(6B BCD) + lat(4B) + lon(4B) + speed(1B) + course(2B) + status(4B)
3. OBD data: PID-based dengan length map
4. PID length map: bervariasi 1, 2, atau 4 byte per PID
5. Support DTC (Diagnostic Trouble Codes) read/clear
6. Comprehensive data: GPS + OBD dalam satu message
7. Fuel data: fuel level, fuel consumption