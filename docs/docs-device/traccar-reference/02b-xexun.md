# 2b. Xexun (GPS103 / GPS303 / GPS305)

> **Referensi Traccar:** `XexunProtocolDecoder.java` (137 lines, 4.87 KB)

## Frame Structure (Text-based NMEA)

Protokol Xexun menggunakan **NMEA 0183 sentences** yang dimodifikasi.

### Basic format
```
GPRMC,hhmmss.sss,A,ddmm.mmmm,N,dddmm.mmmm,E,ss.ss,ccc.cc,ddmmyy,,,A*hh
```

### Extended format (Xexun "full")
```
serial_number,phone_number,GPRMC,hhmmss.sss,A,ddmm.mmmm,N,dddmm.mmmm,E,ss.ss,ccc.cc,ddmmyy,,,A*hh,satellites,altitude,M:F:dd.ddV
```

## Field Breakdown

| Field | Contoh | Deskripsi |
|-------|--------|-----------|
| serial | 12345 | Serial number (full mode only) |
| phone | 62812345678 | Phone number (full mode only) |
| time | 123519.000 | HHMMSS.sss UTC |
| validity | A | A=Valid, V=Invalid |
| latitude | 4807.038 | DDMM.mmmm format |
| ns | N | N=North, S=South |
| longitude | 01131.000 | DDDMM.mmmm format |
| ew | E | E=East, W=West |
| speed | 022.4 | Knots |
| course | 084.4 | Degrees |
| date | 030926 | DDMMYY |
| signal | F | F=Fixed, L=Lost |
| alarm | help me! | Alarm string |
| imei | imei:123456789012345 | IMEI identifier |
| satellites | 09 | Number of satellites (full) |
| altitude | 499.1 | Meters (full) |
| power | M:F:12.34V | Battery voltage (full) |

## Status Strings (alarm field)

| String | Meaning |
|--------|---------|
| acc on / accstart | Ignition ON |
| acc off / accstop | Ignition OFF |
| help me! / help me | SOS alarm |
| low battery | Low battery alarm |
| move! / moved! | Movement alarm |

## Key Implementation Notes

1. FrameDecoder: cari GPRMC atau GNRMC di stream, lalu cari imei: sebagai delimiter
2. Tidak ada ACK - fire-and-forget (beberapa varian ada +ACK:GPRMC)
3. IMEI selalu ada di akhir sentence: imei:123456789012345
4. Checksum NMEA standard: XOR antara $ dan *
5. Dua mode: Basic (hanya GPRMC) dan Full (dengan serial, phone, sats, altitude, power)