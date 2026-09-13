# 2c. Suntech (ST215 / ST240 / ST340 / ST440)

> **Referensi Traccar:** `SuntechProtocolDecoder.java` (800+ lines)

## Frame Structure

Suntech mendukung dua mode: text (prefix-based) dan binary.

### Text mode
```
<payload_length>;<IMEI>;<Command>;<Data...>#<checksum>
```

### Binary mode (ST340/ST440)
```
0x02 | Length(2B) | Command(1B) | Payload(N) | CRC(2B) | 0x03
```

## Text Mode Commands (prefix)

| Prefix | Command | Deskripsi |
|--------|---------|-----------|
| ST215 | Basic tracking | Position, alarm, heartbeat |
| ST235 | Extended tracking | + driver behavior |
| ST240 | Full telemetry | + OBD, temperature |
| ST340 | Binary protocol | Compressed data |
| ST490 | Advanced | + CAN bus |
| CRR | Crash report | Crash data |
| ST9HTE | Travel report | Trip summary |

## Binary Mode Commands

| Command | Code | Deskripsi |
|---------|------|-----------|
| Position report | 0x30 | GPS position |
| Alarm report | 0x31 | Alarm event |
| Driver behavior | 0x33 | Harsh braking/accel |
| Temperature | 0x36 | Sensor temperature |
| OBD data | 0x37 | OBD II data |

## Key Implementation Notes

1. Text mode: delimiter ; (semicolon), checksum di akhir sebelum #
2. Binary mode: header 0x02, footer 0x03, CRC di akhir
3. Support crash report (CRR) dengan data G-sensor
4. ST340/440 gunakan binary compressed format
5. Support protocol type per-device (bisa dikonfigurasi via attribute)
6. Ada mode HBM (Heart Beat Mode) untuk heartbeat interval