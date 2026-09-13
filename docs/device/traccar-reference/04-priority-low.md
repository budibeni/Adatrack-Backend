# 4. Prioritas Rendah

## 4.1 CalAmp

> **Referensi Traccar:** `CalAmpProtocolDecoder.java`

### Frame Structure

```
Options(2B) | Mobile ID(4B) | Sequence(2B) | Type(1B) | Payload(N) | Checksum(2B)
```

### Key Notes

1. Sangat banyak message type (30+)
2. Mobile ID: 4 byte, bukan IMEI
3. Options header mengikuti setiap message
4. Support mini-messages untuk small data
5. Banyak varian firmware dengan behavior berbeda

---

## 4.2 Cellocator

> **Referensi Traccar:** `CellocatorProtocolDecoder.java` + `CellocatorFrameDecoder.java`

### Frame Structure

```
Start(1B: 0x4C) | Length(1B) | Type(1B) | Payload(N) | CRC(2B)
```

### Key Notes

1. Start byte: 0x4C (L)
2. CRC-16/CCITT untuk integrity check
3. Banyak packet type (20+)
4. Support burst mode (multiple packets)
5. Advanced power management commands

---

## 4.3 Ruptela

> **Referensi Traccar:** `RuptelaProtocolDecoder.java`

### Frame Structure

Mirip Teltonika (AVL data format):
```
Preamble(4B: 0x00000000) | DataLength(4B) | CodecID(1B) | Count(1B) | AVL Records(N) | CRC(4B)
```

### Key Notes

1. Sangat mirip Teltonika Codec 8
2. Support BLE sensors
3. IO elements dengan ID panjang
4. Support kilometerage/jamometer