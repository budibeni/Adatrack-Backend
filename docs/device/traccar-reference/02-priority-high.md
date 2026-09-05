# 2. Prioritas Tinggi

## 2.1 Meiligao (GT30i / GT60 / VT300)

> **Referensi Traccar:** `MeiligaoProtocolDecoder.java` (498 lines, 19.5 KB)

### Frame Structure

```
Start(2B) | Length(2B) | Command(2B) | Payload(N) | Serial(2B) | Checksum(2B) | Stop(2B)
0x24 0x24  | Big-Endian | Big-Endian  | Variable   | Big-Endian | XOR          | 0x0D 0x0A
```

### Command Types

| Command | Code | Direction | Deskripsi |
|---------|------|-----------|-----------|
| MSG_LOGIN | 0x5001 | Device->Server | Login/IMEI registration |
| MSG_LOGIN_RESPONSE | 0x9999 | Server->Device | Login ACK |
| MSG_HEARTBEAT | 0x5003 | Device->Server | Heartbeat |
| MSG_POSITION | 0x5002 | Device->Server | GPS Position report |
| MSG_ALARM | 0x5004 | Device->Server | Alarm report |
| MSG_SERVER | 0x9998 | Server->Device | Server command |

### Position Payload

```
Timestamp(7B BCD) | Lat(4B) | Lon(4B) | Speed(1B) | Course(2B) | Status(1B)
```

### Login Payload

```
IMEI(7 byte ASCII)
```

### ACK Response

```
0x24 0x24 | Length(2B) | 0x9999 | IMEI(7B) | Serial(2B) | Checksum(2B) | 0x0D 0x0A
```

### Key Implementation Notes

1. FrameDecoder: strip byte non-0x24, baca length di offset+2
2. IMEI identification: dari login packet
3. Multi-command: satu connection bisa carry multiple command types
4. Support OBD data (MSG_OBD_RT), DTC, dan RFID