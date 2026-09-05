# 1. Overview

## Traccar sebagai Referensi

Traccar adalah platform GPS tracking open-source berbasis Java yang mendukung **200+ protokol** device GPS dan **2000+ model** tracker. Arsitektur decoder-nya menggunakan pattern **FrameDecoder + ProtocolDecoder + ProtocolEncoder**.

### Kategori Protokol Traccar

| Kategori | Jumlah | Contoh |
|----------|--------|--------|
| Text/NMEA-based | ~40 | Xexun, Totem, Gl200, Navstar |
| Binary (fixed header) | ~80 | GT06, Teltonika, Meiligao, H02 |
| Binary (proprietary) | ~60 | Castel, Suntech, Cellocator, CalAmp |
| OBD/CAN-based | ~20 | Castel OBD, AutoFon OBD, Meiligao OBD |
| IoT/Satellite | ~10 | Iridium, Globalstar, LoRa |

## Gap Analysis

### Sudah Diimplementasikan

| Protokol | Status | File | Catatan |
|----------|--------|------|---------|
| GT06 (Concox) | Lengkap | parser.go, server.go | Login, Position, Alarm, Heartbeat, Fuel Sensor (0x0D), LBS |
| Teltonika (Codec 8/8E/7/6) | Lengkap | teltonika.go, codec8e.go | AVL data, IO elements, dual-write fuel |
| TK103 (GT-clone) | Provisional | tk103.go | Login/heartbeat/position dasar |

### Gap Perlu Ditutup

| Prioritas | Protokol | Alasan |
|-----------|----------|--------|
| Tinggi | Meiligao (GT30i/GT60/VT300) | Sangat populer di Asia |
| Tinggi | Xexun (GPS103/GPS303) | NMEA-based, banyak di Indonesia |
| Tinggi | Suntech (ST215/ST240/ST340) | Premium segment, logistik |
| Tinggi | H02 / H08 | Text protocol populer |
| Sedang | Totem | Text + binary hybrid |
| Sedang | GT02 / GT02A | Concox family binary |
| Sedang | Navigil | Binary dengan ACK |
| Sedang | Castel (SC/CC/MPIP) | OBD + GPS komersial |
| Rendah | CalAmp | Enterprise fleet |
| Rendah | Cellocator | Industrial/enterprise |
| Rendah | Ruptela | European market |

## Arsitektur Decoder Pattern

```
TCP Stream
    |
    v
[FrameDecoder]  <-- Delimit raw bytes into frames
    |
    v
[ProtocolDecoder]  <-- Parse frames into Position objects
    |
    v
[ProtocolEncoder]  <-- Encode responses/commands back to device
    |
    v
TCP Stream (outbound)
```

### Layer Responsibilities

1. **FrameDecoder** → Mendelimitasi frame mentah dari stream TCP
   - Cari start marker
   - Tentukan panjang frame
   - Validasi checksum
   - Return frame utuh

2. **ProtocolDecoder** → Parse frame menjadi TelemetryMessage
   - Identifikasi device (IMEI)
   - Identifikasi command type
   - Parse payload fields
   - Return structured data

3. **ProtocolEncoder** → Encode response ke device
   - Build ACK packet
   - Build command packet
   - Calculate checksum
   - Frame wrapping

## Mapping ke Implementasi Go

| Komponen Traccar | Implementasi Go | Lokasi |
|------------------|-----------------|--------|
| BaseFrameDecoder | readFrame(r *bufio.Reader) | controllers/<protocol>.go |
| BaseProtocolDecoder | handleX(c net.Conn) | controllers/<protocol>.go |
| BaseProtocolEncoder | writeAck(c net.Conn, ...) | controllers/<protocol>.go |
| ByteBuf | []byte + bytes.Reader | stdlib |
| Channel | net.Conn | stdlib |
| Pattern (regex) | regexp.MustCompile | stdlib |